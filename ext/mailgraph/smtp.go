package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// SMTP でメールを送る（OAuth2・2026-09-03）
//
// **送信だけ SMTP、受信は Graph** です。ユーザーの選択（2026-09-03）で、理由は2つ:
//
//   - **`In-Reply-To` を立てられる**。Graph の `sendMail` は独自ヘッダ（`x-` 始まり）
//     しか受け付けず、実物で確かめると `InvalidInternetMessageHeader` で 400 になります。
//     返信が相手のメールソフトで元のスレッドに並ぶには、このヘッダが要ります。
//   - **添付の上限が大きい**。Graph の簡易送信は3MiB、SMTP なら M365 で約35MB。
//     図面PDFを何枚か添えると3MiBは簡単に超えます。
//
// **引き換えに失うもの**: Exchange Online は**SMTPで投函されたメールを
// 「送信済みアイテム」へ保存しません**。Outlook から見ると送った形跡が残らないので、
// **控えは w-cms の送信箱が担います**（それを承知でこちらを選んだ・reply.go）。
//
// 認証は XOAUTH2——**パスワードは使いません**。Graph と同じデバイスコードで得た
// トークンをそのまま使います（要求する権限に `SMTP.Send` が要る）。
//
// MIME は自分で組みます（`mime/multipart`・`mime`・`net/smtp` はすべて標準ライブラリ）。
// 件名は RFC 2047 の符号化語、本文は UTF-8 の base64——和文をそのまま置くと
// 8bit のまま流れて経路によっては壊れます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"w-cms/internal/cms"
)

// smtpHost は Exchange Online の投函口です。
const (
	smtpHost = "smtp.office365.com"
	smtpPort = "587"
)

// smtpScope は SMTP 投函のための権限です。**Graph ではなく Exchange Online の側**に
// あるので、Entra のアクセス許可も「所属する組織で使用している API」から足します。
const smtpScope = "https://outlook.office.com/SMTP.Send"

// xoauth2Auth は SMTP の XOAUTH2 認証です（net/smtp は PLAIN と CRAM-MD5 しか
// 持たないので自分で実装します）。
type xoauth2Auth struct {
	user  string
	token string
}

func (a *xoauth2Auth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	// 形式は `user=<アドレス>^Aauth=Bearer <トークン>^A^A`（^A は 0x01）。
	init := "user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"
	return "XOAUTH2", []byte(init), nil
}

func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// 失敗すると**サーバーが理由をbase64のJSONで返してきます**。空を返して
	// 会話を終わらせつつ、理由をそのまま持ち上げます（黙って失敗させない）。
	detail := string(fromServer)
	if raw, err := base64.StdEncoding.DecodeString(detail); err == nil {
		detail = string(raw)
	}
	return nil, errors.New("SMTPの認証に失敗しました: " + detail)
}

// sendViaSMTP は1通送り、立てた Message-ID を返します。
//
// **Message-ID を返すのは意味があります**——送信箱の記録に残しておけば、相手からの
// 返信（`In-Reply-To` にこれが入る）が取り込まれたときに、既存のスレッドの仕組みで
// そのまま繋がります。
func sendViaSMTP(ctx context.Context, username string, msg cms.OutgoingMail) (string, error) {
	token, err := smtpAccessToken(ctx, username)
	if err != nil {
		return "", err
	}
	from := SignedInAddress(username)
	if from == "" {
		return "", errNotSignedIn
	}

	messageID := newMessageID(from)
	body, err := buildMIME(from, messageID, msg)
	if err != nil {
		return "", err
	}

	rcpt := append(append([]string{}, msg.To...), msg.Cc...)
	if err := smtpDeliver(ctx, from, token, rcpt, body); err != nil {
		return "", err
	}
	return messageID, nil
}

