// Command push は、デスクトップへ落としたメール（tools/mail/fetch）を w-cms の通信箱へ上げます
// （2026-09-28・移行の間だけ使う w-cms の外の道具）。
//
// 利用者:「メールも同様にデスクトップのフォルダにダウンロードして…通信量を節約できますか？」——落としたものを
// 手元の w-cms へ入れるだけなので、**メールのサーバーとは通信しない**（データを初期化して入れ直すたびに
// 落とし直さずに済む）。
//
//   - 通信箱へのアップロードは、メールの取り込みと同じ道（取り込み係・重複は Message-ID で弾く・添付の展開）
//     ——何度流してもよい（入っているものは「重複」で数える）。
//   - 自分が出したメール（送信）は「送信」の記録になる（差出人がサインインしているアドレスなら——w-cms 側）。
//   - **古いものから**上げる（スレッドの親が先に来ると、返信元の逆引きが最初から繋がる）。受信と送信は日付で混ぜる。
//   - -handled-before <日付>: その日より前の受信は、取り込むと同時に「対応：不要」の印を付ける（2026-09-28 利用者:
//     「2026年5月より前の受信は、取り込むと同時に『対応：不要』の印を付けてください」——過去のメールで未処理の一覧を
//     埋めない）。印を付けるのは**この回に新しく入ったものだけ**（前から入っていたものの印は人のものなので触らない）。
//
// 使い方（リポジトリの根で・サーバーを動かしたまま）:
//
//	go run ./tools/mail/push                     # 受信と送信を全部
//	go run ./tools/mail/push -since 2026-01-01   # この日以降だけ
//	go run ./tools/mail/push -max 50             # 50通だけ（試し）
//	go run ./tools/mail/push -handled-before 2026-05-01
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type mailMeta struct {
	File string `json:"ファイル"`
	Date string `json:"日付"`
}

type catalog struct {
	Address string `json:"アドレス"`
	Boxes   map[string]*struct {
		Mails map[string]*mailMeta `json:"メール"`
	} `json:"箱"`
}

type item struct {
	path, date, box string
}

func main() {
	dirFlag := flag.String("dir", "", "落としたメールの置き場（既定はデスクトップの w-cms\\メール）")
	boxes := flag.String("boxes", "受信,送信", "上げる箱")
	since := flag.String("since", "", "この日以降のものだけ（YYYY-MM-DD）")
	max := flag.Int("max", 0, "上げる上限（0 なら全部）")
	handledBefore := flag.String("handled-before", "", "この日より前の受信は「対応：不要」の印を付ける（YYYY-MM-DD）")
	flag.Parse()
	root := *dirFlag
	if root == "" {
		root = filepath.Join(desktop(), "w-cms", "メール")
	}
	if err := run(root, strings.Split(*boxes, ","), *since, *max, *handledBefore); err != nil {
		fmt.Fprintln(os.Stderr, "失敗:", err)
		os.Exit(1)
	}
}

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

