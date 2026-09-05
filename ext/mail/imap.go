package mail

// ─────────────────────────────────────────────────────────────────────────
// IMAP でメールを取ってくる（2026-09-05）
//
// **標準ライブラリだけで書いています**（`net`・`crypto/tls`・`bufio`・`encoding/base64`）。
// IMAP4rev1 のうち、この用途に要る部分だけを実装したものです——外部依存を増やさない
// ため（[docs/開発方針.md] §1）で、使うコマンドは5つしかありません:
//
//	AUTHENTICATE … 認証（いまは XOAUTH2。移設後はパスワード＝下の「継ぎ目」）
//	EXAMINE      … 受信箱を**読み取り専用**で開く（SELECT ではない。既読にしない）
//	UID SEARCH   … 期間で絞って UID を得る
//	UID FETCH    … 封筒の見出し（重複判定用）と、生MIME本体
//	LOGOUT
//
// **既読にしません。** `EXAMINE`（読み取り専用）＋ `BODY.PEEK[]` の2枚重ねです
// ——w-cms が取り込んだせいで Outlook 側の未読が消えると、人の作業の目印が壊れます。
//
// ── 接続先と認証は差し替えられる継ぎ目です ──
//
// メールサーバーはレンタルへ移ることが決まっています（2026-09-05 ユーザー）。
// 移設までは M365 を使い続けるので、**通信の骨格（IMAP／SMTP）は据え置き、
// 変わるのは接続先と認証方式だけ**という形にしてあります:
//
//	いま     … outlook.office365.com:993 ＋ XOAUTH2（OAuth2 のアクセストークン）
//	移設後   … 借りたホスト ＋ パスワード（AUTHENTICATE PLAIN・TLS の上）
//
// 接続先は環境変数で差し替えます（既定は M365）。パスワード認証は移設先が
// 決まってから足します——ホストも分からないうちに書くと、確かめようがありません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// envIMAPHost は接続先の差し替え口です（`ホスト:ポート`）。
const envIMAPHost = "WCMS_MAIL_IMAP_HOST"

// defaultIMAPHost は M365 の IMAP です（移設までの既定）。
const defaultIMAPHost = "outlook.office365.com:993"

// imapHost はいまの接続先を返します。
func imapHost() string {
	if h := strings.TrimSpace(os.Getenv(envIMAPHost)); h != "" {
		return h
	}
	return defaultIMAPHost
}

// imapDialTimeout は接続と読み書きの時間切れです。**必ず持たせること**
// ——相手が黙ったまま返さないと、取り込みの要求がそのまま居座ります。
const imapDialTimeout = 60 * time.Second

// imapSession は1本の接続です。IMAP は**状態を持つ**ので（開いた箱・認証状態）、
// 取り込み1回につき1本を開いて使い回します。1通ごとに繋ぎ直すのは無駄です。
type imapSession struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	seq  int
}

// openIMAP は接続してログインし、受信箱を読み取り専用で開きます。
func openIMAP(ctx context.Context, username string) (*imapSession, error) {
	token, err := mailAccessToken(ctx, username)
	if err != nil {
		return nil, err
	}
	addr := SignedInAddress(username)
	if addr == "" {
		return nil, errNotSignedIn
	}

	host := imapHost()
	serverName, _, err := net.SplitHostPort(host)
	if err != nil {
		return nil, errors.New("IMAPの接続先が「ホスト:ポート」の形ではありません: " + host)
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, errors.New("IMAPへ接続できません: " + err.Error())
	}
	conn := tls.Client(raw, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12})
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, errors.New("IMAPのTLSに失敗しました: " + err.Error())
	}
	// 以後の読み書きに期限を掛けます（ctx は Dial までしか効きません）。
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(imapDialTimeout))
	}

	s := &imapSession{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	if _, err := s.readLine(); err != nil { // サーバーの挨拶
		conn.Close()
		return nil, errors.New("IMAPの応答がありません: " + err.Error())
	}
	if err := s.authXOAUTH2(addr, token); err != nil {
		conn.Close()
		return nil, err
	}
	// **EXAMINE は読み取り専用の SELECT** です（既読の印を付けない）。
	if _, err := s.command("EXAMINE INBOX"); err != nil {
		conn.Close()
		return nil, errors.New("受信箱を開けません: " + err.Error())
	}
	return s, nil
}

// Close はログアウトして接続を閉じます。
func (s *imapSession) Close() {
	s.command("LOGOUT")
	s.conn.Close()
}