// smtpDeliver は投函の会話だけを持ちます（組み立てとは分けておく）。
func smtpDeliver(ctx context.Context, from, token string, rcpt []string, body []byte) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", smtpHost+":"+smtpPort)
	if err != nil {
		return errors.New("SMTPへ接続できません: " + err.Error())
	}
	c, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		conn.Close()
		return errors.New("SMTPの開始に失敗しました: " + err.Error())
	}
	defer c.Close()

	if err := c.StartTLS(&tls.Config{ServerName: smtpHost, MinVersion: tls.VersionTLS12}); err != nil {
		return errors.New("SMTPのTLSに失敗しました: " + err.Error())
	}
	if err := c.Auth(&xoauth2Auth{user: from, token: token}); err != nil {
		return err // 理由は Next が持ち上げている
	}
	if err := c.Mail(from); err != nil {
		return errors.New("差出人を受け付けられません: " + err.Error())
	}
	for _, to := range rcpt {
		if err := c.Rcpt(to); err != nil {
			return errors.New("宛先を受け付けられません（" + to + "）: " + err.Error())
		}
	}
	w, err := c.Data()
	if err != nil {
		return errors.New("本文を送れません: " + err.Error())
	}
	if _, err := w.Write(body); err != nil {
		w.Close()
		return errors.New("本文の書き込みに失敗しました: " + err.Error())
	}
	if err := w.Close(); err != nil {
		return errors.New("送信を締められませんでした: " + err.Error())
	}
	return c.Quit()
}

// buildMIME は1通ぶんのMIMEを組みます。
func buildMIME(from, messageID string, msg cms.OutgoingMail) ([]byte, error) {
	var b strings.Builder
	head := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		b.WriteString(name + ": " + value + "\r\n")
	}
	head("From", from)
	head("To", strings.Join(msg.To, ", "))
	head("Cc", strings.Join(msg.Cc, ", "))
	// 件名は RFC 2047 の符号化語（和文をそのまま置くと経路で壊れます）。
	head("Subject", mime.BEncoding.Encode("UTF-8", msg.Subject))
	head("Date", time.Now().Format(time.RFC1123Z))
	head("Message-ID", messageID)
	// **ここが SMTP にした理由**——Graph では立てられなかったヘッダ。
	// References にも同じものを入れます（メールソフトはどちらも見ます）。
	head("In-Reply-To", msg.InReplyTo)
	head("References", msg.InReplyTo)
	head("MIME-Version", "1.0")

	if len(msg.Attachments) == 0 {
		head("Content-Type", `text/plain; charset="UTF-8"`)
		head("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		b.WriteString(wrapBase64(msg.BodyText))
		return []byte(b.String()), nil
	}

	var parts strings.Builder
	mw := multipart.NewWriter(&parts)
	head("Content-Type", `multipart/mixed; boundary="`+mw.Boundary()+`"`)
	b.WriteString("\r\n")

	textHead := textproto.MIMEHeader{}
	textHead.Set("Content-Type", `text/plain; charset="UTF-8"`)
	textHead.Set("Content-Transfer-Encoding", "base64")
	tp, err := mw.CreatePart(textHead)
	if err != nil {
		return nil, err
	}
	if _, err := tp.Write([]byte(wrapBase64(msg.BodyText))); err != nil {
		return nil, err
	}

	for _, a := range msg.Attachments {
		h := textproto.MIMEHeader{}
		ct := a.MIMEType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Transfer-Encoding", "base64")
		// ファイル名も符号化語にします（和文のファイル名は実際に来ます）。
		h.Set("Content-Disposition",
			`attachment; filename="`+mime.BEncoding.Encode("UTF-8", a.Name)+`"`)
		p, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := p.Write([]byte(wrapBytesBase64(a.Content))); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	b.WriteString(parts.String())
	return []byte(b.String()), nil
}

// wrapBase64 は文字列を base64 にして76桁で折ります（RFC 2045 の行長）。
func wrapBase64(s string) string { return wrapBytesBase64([]byte(s)) }

// wrapBytesBase64 はバイト列を base64 にして76桁で折ります。
func wrapBytesBase64(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(enc) > 76 {
		out.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	out.WriteString(enc + "\r\n")
	return out.String()
}

// newMessageID は Message-ID を1つ作ります（右辺は差出人のドメイン）。
func newMessageID(from string) string {
	domain := "w-cms.local"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = from[i+1:]
	}
	var buf [16]byte
	rand.Read(buf[:])
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(buf[:]), domain)
}
