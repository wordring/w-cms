package mail

// ─────────────────────────────────────────────────────────────────────────
// 送る欄の初期値と下書き（2026-09-30）
//
// 利用者:「メール表示、メール送信、メール編集などを部品化したら良いと思います」「メール編集は下書きのことです」。
//
// 画面の送る欄（`assets/mail-compose.js`）は1つで、ここは**その欄に出す中身**を返す口です:
//
//	GET  /api/mail/compose?purpose=返信&page_id=… … 用件の初期値（宛先・件名・署名入りの本文・添付の候補）
//	GET  /api/mail/compose?draft=…                 … 保存した下書き
//	POST /api/mail/draft                            … 下書きを保存する（無ければ作る・あれば書き換える）
//	POST /api/mail/send                             … 送る（reply.go——用件と下書きを受ける）
//
// ⚠ **下書きは通信箱のページです**（`comm.DraftTag`）——家でも職場でも開けて、対応のタグが無いので
// 未処理の一覧に並びます。形は送信の控えと同じテンプレート（通信記録（送信メール））で、
// 送信日時・メッセージID・対応が無く、`下書き`（用件）と `下書きの元`（用件の元のページ）を持ちます。
// 送ると、送った日の控えを作って下書きはごみ箱へ移します（reply.go）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

func init() {
	comm.RegisterSendPurpose(comm.PurposeReply, comm.SendPurpose{
		Defaults: replyDefaults, InReplyTo: true, NeedsPage: true,
	})
	comm.RegisterSendPurpose(comm.PurposeNew, comm.SendPurpose{Defaults: newMailDefaults})
}

// quoteMaxLines は返信に引用する元の本文の行数の上限です（長いメールを丸ごと引用しない）。
const quoteMaxLines = 60

// reSubject は件名が既に返信の印（`RE:`・`Re:`）で始まっているかです（`RE: RE:` を重ねない）。
var reSubject = regexp.MustCompile(`(?i)^\s*re\s*:`)

