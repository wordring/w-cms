package cms

// ─────────────────────────────────────────────────────────────────────────
// .eml の取り込み係——汎用寄りの同梱拡張（2026-09-01）
//
// メール（RFC 5322 / MIME）を**通信記録ページ**へ変換します。
//
//	<h1>件名</h1>
//	<dl data-type="tags">差出人・宛先・受信日時（ISO 8601・ローカル時刻＋オフセット）</dl>
//	本文（text/plain を段落へ）
//	📎 添付（files/ へ保存・リンクは生成ID・download 属性が元名を運ぶ）
//
// メタはすべて**可変タグ**で持つ——名前：値なら索引に載り、検索も参照も
// 既存の仕組みがそのまま効く（発注書としての解釈は次の段＝板金部の既定セットの
// 仕事で、この係は「メールを記録として残す」ことしかしない）。
//
// 和文メールの文字コード（ISO-2022-JP・Shift_JIS 等）は x/text で復号します
// （開発方針 §1 の承認済み依存——「通信記録の取り込みで直接利用予定」の予定が
// ここで現実になった）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

func init() {
	RegisterIntake(emlIntake{})
}

type emlIntake struct{}

func (emlIntake) Name() string         { return "eml" }
func (emlIntake) Extensions() []string { return []string{".eml"} }

// charsetReader は MIME の charset 名から復号リーダーを作ります（ISO-2022-JP 等）。
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("未対応の文字コード: %s", charset)
	}
	return transform.NewReader(input, enc.NewDecoder()), nil
}

// wordDecoder は件名・差出人などのヘッダ（=?ISO-2022-JP?B?…?=）の復号器です。
var wordDecoder = mime.WordDecoder{CharsetReader: charsetReader}

// decodeHeader はヘッダ値を復号します（復号できない部分は原文のまま）。
func decodeHeader(s string) string {
	out, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// emlPart は展開した MIME パートです。
type emlPart struct {
	fileName  string // 添付なら元のファイル名（無ければ空＝本文候補）
	mediaType string
	body      []byte
}

// MessageIDTag は重複検知の鍵を置くタグの名前です。
//
// **専用テーブルは持ちません**（D-1 でドメイン表は全廃）。鍵の置き場は可変タグ、
// つまり**見える文字**しかありません——「見える文字がデータの手掛かり」
// （コンセプト §2）がここでも効いて、`vocab_index` の逆引き（`pagesByTag`）が
// そのまま重複判定になります。新しい仕組みは1つも要りません。
const MessageIDTag = "メッセージID"

// SourceRef は重複検知の鍵（Message-ID）を返します。**鍵の取り出しは形式を知る
// 取り込み係の仕事**で、照合の仕組みはコアが持ちます（intake.go）。
//
// Message-ID が無いメールは珍しくない（手で組んだもの・一部のFAXゲートウェイ）。
// **鍵が無いことは異常ではない**ので ok=false を返すだけで、取り込みは止めません
// ——重複検知が効かないより、記録が残らないほうが困ります。
func (emlIntake) SourceRef(fileName string, content []byte) (string, string, bool) {
	msg, err := mail.ReadMessage(strings.NewReader(string(content)))
	if err != nil {
		return "", "", false // 壊れたメールは OnFile が理由つきで断る
	}
	id := strings.TrimSpace(msg.Header.Get("Message-ID"))
	if id == "" {
		return "", "", false
	}
	return MessageIDTag, id, true
}

// OnFile は .eml を通信記録ページにします。
func (emlIntake) OnFile(ctx *IntakeContext, fileName string, content []byte) (string, string, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(content)))
	if err != nil {
		return "", "", fmt.Errorf("メールとして読めません: %w", err)
	}

	subject := strings.TrimSpace(decodeHeader(msg.Header.Get("Subject")))
	if subject == "" {
		subject = "（件名なし）"
	}
	from := decodeHeader(msg.Header.Get("From"))
	to := decodeHeader(msg.Header.Get("To"))
	dateISO := ""
	if t, err := msg.Header.Date(); err == nil {
		// 日時は ISO 8601 が全域の正（要件 §3）。表記は**運用者のローカル時刻＋
		// オフセット**（例: 2026-09-01T10:30:00+09:00）——UTC の Z 表記は人が
		// 読み違えるため（2026-09-01 ユーザー要望「ISO表記の範囲内でローカル時刻に」）。
		dateISO = t.In(time.Local).Format(time.RFC3339)
	}

	parts, err := collectParts(msg.Header.Get("Content-Type"),
		msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
	if err != nil {
		return "", "", err
	}

	// 先にページを作り（添付の置き場＝新ページのIDが要る）、添付を置いてから
	// リンク入りの本文で確定する。
	pageID, err := ctx.CreatePage("<h1>" + html.EscapeString(subject) + "</h1>")
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(subject) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	writeTag(&b, "差出人", from)
	writeTag(&b, "宛先", to)
	writeTag(&b, "受信日時", dateISO)
	// 重複検知の鍵。**見える文字として置く**——専用テーブルは無く、索引の逆引き
	// （pagesByTag）が判定そのものになる。人にとっては普段読まない値だが、
	// 「機械が使う値も本文にある」という原則を曲げてまで隠す理由が無い。
	writeTag(&b, MessageIDTag, strings.TrimSpace(msg.Header.Get("Message-ID")))
	b.WriteString("</dl>")

	bodyWritten := false
	for _, p := range parts {
		if p.fileName == "" && !bodyWritten && strings.HasPrefix(p.mediaType, "text/plain") {
			for _, para := range strings.Split(strings.ReplaceAll(string(p.body), "\r\n", "\n"), "\n") {
				para = strings.TrimRight(para, " \t")
				if strings.TrimSpace(para) == "" {
					continue
				}
				b.WriteString("<p>" + html.EscapeString(para) + "</p>")
			}
			bodyWritten = true
		}
	}
	for _, p := range parts {
		if p.fileName == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p.fileName))
		if ext == "" {
			ext = ".bin"
		}
		id, href, err := ctx.SaveAttachment(pageID, ext, p.body)
		if err != nil {
			return "", "", err
		}
		b.WriteString(`<p data-id="` + html.EscapeString(id) + `">📎 <a href="` +
			html.EscapeString(href) + `" download="` + html.EscapeString(p.fileName) + `">` +
			html.EscapeString(p.fileName) + `</a></p>`)
	}

	if err := ctx.UpdatePage(pageID, b.String()); err != nil {
		return "", "", err
	}
	return pageID, subject, nil
}

