package mail

// ─────────────────────────────────────────────────────────────────────────
// 箱を読むだけの口（2026-09-28）——w-cms の外の道具（tools/mail/fetch）が、メールをフォルダへ落とすため。
//
// 利用者:「メールも同様にデスクトップのフォルダにダウンロードして、今後新たに受信したものは追加で
// ダウンロードする方式で通信量を節約できますか？」「サーバーには私が送信したメールも残っており…移行の間だけ
// w-cmsの外のツールでフォルダにダウンロードできませんか？」。
//
//   - **読み取り専用**（EXAMINE・BODY.PEEK）——既読の印を付けない。受信の取り込み（import.go）と同じ規律。
//   - サインインは w-cms と同じ保管（data/mail/<利用者>.json）を使う——道具はリポジトリの根で動かす。
//   - 送信済みの箱は LIST の `\Sent` の印で探す（箱の名前は言語設定で変わるため、名前で当てない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// SentBox は OpenMailbox に渡す「送信済みの箱」の印です。
const SentBox = `\Sent`

// mirrorOpTimeout は1回の読み（一覧・1通の本体）の時間切れです——接続の期限を操作ごとに延ばす
// （長く落とし続ける道具が、接続を開いたときの期限で途中で切られないように）。
const mirrorOpTimeout = 3 * time.Minute

// Mailbox は読み取り専用で開いた1つの箱です。
type Mailbox struct {
	s           *imapSession
	name        string
	UIDValidity string // 箱の UIDVALIDITY（変わったら UID は振り直されている——控えは使えない）
}

// OpenMailbox は username のアカウントで箱を読み取り専用で開きます。name が空なら受信箱、
// SentBox なら送信済みの箱、それ以外は IMAP の箱の名前そのもの。
func OpenMailbox(ctx context.Context, username, name string) (*Mailbox, error) {
	s, err := connectIMAP(ctx, username)
	if err != nil {
		return nil, err
	}
	s.conn.SetDeadline(time.Now().Add(mirrorOpTimeout))
	box := name
	switch name {
	case "":
		box = "INBOX"
	case SentBox:
		box, err = s.specialBox(SentBox)
		if err != nil {
			s.Close()
			return nil, err
		}
	}
	lines, err := s.command("EXAMINE " + imapQuote(box))
	if err != nil {
		s.Close()
		return nil, errors.New("箱「" + box + "」を開けません: " + err.Error())
	}
	m := &Mailbox{s: s, name: box}
	for _, l := range lines {
		if v := uidValidityRe.FindStringSubmatch(l); v != nil {
			m.UIDValidity = v[1]
		}
	}
	return m, nil
}

var uidValidityRe = regexp.MustCompile(`\[UIDVALIDITY (\d+)\]`)

// listRe は LIST の応答（`* LIST (\HasNoChildren \Sent) "/" "Sent Items"`）です。
var listRe = regexp.MustCompile(`^\* LIST \(([^)]*)\) (?:"(?:[^"\\]|\\.)*"|NIL) (.+)$`)

// specialBox は印（\Sent など）の付いた箱の名前を LIST で探します（引用符つきならそのまま返す）。
func (s *imapSession) specialBox(flag string) (string, error) {
	lines, err := s.command(`LIST "" "*"`)
	if err != nil {
		return "", errors.New("箱の一覧を読めません: " + err.Error())
	}
	for _, l := range lines {
		m := listRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		for _, f := range strings.Fields(m[1]) {
			if strings.EqualFold(f, flag) {
				return strings.TrimSpace(m[2]), nil
			}
		}
	}
	return "", errors.New("送信済みの箱が見つかりません（LIST に " + flag + " の印がありません）")
}

// imapQuote は箱の名前を IMAP の引用符つきの文字列にします（既に引用符つきならそのまま）。
func imapQuote(name string) string {
	if strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) && len(name) >= 2 {
		return name
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
}

// Name は開いた箱の IMAP の名前です（送信済みの箱は言語設定で名前が変わる）。
func (m *Mailbox) Name() string { return strings.Trim(m.name, `"`) }

// UIDs は箱のメールの UID を昇順で返します（since は ISO 8601 の日付・空なら全期間）。
func (m *Mailbox) UIDs(since string) ([]string, error) {
	m.s.conn.SetDeadline(time.Now().Add(mirrorOpTimeout))
	return m.s.searchUIDs(since)
}

// Fetch は1通の生の MIME（`.eml` と同じ中身）を落とします（既読にしない）。
func (m *Mailbox) Fetch(uid string) ([]byte, error) {
	m.s.conn.SetDeadline(time.Now().Add(mirrorOpTimeout))
	return m.s.fetchRaw(uid)
}

// Close はログアウトして接続を閉じます。
func (m *Mailbox) Close() {
	m.s.conn.SetDeadline(time.Now().Add(30 * time.Second))
	m.s.Close()
}
