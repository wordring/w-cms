// Command fetch はメールをデスクトップのフォルダへ落とします（2026-09-28・移行の間だけ使う w-cms の外の道具）。
//
// 利用者:「メールも同様にデスクトップのフォルダにダウンロードして、今後新たに受信したものは追加でダウンロードする
// 方式で通信量を節約できますか？」「サーバーには私が送信したメールも残っており…移行の間だけw-cmsの外のツールで
// フォルダにダウンロードできませんか？」。
//
//   - 置き場はデスクトップの w-cms\メール\<アドレス>\受信\・送信\（1通1ファイルの .eml）。目録.json に UID と
//     見出し（日付・差出人・宛先・件名・Message-ID）を控え、**次からは無いものだけ**落とす。
//   - **読み取り専用**（既読にしない）。サインインは w-cms と同じ保管（data/mail/<利用者>.json）を使う
//     ——w-cms で一度サインインしておき、リポジトリの根で、秘密の設定を読み込んでから動かす。
//   - 送信済みの箱は w-cms が SMTP で送ったメールを含まない（その控えは通信箱にある）——Outlook から送ったもの。
//   - 箱の UIDVALIDITY が変わったら（UID の振り直し）、前の落とし物は「_旧」を付けて脇へ置き、初めから落とす。
//
// 使い方（リポジトリの根で）:
//
//	set -a; . ~/OneDrive/デスクトップ/w-cms/秘密の設定/w-cms.env; set +a
//	go run ./tools/mail/fetch                  # 受信と送信を全部（新しいものから）
//	go run ./tools/mail/fetch -max 100         # 1つの箱につき100通まで（残りは次の回）
//	go run ./tools/mail/fetch -since 2025-01-01
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"mime"
	netmail "net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm/mail"
)

type mailMeta struct {
	File      string `json:"ファイル"`
	Date      string `json:"日付"`
	From      string `json:"差出人"`
	To        string `json:"宛先"`
	Subject   string `json:"件名"`
	MessageID string `json:"Message-ID"`
	Size      int    `json:"大きさ"`
}

type boxRecord struct {
	IMAP        string               `json:"IMAPの箱"`
	UIDValidity string               `json:"UIDVALIDITY"`
	Mails       map[string]*mailMeta `json:"メール"` // UID → 見出し
}

type catalog struct {
	Address string                `json:"アドレス"`
	Updated string                `json:"更新"`
	Boxes   map[string]*boxRecord `json:"箱"` // 受信・送信
}

func main() {
	user := flag.String("user", "a", "w-cms の利用者（メールにサインインした人）")
	dirFlag := flag.String("dir", "", "置き場（既定はデスクトップの w-cms\\メール）")
	since := flag.String("since", "", "この日以降に届いた・送ったものだけ（YYYY-MM-DD・空なら全期間）")
	max := flag.Int("max", 0, "1つの箱で1回に落とす上限（0 なら全部）")
	boxes := flag.String("boxes", "受信,送信", "落とす箱（受信・送信）")
	flag.Parse()
	root := *dirFlag
	if root == "" {
		root = filepath.Join(desktop(), "w-cms", "メール")
	}
	if err := run(*user, root, *since, *max, strings.Split(*boxes, ",")); err != nil {
		fmt.Fprintln(os.Stderr, "失敗:", err)
		os.Exit(1)
	}
}

// desktop はデスクトップの場所です（OneDrive へ移されていればそちら）。
func desktop() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, "OneDrive", "デスクトップ"), filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "Desktop")} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return home
}

var unsafeRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func run(user, root, since string, max int, boxes []string) error {
	addr := mail.SignedInAddress(user)
	if addr == "" {
		return fmt.Errorf("利用者「%s」はメールにサインインしていません（w-cms の画面でサインインしてから・リポジトリの根で動かす）", user)
	}
	acct := filepath.Join(root, unsafeRe.ReplaceAllString(addr, "_"))
	if err := os.MkdirAll(acct, 0o755); err != nil {
		return err
	}
	unlock, err := lock(acct)
	if err != nil {
		return err
	}
	defer unlock()
	logf := logger(filepath.Join(acct, "取り込みの記録.log"))

	catPath := filepath.Join(acct, "目録.json")
	var cat catalog
	if b, err := os.ReadFile(catPath); err == nil {
		json.Unmarshal(b, &cat)
	}
	if cat.Boxes == nil {
		cat.Boxes = map[string]*boxRecord{}
	}
	cat.Address = addr
	save := func() error {
		cat.Updated = time.Now().Format(time.RFC3339)
		b, err := json.MarshalIndent(cat, "", "  ")
		if err != nil {
			return err
		}
		tmp := catPath + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, catPath)
	}

	logf("メールを落とし始めます（%s・期間 %s・上限 %s）", addr, orAll(since), orNone(max))
	for _, label := range boxes {
		label = strings.TrimSpace(label)
		imapName := ""
		switch label {
		case "受信":
		case "送信":
			imapName = mail.SentBox
		default:
			return fmt.Errorf("箱「%s」は分かりません（受信・送信）", label)
		}
		if err := fetchBox(user, acct, label, imapName, since, max, &cat, save, logf); err != nil {
			logf("%s: 止まりました: %v", label, err)
		}
	}
	return save()
}