// writeTag は値のあるタグだけを書きます。
func writeTag(b *strings.Builder, name, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("<dt>" + html.EscapeString(name) + "</dt><dd>" + html.EscapeString(value) + "</dd>")
}

// collectParts は MIME を展開して（本文候補と添付の）平らな一覧にします。
// multipart は入れ子ごと辿り、text/* は宣言された文字コードから UTF-8 へ復号します。
func collectParts(contentType, cte string, body io.Reader) ([]emlPart, error) {
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType, params = "text/plain", nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("multipart に boundary がありません")
		}
		mr := multipart.NewReader(body, boundary)
		var out []emlPart
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return out, nil // 途中で壊れていても、読めた分は取り込む
			}
			sub, err := collectParts(p.Header.Get("Content-Type"),
				p.Header.Get("Content-Transfer-Encoding"), p)
			if err == nil {
				// パート自身のファイル名（添付の印）を優先する
				if fn := p.FileName(); fn != "" {
					for i := range sub {
						if sub[i].fileName == "" {
							sub[i].fileName = decodeHeader(fn)
						}
					}
				}
				out = append(out, sub...)
			}
			p.Close()
		}
		return out, nil
	}

	// 単一パート: 転送符号化を解いてから、text/* は文字コードを復号する。
	r := body
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		r = newBase64Reader(r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(mediaType, "text/") {
		if cs := params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") {
			if dec, err := charsetReader(cs, strings.NewReader(string(raw))); err == nil {
				if converted, err := io.ReadAll(dec); err == nil {
					raw = converted
				}
			}
		}
	}
	return []emlPart{{mediaType: mediaType, body: raw}}, nil
}

// newBase64Reader は改行入りの base64 本文を読むリーダーです。
func newBase64Reader(r io.Reader) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, &newlineStripper{r: r})
}

// newlineStripper は CR/LF を読み飛ばします（MIME の base64 は 76 桁で折られる）。
type newlineStripper struct{ r io.Reader }

func (n *newlineStripper) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	for {
		m, err := n.r.Read(buf)
		w := 0
		for _, c := range buf[:m] {
			if c != '\r' && c != '\n' {
				p[w] = c
				w++
			}
		}
		if w > 0 || err != nil {
			return w, err
		}
	}
}
