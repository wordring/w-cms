package mail

// ─────────────────────────────────────────────────────────────────────────
// メールを送り、送信箱へ記録する（2026-09-03）
//
// ユーザー:「返信の本体は送信箱にあるのはどうでしょう？そして、返信元のメールから
// ものぞき見できるのが良いかと」
//
// **送った記録は通信箱の下に立ちます**（受信と同じ年月フォルダ）。返信元の子には
// しません——返信元を持たない新規のメールが行き場を失いますし、送った記録が
// 受け取った記録に従属して見えるためです。繋がりは所有ではなく**参照**で表します:
//
//	送信記録の `返信元` タグ → 返信元の記録ページ（押せば飛ぶ）
//	返信元の側からは**逆引き**で「この記録への返信」が引ける（PagesByTag）
//
// **送信と記録は別の失敗をします。** メールは出たのに記録を作れなかった場合、
// 記録が無いことより「出したかどうか分からない」ほうが困るので、
// **送信の成否をそのまま返し、記録の失敗は理由を添えて知らせます**
// （送ったものを取り消すことはできない——出た事実を隠さない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ReplyRequest は返信1通ぶんの入力です。
type ReplyRequest struct {
	SourcePageID string   `json:"source_page_id"` // 返信元の記録ページ（空なら新規メール）
	To           []string `json:"to"`
	Cc           []string `json:"cc"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
}

// MailSendAPIHandler は POST /api/mail/send です。
func MailSendAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req ReplyRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(cleanAddrs(req.To)) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "宛先が空です")
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "本文が空です")
		return
	}
	source := ""
	if req.SourcePageID != "" {
		norm, ok := page.NormalizeID(req.SourcePageID)
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "返信元のページIDが不正です")
			return
		}
		// **返信元を読めない相手には返信させません**——読めない記録の宛先や件名を
		// 引き写す口になるため（見せ分けと同じ規律）。
		if idInt, err := strconv.Atoi(norm); err != nil || !page.CanView(user, idInt) {
			cms.JSONFail(w, http.StatusNotFound, "返信元のページが見つかりません")
			return
		}
		source = norm
	}

	// 1. 送る。ここで失敗したら記録は作りません（出ていないので）。
	//
	// **返信元のメッセージIDを In-Reply-To に載せます**——これが相手のメールソフトで
	// 元のスレッドに並ぶ条件です（SMTP を選んだ
	// 理由そのもの）。返信元が無い新規メールでは空のまま。
	// **送信はコアの口を通します**（cms.SendMail）。実装を直に呼ばないのは、
	// 「使う側はコアに尋ねる」という mail.go の設計そのもの——この経路が
	// この拡張の中にあるのは偶然で、他の拡張から送るときも同じ口を使います。
	sentID, err := cms.SendMail(user, cms.OutgoingMail{
		To: cleanAddrs(req.To), Cc: cleanAddrs(req.Cc),
		Subject: req.Subject, BodyText: req.Body,
		InReplyTo: sourceMessageID(source),
	})
	if err != nil {
		switch err {
		case cms.ErrNoMailer:
			cms.JSONFail(w, 0, "メール送信のプラグインが入っていません")
		case cms.ErrMailNotSignedIn:
			cms.JSONFail(w, 0, "メールアカウントにサインインしていません（設定からサインインしてください）")
		default:
			cms.JSONFail(w, 0, "送信できませんでした: "+err.Error())
		}
		return
	}
	auth.Audit(user.Username, "mail.send", strings.Join(cleanAddrs(req.To), ",")+" "+req.Subject)

	// 2. 記録する。**ここで失敗しても送信は成功のまま返します**——出た事実を隠さない。
	pageID, recErr := recordSentMail(user, source, sentID, req)
	resp := map[string]any{"success": true, "sent": true}
	if recErr != nil {
		log.Printf("送信記録を作れませんでした user=%s: %v", user.Username, recErr)
		resp["record_error"] = "メールは送信しましたが、送信箱への記録を作れませんでした: " + recErr.Error()
	} else {
		resp["page_id"] = pageID
	}
	json.NewEncoder(w).Encode(resp)
}

// recordSentMail は通信箱の下へ送信の記録ページを作ります。
//
// **受信と同じ箱・同じ年月フォルダ**です（2026-09-05 の統合）。向きは置き場所ではなく
// `向き：送信` のタグが表します。
func recordSentMail(user *auth.User, sourcePageID, messageID string, req ReplyRequest) (string, error) {
	rootID, ok := cms.MailBoxPageID()
	if !ok {
		return "", errNoMailBox
	}
	now := time.Now()
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "（件名なし）"
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(subject) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	cms.WriteTag(&b, cms.DirectionTag, cms.DirectionOut)
	cms.WriteTag(&b, cms.ChannelTag, "メール")
	cms.WriteTag(&b, "差出人アドレス", SignedInAddress(user.Username))
	for _, a := range cleanAddrs(req.To) {
		cms.WriteTag(&b, "宛先アドレス", a)
	}
	for _, a := range cleanAddrs(req.Cc) {
		cms.WriteTag(&b, "CCアドレス", a)
	}
	cms.WriteTag(&b, "送信日時", now.In(time.Local).Format(time.RFC3339))
	// **自分が立てた Message-ID を残します。** 相手がこれに返信すると、その
	// In-Reply-To がここを指すので、**受信の取り込みだけでスレッドが繋がります**
	// （返信元メッセージID の逆引き——既にある仕組みがそのまま効く）。
	cms.WriteTag(&b, cms.MessageIDTag, messageID)
	if src := sourceMessageID(sourcePageID); src != "" {
		cms.WriteTag(&b, "返信元メッセージID", src)
	}
	// **返信元は参照タグ**（`ページID`）——押せば飛び、逆引きで「この記録への返信」も
	// 引けます。返信元が無い新規メールでは書きません（分からないことを書かない）。
	cms.WriteTag(&b, cms.ReplySourceTag, sourcePageID)
	b.WriteString("</dl>")
	// 本文は平文のまま段落へ。HTMLメールは作らないので、見たままが送った中身です。
	for _, line := range strings.Split(req.Body, "\n") {
		b.WriteString("<p>" + html.EscapeString(line) + "</p>")
	}

	return cms.CreateRecordPage(rootID, user.Username, now, b.String())
}

// cleanAddrs は空白を落とし、空の要素を除きます。
func cleanAddrs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		for _, part := range strings.Split(a, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// errNoMailBox は通信箱ページが無い印です。
var errNoMailBox = errNoMailBoxErr{}

type errNoMailBoxErr struct{}

func (errNoMailBoxErr) Error() string {
	return "通信箱ページがありません（トップ直下に「" + cms.MailBoxTitle + "」という名前のページを作ってください）"
}

// sourceMessageID は返信元ページの「メッセージID」タグを読みます（無ければ空）。
//
// **索引から直接読みます**——本文を解釈し直さないため。取り込みが書いた値が正本で、
// 受信メールの Message-ID がそのまま入っています。
func sourceMessageID(sourcePageID string) string {
	if sourcePageID == "" {
		return ""
	}
	idInt, err := strconv.Atoi(sourcePageID)
	if err != nil {
		return ""
	}
	var v string
	database.DB.QueryRow(
		`SELECT value FROM vocab_index WHERE page_id = ? AND field = ? LIMIT 1`,
		idInt, cms.MessageIDTag).Scan(&v)
	return strings.TrimSpace(v)
}