func fetchBox(user, acct, label, imapName, since string, max int, cat *catalog, save func() error,
	logf func(string, ...any)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	mb, err := mail.OpenMailbox(ctx, user, imapName)
	if err != nil {
		return err
	}
	defer mb.Close()
	dir := filepath.Join(acct, label)
	rec := cat.Boxes[label]
	if rec != nil && rec.UIDValidity != "" && rec.UIDValidity != mb.UIDValidity {
		// UID が振り直された——前の落とし物は脇へ置き、初めから落とす（同じ UID が別のメールを指す）。
		old := dir + "_旧" + rec.UIDValidity
		os.Rename(dir, old)
		logf("%s: 箱の UIDVALIDITY が変わりました（%s → %s）——前の分は %s へ置いて、初めから落とします",
			label, rec.UIDValidity, mb.UIDValidity, filepath.Base(old))
		rec = nil
	}
	if rec == nil {
		rec = &boxRecord{Mails: map[string]*mailMeta{}}
		cat.Boxes[label] = rec
	}
	rec.IMAP, rec.UIDValidity = mb.Name(), mb.UIDValidity
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	uids, err := mb.UIDs(since)
	if err != nil {
		return err
	}
	// 新しいものから（最近の受注・発注が先に揃う）。
	sort.Slice(uids, func(i, j int) bool { return atoi(uids[i]) > atoi(uids[j]) })
	var todo []string
	for _, u := range uids {
		if m, ok := rec.Mails[u]; ok {
			if st, err := os.Stat(filepath.Join(dir, m.File)); err == nil && st.Size() > 0 {
				continue
			}
		}
		todo = append(todo, u)
	}
	got, failed, bytesGot := 0, 0, 0
	for _, u := range todo {
		if max > 0 && got >= max {
			break
		}
		raw, err := mb.Fetch(u)
		if err != nil {
			failed++
			logf("%s: UID %s を落とせません: %v", label, u, err)
			if failed >= 5 {
				return errors.New("落とせないものが続くので止めます（回線を確かめて、次の回に）")
			}
			continue
		}
		meta := metaOf(raw)
		meta.Size = len(raw)
		meta.File = fileName(meta.Date, u)
		if err := writeAtomic(filepath.Join(dir, meta.File), raw); err != nil {
			return err
		}
		rec.Mails[u] = meta
		got++
		bytesGot += len(raw)
		if got%20 == 0 {
			if err := save(); err != nil {
				return err
			}
		}
	}
	if err := save(); err != nil {
		return err
	}
	logf("%s（%s）: 新しく %d 通（%.1f MB）・失敗 %d・まだ %d 通・持っている %d 通",
		label, rec.IMAP, got, float64(bytesGot)/1e6, failed, len(todo)-got-failed, len(rec.Mails))
	return nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// fileName は人が並べて読める名前です（日付_UID.eml）。
func fileName(date, uid string) string {
	if t, err := time.Parse(time.RFC3339, date); err == nil {
		return t.Format("2006-01-02_1504") + "_" + uid + ".eml"
	}
	return "日付不明_" + uid + ".eml"
}

// metaOf は見出しを読みます（件名・差出人は RFC 2047 を復号——人が目録を読むため。読めなければ素のまま）。
func metaOf(raw []byte) *mailMeta {
	m := &mailMeta{}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return m
	}
	var dec mime.WordDecoder
	decode := func(s string) string {
		if d, err := dec.DecodeHeader(s); err == nil {
			return d
		}
		return s
	}
	if t, err := msg.Header.Date(); err == nil {
		m.Date = t.In(time.Local).Format(time.RFC3339)
	}
	m.From = decode(msg.Header.Get("From"))
	m.To = decode(msg.Header.Get("To"))
	m.Subject = decode(msg.Header.Get("Subject"))
	m.MessageID = strings.TrimSpace(msg.Header.Get("Message-Id"))
	return m
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// lock は二重に動かないための印です（タスク スケジューラで繰り返し動かしても重ならない）。3時間より古い印は
// 前の回が落ちた残りとみなして取り直す。
func lock(dir string) (func(), error) {
	p := filepath.Join(dir, "動いています.lock")
	for i := 0; i < 2; i++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > 3*time.Hour {
			os.Remove(p)
			continue
		}
		return nil, errors.New("前の回がまだ動いています（" + p + "）")
	}
	return nil, errors.New("印を置けません: " + p)
}

// logger は画面と記録のファイルの両方へ書きます。
func logger(path string) func(string, ...any) {
	return func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		fmt.Println(s)
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), s)
			f.Close()
		}
	}
}

func orAll(s string) string {
	if s == "" {
		return "全期間"
	}
	return s + " から"
}

func orNone(n int) string {
	if n <= 0 {
		return "なし"
	}
	return fmt.Sprintf("1つの箱で %d 通", n)
}