// authXOAUTH2 は OAuth2 のアクセストークンで認証します。
//
// 形は SMTP の XOAUTH2 と同じ（`user=…^Aauth=Bearer …^A^A`）で、IMAP では
// base64 にして `AUTHENTICATE XOAUTH2 <値>` として渡します。**失敗すると
// サーバーは理由を base64 の JSON で返す**ので、続きの行を読んで持ち上げます
// ——「認証できません」だけでは運用側が直しようがありません。
func (s *imapSession) authXOAUTH2(addr, token string) error {
	init := base64.StdEncoding.EncodeToString(
		[]byte("user=" + addr + "\x01auth=Bearer " + token + "\x01\x01"))
	tag := s.nextTag()
	if err := s.send(tag + " AUTHENTICATE XOAUTH2 " + init); err != nil {
		return err
	}
	for {
		line, err := s.readLine()
		if err != nil {
			return err
		}
		if strings.HasPrefix(line, "+") {
			// 継続要求＝失敗の合図。中身が理由なので、空行を送って会話を終える。
			detail := strings.TrimSpace(strings.TrimPrefix(line, "+"))
			if raw, err := base64.StdEncoding.DecodeString(detail); err == nil {
				detail = string(raw)
			}
			s.send("")
			s.readLine()
			return errors.New("IMAPの認証に失敗しました: " + detail)
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return nil
			}
			return errors.New("IMAPの認証に失敗しました: " + line)
		}
		// `* CAPABILITY …` などは読み飛ばす。
	}
}

// nextTag はコマンドの札を1つ返します。
func (s *imapSession) nextTag() string {
	s.seq++
	return fmt.Sprintf("a%03d", s.seq)
}

// send は1行送ります。
func (s *imapSession) send(line string) error {
	if _, err := s.w.WriteString(line + "\r\n"); err != nil {
		return err
	}
	return s.w.Flush()
}

// readLine は CRLF 終端の1行を返します（末尾の CRLF は落とします）。
func (s *imapSession) readLine() (string, error) {
	line, err := s.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// command は1つのコマンドを送り、完了行まで読んで応答行を返します
// （リテラルを含まない、短い応答のためのもの）。
func (s *imapSession) command(cmd string) ([]string, error) {
	tag := s.nextTag()
	if err := s.send(tag + " " + cmd); err != nil {
		return nil, err
	}
	var out []string
	for {
		line, err := s.readLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return out, nil
			}
			return out, errors.New(strings.TrimSpace(strings.TrimPrefix(line, tag)))
		}
		out = append(out, line)
	}
}

// literalRe が要らないよう、リテラルの検出は末尾の `{N}` を見るだけにします。
// IMAP の応答行は「… {123}」で終わり、次の123バイトが中身です。
func literalSize(line string) (int, bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	i := strings.LastIndex(line, "{")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(line[i+1 : len(line)-1])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// fetch は UID FETCH を投げ、1件ごとに（属性の行, リテラルの中身）を渡します。
//
// **リテラルの読み取りがこの実装の肝**です。IMAP はサイズを先に告げてから
// 生バイトを流すので、行単位で読んでいると本文の改行で崩れます。
func (s *imapSession) fetch(cmd string, onItem func(attrs string, body []byte)) error {
	tag := s.nextTag()
	if err := s.send(tag + " " + cmd); err != nil {
		return err
	}
	for {
		line, err := s.readLine()
		if err != nil {
			return err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return nil
			}
			return errors.New(strings.TrimSpace(strings.TrimPrefix(line, tag)))
		}
		n, ok := literalSize(line)
		if !ok {
			continue // リテラルを伴わない情報行（* OK …）は読み飛ばす
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(s.r, body); err != nil {
			return err
		}
		if onItem != nil {
			onItem(line, body)
		}
		// リテラルの後ろに続く残り（たいてい `)`）を読み捨てる。
		if _, err := s.readLine(); err != nil {
			return err
		}
	}
}

// searchUIDs は期間で絞った UID を**昇順**で返します（IMAP の SEARCH の順）。
//
// `since` は ISO 8601 の日付（空なら全期間）。IMAP の `SINCE` は**日付だけ**で、
// サーバーの受信時刻（INTERNALDATE）を見ます。
func (s *imapSession) searchUIDs(since string) ([]string, error) {
	cmd := "UID SEARCH ALL"
	if d := imapDate(since); d != "" {
		cmd = "UID SEARCH SINCE " + d
	}
	lines, err := s.command(cmd)
	if err != nil {
		return nil, errors.New("メールを探せません: " + err.Error())
	}
	var uids []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "* SEARCH") {
			continue
		}
		for _, f := range strings.Fields(strings.TrimPrefix(line, "* SEARCH")) {
			if _, err := strconv.Atoi(f); err == nil {
				uids = append(uids, f)
			}
		}
	}
	return uids, nil
}

// imapDate は ISO 8601 を IMAP の日付（`25-Aug-2026`）へ直します。
// 読めない値は空を返します——**絞りが効かないほうが、黙って0件になるよりまし**。
func imapDate(iso string) string {
	iso = strings.TrimSpace(iso)
	if iso == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.Format("02-Jan-2006")
		}
	}
	return ""
}