// signatureBlock はメールの署名を本文に置く形にします（無ければ空）。
func signatureBlock(user *auth.User) string {
	lines := contacts.MySignature(user, contacts.MailSignatureHeading)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// newMailDefaults は新しいメールの初期値です（本文は署名だけ）。
func newMailDefaults(user *auth.User, _ string) (comm.ComposeDraft, error) {
	d := comm.ComposeDraft{Purpose: comm.PurposeNew}
	if sig := signatureBlock(user); sig != "" {
		d.Body = "\n\n" + sig
	}
	return d, nil
}

// replyDefaults は通信記録への返信の初期値です。
//
//   - 宛先は `返信先`（Reply-To）が先、無ければ `差出人`——**素のアドレスだけ**（SMTP の `RCPT TO:` の都合）
//   - 件名は元の題に `RE: ` を1つだけ（重ねない）
//   - 本文は 空行・署名・「○○さんは書きました:」・元の本文の引用（`> `・60行まで）
//   - 添付の候補は元の記録のファイル（**印は付けない**——回すかどうかは人が決める）
func replyDefaults(user *auth.User, pageID string) (comm.ComposeDraft, error) {
	d := comm.ComposeDraft{Purpose: comm.PurposeReply, PageID: pageID}
	id, _ := strconv.Atoi(pageID)
	tags, _ := cms.TagsOfPage(database.DB, id)
	first := func(name string) string {
		if v := tags[name]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	to := first(comm.ReplyToTag)
	if to == "" {
		to = first(comm.FromTag)
	}
	if to = bareAddr(to); to != "" {
		d.To = []string{to}
	}
	subj := cms.PageTitleByID(id)
	if !reSubject.MatchString(subj) {
		subj = "RE: " + subj
	}
	d.Subject = subj

	body, _ := cms.ReadPageBody(pageID)
	var b strings.Builder
	b.WriteString("\n\n")
	if sig := signatureBlock(user); sig != "" {
		b.WriteString(sig + "\n")
	}
	if text := strings.TrimRight(mailBodyText(body), " \t\r\n"); strings.TrimSpace(text) != "" {
		if from := first(comm.FromTag); from != "" {
			b.WriteString(from + " さんは書きました:\n")
		}
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		if len(lines) > quoteMaxLines {
			lines = lines[:quoteMaxLines]
		}
		for _, ln := range lines {
			b.WriteString("> " + strings.TrimRight(ln, " \t\r") + "\n")
		}
	}
	d.Body = b.String()
	d.Attachments = attachmentLinks(body)
	return d, nil
}

// bareAddr は `名前 <アドレス>` からアドレスだけを取り出します（山括弧が無ければ全体）。
func bareAddr(s string) string {
	v := strings.TrimSpace(s)
	if i := strings.LastIndex(v, "<"); i >= 0 {
		if j := strings.Index(v[i:], ">"); j > 0 {
			v = v[i+1 : i+j]
		}
	}
	return strings.TrimSpace(v)
}

// mailBodyText は記録の本文を平文で返します（本体は comm.RecordBodyText——2026-10-01 に上げた）。
func mailBodyText(bodyHTML string) string { return comm.RecordBodyText(bodyHTML) }

// cleanAttachHref はページの添付のきれいなURL（`/<6桁>/<生成ID>.<拡張子>`）です。
var cleanAttachHref = regexp.MustCompile(`^/(\d{6})/([0-9a-z]+\.[0-9a-z]+)$`)

// attachmentLinks は本文にある添付のリンク（📎 と 📧）を候補にします（印は付けない）。
//
// ⚠ **形を必ず検査します**（クロームがリンクから何かを導出するときの決まり）——きれいなURLの形の
// リンクだけを候補にし、外へのリンクは拾いません。
func attachmentLinks(bodyHTML string) []comm.ComposeAttachment {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []comm.ComposeAttachment
	for _, root := range nodes {
		cms.WalkElements(root, func(n *html.Node) {
			if n.Data != "a" {
				return
			}
			m := cleanAttachHref.FindStringSubmatch(strings.ToLower(cms.Attr(n, "href")))
			if m == nil || seen[m[1]+"/"+m[2]] {
				return
			}
			seen[m[1]+"/"+m[2]] = true
			name := strings.TrimSpace(cms.Attr(n, "download"))
			if name == "" {
				name = strings.TrimSpace(nodeText(n))
			}
			if name == "" {
				name = m[2]
			}
			out = append(out, comm.ComposeAttachment{PageID: m[1], File: m[2], Name: name})
		})
	}
	return out
}

// sectionHeadingText は節の直下の見出しの文字です。
func sectionHeadingText(section *html.Node) string {
	for c := section.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			switch c.Data {
			case "h2", "h3", "h4", "h5", "h6":
				return strings.TrimSpace(nodeText(c))
			}
		}
	}
	return ""
}

