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
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm"
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
	// 添付は **w-cms の中にあるもの**を指します（attach.go）。手元のディスクから
	// 選び直さないので、同じファイルが2つに増えません。
	Attachments []AttachRef `json:"attachments"`
}

// MailSendAPIHandler は POST /api/mail/send です。
func MailSendAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
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
	// **送信は通信拡張の口を通します**（comm.SendMail。2026-09-15 までコアに在った口）。
	// 実装を直に呼ばないのは、
	// 「使う側は口に尋ねる」という mail.go の設計そのもの——この経路が
	// この拡張の中にあるのは偶然で、他の拡張から送るときも同じ口を使います。
	//
	// **添付は送る前に読み切ります。** 途中で足りないと分かると「送ったつもりで
	// 届いていない」になるので、揃わなければ1通も送りません。
	files, err := collectAttachments(user, req.Attachments)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	sentID, err := comm.SendMail(user, comm.OutgoingMail{
		To: cleanAddrs(req.To), Cc: cleanAddrs(req.Cc),
		Subject: req.Subject, BodyText: req.Body,
		InReplyTo:   sourceMessageID(source),
		Attachments: files,
	})
	if err != nil {
		switch err {
		case comm.ErrNoMailer:
			cms.JSONFail(w, http.StatusNotImplemented, "メール送信のプラグインが入っていません")
		case comm.ErrMailNotSignedIn:
			cms.JSONFail(w, http.StatusConflict, "メールアカウントにサインインしていません（設定からサインインしてください）")
		default:
			cms.JSONFail(w, http.StatusBadGateway, "送信できませんでした: "+err.Error())
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
	cms.WriteJSON(w, resp)
}

// recordSentMail は通信箱の下へ送信の記録ページを作ります。
//
// **受信と同じ箱・同じ年月フォルダ**です（2026-09-05 の統合）。向きは置き場所ではなく
// `向き：送信` のタグが表します。
func recordSentMail(user *auth.User, sourcePageID, messageID string, req ReplyRequest) (string, error) {
	rootID, ok := comm.MailBoxPageID()
	if !ok {
		return "", comm.ErrNoMailBox
	}
	// 時刻は1回だけ取ります——年月フォルダと `送信日時` が、日付の変わり目で食い違わないように。
	now := time.Now()
	body := sentRecordBody(SignedInAddress(user.Username), sourcePageID,
		sourceMessageID(sourcePageID), messageID, now, req)
	return comm.CreateRecordPage(rootID, user.Username, now, body)
}

// sentRecordBody は送信の控えの本文を組みます。**DBにもファイルにも触りません**
// ——試験で「受信の取り込みと同じ名前で書いているか」を直接確かめるために
// recordSentMail から切り出しました（2026-09-15。廃止した名前で書き続けていた不具合の
// 再発を止めるため）。
func sentRecordBody(from, sourcePageID, sourceMsgID, messageID string, now time.Time, req ReplyRequest) string {
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "（件名なし）"
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(subject) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	cms.WriteTag(&b, comm.DirectionTag, comm.DirectionOut)
	cms.WriteTag(&b, comm.ChannelTag, comm.ChannelMail)
	// **送るという仕事はその場で終わっています。** 控えを作業待ちに並べても
	// 押すことが無いので、ここで印を付けます（2026-09-05）。返信を待つ必要が
	// あるなら、人がこのタグを消せば一覧へ戻ります。
	cms.WriteTag(&b, comm.HandledTag, comm.HandledNotNeeded)
	// **相手のタグは受信の取り込みと同じ名前**（`comm.FromTag` 等）。2026-09-13 の
	// 1人1タグへの移行で受信側だけが追随し、ここは `差出人アドレス` 等の廃止した
	// 名前で書き続けていました（2026-09-15 に発見）。
	cms.WriteTag(&b, comm.FromTag, from)
	for _, a := range cleanAddrs(req.To) {
		cms.WriteTag(&b, comm.ToTag, a)
	}
	for _, a := range cleanAddrs(req.Cc) {
		cms.WriteTag(&b, comm.CcTag, a)
	}
	cms.WriteTag(&b, comm.SentAtTag, now.In(time.Local).Format(time.RFC3339))
	// **自分が立てた Message-ID を残します。** 相手がこれに返信すると、その
	// In-Reply-To がここを指すので、**受信の取り込みだけでスレッドが繋がります**
	// （返信元メッセージID の逆引き——既にある仕組みがそのまま効く）。
	cms.WriteTag(&b, comm.MessageIDTag, messageID)
	// 何件添えたか。**受信側と同じタグ**なので、一覧の 📎 もそのまま出ます。
	if n := len(req.Attachments); n > 0 {
		cms.WriteTag(&b, comm.AttachmentCountTag, strconv.Itoa(n))
	}
	if sourceMsgID != "" {
		cms.WriteTag(&b, comm.InReplyToTag, sourceMsgID)
	}
	// **返信元は参照タグ**（`ページID`）——押せば飛び、逆引きで「この記録への返信」も
	// 引けます。返信元が無い新規メールでは書きません（分からないことを書かない）。
	cms.WriteTag(&b, comm.ReplySourceTag, sourcePageID)
	b.WriteString("</dl>")
	// 本文は平文のまま `<pre>` へ。HTMLメールは作らないので、**見たままが送った中身**です
	// ——段落に割ると空行と字下げが落ち、控えが「送ったもの」と違う形になります
	// （受信側と同じ扱い・2026-09-05）。
	b.WriteString(comm.PlainTextBlockHTML(req.Body))

	// **何を添えたかも控えに残します。** リンクは**元のページのファイルを指します**
	// ——同じものを2つ持たないためで、控えの仕事は「送った事実」を記録することです。
	for _, ref := range req.Attachments {
		pageID, ok := page.NormalizeID(strings.TrimSpace(ref.PageID))
		if !ok {
			continue
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			name = ref.File
		}
		b.WriteString(`<p>📎 <a href="` +
			html.EscapeString(page.AttachmentURLFor(pageID, ref.File)) +
			`" download="` + html.EscapeString(name) + `">` +
			html.EscapeString(name) + `</a></p>`)
	}
	return b.String()
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
	return strings.TrimSpace(cms.PageTagValue(database.DB, idInt, comm.MessageIDTag))
}