func run(root string, boxes []string, since string, max int, handledBefore string) error {
	// 置き場の下のアカウント（目録.json のあるフォルダ）を全部。
	accts, _ := filepath.Glob(filepath.Join(root, "*", "目録.json"))
	if len(accts) == 0 {
		return fmt.Errorf("落としたメールがありません（先に go run ./tools/mail/fetch）: %s", root)
	}
	var items []item
	for _, catPath := range accts {
		b, err := os.ReadFile(catPath)
		if err != nil {
			return err
		}
		var cat catalog
		if err := json.Unmarshal(b, &cat); err != nil {
			return fmt.Errorf("%s を読めません: %w", catPath, err)
		}
		for _, box := range boxes {
			box = strings.TrimSpace(box)
			rec := cat.Boxes[box]
			if rec == nil {
				continue
			}
			for _, m := range rec.Mails {
				if since != "" && m.Date != "" && m.Date[:min(10, len(m.Date))] < since {
					continue
				}
				items = append(items, item{path: filepath.Join(filepath.Dir(catPath), box, m.File), date: m.Date, box: box})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].date < items[j].date }) // 古いものから
	if max > 0 && len(items) > max {
		items = items[:max]
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	inbox, err := c.inboxID()
	if err != nil {
		return err
	}
	counts := map[string]int{}
	var notNeeded []string // 「対応：不要」の印を付ける新しい受信の記録
	start := time.Now()
	for i, it := range items {
		res, err := c.upload(inbox, it.path)
		switch {
		case err != nil:
			counts["失敗"]++
			fmt.Printf("✗ %s: %v\n", filepath.Base(it.path), err)
		case res.Duplicate:
			counts["重複"]++
		default:
			counts[it.box+"を新しく"]++
			if it.box == "受信" && handledBefore != "" && res.PageID != "" && it.date != "" &&
				it.date[:min(10, len(it.date))] < handledBefore {
				notNeeded = append(notNeeded, res.PageID)
			}
		}
		if (i+1)%100 == 0 {
			fmt.Printf("… %d / %d（%s）\n", i+1, len(items), time.Since(start).Round(time.Second))
		}
	}
	fmt.Printf("通信箱へ: 受信を新しく %d・送信を新しく %d・重複 %d・失敗 %d（%d 通・%s）\n",
		counts["受信を新しく"], counts["送信を新しく"], counts["重複"], counts["失敗"], len(items),
		time.Since(start).Round(time.Second))
	if len(notNeeded) > 0 {
		done, err := c.markNotNeeded(notNeeded)
		if err != nil {
			return fmt.Errorf("「対応：不要」の印を付けられません（%d 件中 %d 件まで）: %w", len(notNeeded), done, err)
		}
		fmt.Printf("%s より前の受信 %d 通に「対応：不要」の印を付けました\n", handledBefore, done)
	}
	return nil
}

// ── w-cms への口 ───────────────────────────────────────────────────────

type client struct {
	base string
	hc   *http.Client
}

func newClient() (*client, error) {
	base := strings.TrimRight(os.Getenv("WCMS_BASE"), "/")
	if base == "" {
		base = "https://localhost:8443"
	}
	jar, _ := cookiejar.New(nil)
	c := &client{base: base, hc: &http.Client{
		Jar: jar,
		// 手元のサーバーは自己署名の証明書（ローカル検証のため確かめない）。
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Minute,
	}}
	user, pass := os.Getenv("WCMS_USER"), os.Getenv("WCMS_PASS")
	if user == "" {
		user, pass = "a", "a" // ローカル開発の管理者（引き継ぎ・環境の節）
	}
	res, err := c.do("POST", "/api/login", strings.NewReader(url.Values{"username": {user}, "password": {pass}}.Encode()),
		"application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	if strings.Contains(res.Header.Get("Location"), "error") {
		return nil, errors.New("ログインできません")
	}
	return c, nil
}

func (c *client) do(method, path string, body io.Reader, ctype string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.base)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	return c.hc.Do(req)
}

// inboxID はトップ直下の「通信箱」です（題が機能——引き継ぎ「通信箱は h1 で決まる」）。
func (c *client) inboxID() (string, error) {
	res, err := c.do("GET", "/api/children?parent_id=000000", nil, "")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return "", err
	}
	for _, m := range list {
		t, _ := firstOf(m, "title", "Title").(string)
		if strings.TrimSpace(t) == "通信箱" {
			id, _ := firstOf(m, "id", "ID").(string)
			return id, nil
		}
	}
	return "", errors.New("トップ直下に「通信箱」がありません（置き場を作ってから）")
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

type uploadResult struct {
	Success   bool   `json:"success"`
	Duplicate bool   `json:"duplicate"`
	Title     string `json:"title"`
	PageID    string `json:"page_id"` // 新しく作った記録（重複のときは既にある記録）
}

// markNotNeeded は記録に「対応：不要」の印を付けます（通信箱の「済」の口・100件ずつまとめて）。
func (c *client) markNotNeeded(ids []string) (int, error) {
	done := 0
	for i := 0; i < len(ids); i += 100 {
		part := ids[i:min(i+100, len(ids))]
		b, _ := json.Marshal(map[string]any{"page_ids": part, "value": "不要"})
		res, err := c.do("POST", "/api/intake/handled", bytes.NewReader(b), "application/json")
		if err != nil {
			return done, err
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var r struct {
			Handled int `json:"handled"`
			Failed  int `json:"failed"`
		}
		if res.StatusCode != 200 || json.Unmarshal(body, &r) != nil {
			return done, fmt.Errorf("%d %s", res.StatusCode, strings.TrimSpace(string(body)))
		}
		done += r.Handled
		if r.Failed > 0 {
			// 開いている人がいる記録などは口が飛ばす——付かなかった数を黙らない。
			fmt.Printf("⚠ %d 件は印を付けられませんでした（開いている人がいる・権限が無いなど）\n", r.Failed)
		}
	}
	return done, nil
}

// upload は1通を通信箱へ上げます（取り込み係が引き受ける——編集ロックは要らない）。
func (c *client) upload(inbox, path string) (uploadResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return uploadResult{}, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("page_id", inbox)
	fw, err := w.CreateFormFile("file", "mail.eml")
	if err != nil {
		return uploadResult{}, err
	}
	fw.Write(content)
	w.Close()
	res, err := c.do("POST", "/api/upload-file", &buf, w.FormDataContentType())
	if err != nil {
		return uploadResult{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var r uploadResult
	if res.StatusCode != 200 || json.Unmarshal(body, &r) != nil || !r.Success {
		msg := strings.TrimSpace(string(body))
		if r := []rune(msg); len(r) > 200 {
			msg = string(r[:200]) + "…"
		}
		return uploadResult{}, fmt.Errorf("%d %s", res.StatusCode, msg)
	}
	return r, nil
}