// nodeText は要素の中の文字をつなげて返します（`<br>` は改行）。
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch {
		case x.Type == html.TextNode:
			sb.WriteString(x.Data)
		case x.Type == html.ElementNode && x.Data == "br":
			sb.WriteString("\n")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// ─── 下書き ───────────────────────────────────────────────────────────────

// draftOf は下書きのページを送る欄の中身へ読み戻します（下書きでなければ ok=false）。
func draftOf(draftID string) (comm.ComposeDraft, bool) {
	id, err := strconv.Atoi(draftID)
	if err != nil {
		return comm.ComposeDraft{}, false
	}
	tags, err := cms.TagsOfPage(database.DB, id)
	if err != nil || len(tags[comm.DraftTag]) == 0 {
		return comm.ComposeDraft{}, false
	}
	body, err := cms.ReadPageBody(draftID)
	if err != nil {
		return comm.ComposeDraft{}, false
	}
	d := comm.ComposeDraft{
		Purpose: strings.TrimSpace(tags[comm.DraftTag][0]),
		DraftID: draftID,
		To:      nonEmpty(tags[comm.ToTag]),
		Cc:      nonEmpty(tags[comm.CcTag]),
		Subject: cms.PageTitleByID(id),
		Body:    mailBodyText(body),
	}
	if src := tags[comm.DraftSourceTag]; len(src) > 0 {
		if norm, ok := page.NormalizeID(strings.TrimSpace(src[0])); ok {
			d.PageID = norm
		}
	}
	if d.Subject == "（件名なし）" {
		d.Subject = ""
	}
	// 下書きに書いた添付は、送るつもりで選んだもの（印を付けて戻す）。
	for _, a := range attachmentLinks(body) {
		a.Checked = true
		d.Attachments = append(d.Attachments, a)
	}
	return d, true
}

func nonEmpty(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// draftRecordBody は下書きのページの本文を組みます（送信の控えと同じテンプレート）。
func draftRecordBody(tmpl, from, purpose, sourcePageID string, req ReplyRequest) (string, error) {
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "（件名なし）"
	}
	d := cms.NewPageDraft(SentTemplate, tmpl)
	d.SetTitle(subject)
	d.SetTag(comm.DirectionTag, comm.DirectionOut)
	d.SetTag(comm.ChannelTag, comm.ChannelMail)
	d.SetTag(comm.DraftTag, purpose)
	d.SetTag(comm.DraftSourceTag, sourcePageID)
	d.SetTag(comm.FromTag, from)
	d.SetTag(comm.ToTag, cleanAddrs(req.To)...)
	d.SetTag(comm.CcTag, cleanAddrs(req.Cc)...)
	// ⚠ **`<pre>` の直後の改行は、HTML として読むと1つ落ちます**（仕様）。書いたとき空行で始まる本文
	// （返信の初期値は空行から始まる）が、開き直すたびに1行ずつ詰まらないよう1つ補います。
	if strings.HasPrefix(strings.ReplaceAll(req.Body, "\r\n", "\n"), "\n") {
		req.Body = "\n" + req.Body
	}
	if err := fillMailBody(d, req); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// mailReady は user がいま送れるか（メールの拡張が入っていて、サインイン済みか）です。
func mailReady(user *auth.User) bool {
	m, ok := comm.CurrentMailer()
	return ok && m.Ready(user)
}

// ComposeAPIHandler は GET /api/mail/compose です（用件の初期値か、保存した下書き）。
func ComposeAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	q := r.URL.Query()
	var d comm.ComposeDraft
	if raw := strings.TrimSpace(q.Get("draft")); raw != "" {
		id, ok := page.NormalizeID(raw)
		n, _ := strconv.Atoi(id)
		if !ok || !page.CanView(user, n) {
			cms.JSONFail(w, http.StatusNotFound, "下書きが見つかりません")
			return
		}
		if d, ok = draftOf(id); !ok {
			cms.JSONFail(w, http.StatusNotFound, "このページは下書きではありません")
			return
		}
		// 送ると何が起きるか（発注書なら PDF・発注済み）は、いまの用件から引き直す。
		if p, ok := comm.SendPurposeOf(d.Purpose); ok && p.Defaults != nil && (d.PageID != "" || !p.NeedsPage) {
			if def, err := p.Defaults(user, d.PageID); err == nil {
				d.SendNote, d.Reload = def.SendNote, def.Reload
			}
		}
	} else {
		name := strings.TrimSpace(q.Get("purpose"))
		p, ok := comm.SendPurposeOf(name)
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "用件「"+name+"」はありません")
			return
		}
		pageID, ok := purposePage(w, user, p, q.Get("page_id"))
		if !ok {
			return
		}
		d = comm.ComposeDraft{Purpose: name, PageID: pageID}
		if p.Defaults != nil {
			def, err := p.Defaults(user, pageID)
			if err != nil {
				cms.JSONFail(w, http.StatusBadRequest, err.Error())
				return
			}
			d = def
			d.Purpose, d.PageID = name, pageID
		}
	}
	if d.To == nil {
		d.To = []string{}
	}
	if d.Cc == nil {
		d.Cc = []string{}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "draft": d, "ready": mailReady(user)})
}

// purposePage は用件の元のページを確かめます（要る用件で空・読めない・形が違えば断る）。
func purposePage(w http.ResponseWriter, user *auth.User, p comm.SendPurpose, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if p.NeedsPage {
			cms.JSONFail(w, http.StatusBadRequest, "元のページがありません")
			return "", false
		}
		return "", true
	}
	id, ok := page.NormalizeID(raw)
	n, _ := strconv.Atoi(id)
	if !ok || !page.CanView(user, n) {
		// **読めないページは「無い」と同じ顔**（読めない記録の宛先や件名を引き写す口にしない）。
		cms.JSONFail(w, http.StatusNotFound, "元のページが見つかりません")
		return "", false
	}
	return id, true
}

// DraftRequest は下書きの保存の入力です。
type DraftRequest struct {
	ReplyRequest
	DraftID string `json:"draft_id"` // 書き換える下書き（空なら新しく作る）
	Purpose string `json:"purpose"`
	PageID  string `json:"page_id"` // 用件の元のページ
}

// DraftSaveAPIHandler は POST /api/mail/draft です（下書きを作る・書き換える）。
//
// ⚠ **書き換えは下書きのページだけ**です——`下書き` のタグが無いページを draft_id に渡されても断ります
// （この口で任意のページを上書きできないように）。⚠ **エディタで開いている下書きも断ります**
// （オートセーブと黙って上書きし合う・機械が既存ページを書き換えるときの関門）。
func DraftSaveAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req DraftRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	p, ok := comm.SendPurposeOf(strings.TrimSpace(req.Purpose))
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "用件「"+req.Purpose+"」はありません")
		return
	}
	source, ok := purposePage(w, user, p, req.PageID)
	if !ok {
		return
	}
	tmpl, err := cms.PageTemplateBody(SentTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "下書きを作るテンプレートがありません: "+err.Error())
		return
	}
	body, err := draftRecordBody(tmpl, SignedInAddress(user.Username), strings.TrimSpace(req.Purpose), source, req.ReplyRequest)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "下書きを組めません: "+err.Error())
		return
	}

	if raw := strings.TrimSpace(req.DraftID); raw != "" {
		id, ok := checkDraftWritable(w, r, user, raw)
		if !ok {
			return
		}
		if err := cms.RewriteBody(id, user.Username, func(string) string { return body }); err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "下書きを保存できません: "+err.Error())
			return
		}
		auth.Audit(user.Username, "mail.draft", id+" "+req.Subject)
		cms.WriteJSON(w, map[string]any{"success": true, "draft_id": id})
		return
	}

	rootID, ok := comm.MailBoxPageID()
	if !ok {
		cms.JSONFail(w, http.StatusConflict, comm.ErrNoMailBox.Error())
		return
	}
	id, err := comm.CreateRecordPage(rootID, user.Username, time.Now(), body)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "下書きを作れません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "mail.draft", id+" "+req.Subject)
	cms.WriteJSON(w, map[string]any{"success": true, "draft_id": id})
}

// checkDraftWritable は下書きのページを書き換えてよいかを確かめます（断るときは応答を書く）。
func checkDraftWritable(w http.ResponseWriter, r *http.Request, user *auth.User, raw string) (string, bool) {
	id, ok := cms.PageIDOrFail(w, raw)
	if !ok {
		return "", false
	}
	if !page.RequirePageWrite(w, r, id) {
		return "", false
	}
	n, _ := strconv.Atoi(id)
	if tags, err := cms.TagsOfPage(database.DB, n); err != nil || len(tags[comm.DraftTag]) == 0 {
		cms.JSONFail(w, http.StatusConflict, "このページは下書きではありません")
		return "", false
	}
	if !editlock.RefuseWhileEditing(w, id) {
		return "", false
	}
	return id, true
}
