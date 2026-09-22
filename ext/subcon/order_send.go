package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注書を送る——メール・FAX・手渡し（2026-09-22）
//
// ユーザー:「発注書ページで**送信方法を選んで**、メールの場合は**メールを書くボックス**
// が出て送信します。FAXの場合は、FAXのページを揃えて送信します。（**FAXサーバーは
// まだつくっていないので、人間が送ったボタンを押すかもしれません**）」。
//
//	発注書ページ
//	  ├ 発注明細（本文の表）
//	  │    ├ 行ごとに [取消] [発注済]                 ← 印は行に付く（order_status.go）
//	  │    └ 足元に  [📧 メール] [📠 FAX] [🤝 手渡し]  ← ここ
//	  └ 添付: 発注書 20260922-201530.pdf
//
// ⚠ **送信の口は作りません。** メールは `POST /api/mail/send` が**もう在ります**
// （添付つき・通信箱に控えが残る）。ここが持つのは**下ごしらえ**（宛先の候補・件名・
// 署名入りの本文）と、**出たあとに印を付ける口**だけです。
//
// ⚠ **FAXとメールで「送った」の意味が違います。** メールは**送信の成功が事実**なので
// 自動で印が付きます。FAX・手渡しは**人が押したことが事実**です——FAXサーバーが
// 出来たら、そちらの成功に付け替えます（画面は変わりません）。
//
// ⚠ **宛先は候補まで。** `仕入先` の題で連絡帳を引きますが、**当たらないことが
// 正常**です（新しい仕入先の1通目）。そのときは黙らずにそう言って、人が打ちます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 送り方の名前です（監査と画面で共有）。
const (
	sendByMail = "メール"
	sendByFax  = "FAX"
	sendByHand = "手渡し"
)

// OrderSentAPIHandler は POST /api/our-order/sent です。
//
// 入力: {page_id, method}——**取消でない行すべて**に `発注済` の印を付けます。
//
// ⚠ **これは「送った」の記録ではなく、「送った結果」の反映です。** メールの控えは
// 通信箱に残り（`/api/mail/send` が作る）、FAX・手渡しは**紙そのもの**（PDFの添付）が
// 証拠です。ここで二重に持つと、**どちらが本当か**という問いが生まれます。
func OrderSentAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Method string `json:"method"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := cms.PageIDOrFail(w, req.PageID)
	if !okID {
		return
	}
	method := strings.TrimSpace(req.Method)
	switch method {
	case sendByMail, sendByFax, sendByHand:
	default:
		cms.JSONFail(w, http.StatusBadRequest,
			"送り方は "+sendByMail+"・"+sendByFax+"・"+sendByHand+" のどれかです")
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	changed := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, n := setOrderLineStatus(cur, 0, OrderLineSent)
		changed = n
		return out
	}) {
		return
	}
	auth.Audit(user.Username, "our-order.sent", pageID+" "+method+" "+strconv.Itoa(changed)+"行")
	// ⚠ **0行でも失敗にしません。** もう全部 `発注済` だった（2通目を送った・
	//    FAXのあとにメールもした）は**普通に起こります**——そこで赤いエラーを出すと、
	//    人は「送れていない」と読みます。
	cms.WriteJSON(w, map[string]any{
		"success": true, "page_id": pageID, "method": method, "rows": changed})
}

// supplierAddresses は仕入先の題から、メールアドレスの候補を集めます。
//
// ⚠ **連絡帳の木だけを見ます**（`連絡帳／組織／人`）。組織ページ自身のアドレスと、
// その下の人のアドレスを並べます——**窓口が誰かは機械には決められません**。
//
// ⚠ **当たらないことが正常です。** 新しい仕入先の1通目は連絡帳に居ません。
func supplierAddresses(user *auth.User, supplier string) []string {
	orgID, ok := contacts.PartnerByTitle(user, supplier)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		for _, v := range mailAddressesOfPage(pageNum(id)) {
			addr := bareMailAddress(v)
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, addr)
		}
	}
	add(orgID)
	for _, p := range contacts.PersonsOf(user, orgID) {
		add(p.ID)
	}
	sort.Strings(out)
	return out
}

// mailAddressesOfPage はページの `メールアドレス` タグを全部返します。
//
// ⚠ **1ページに何個でも置けます**（連絡帳の決まり——窓口・担当・共有アドレス）。
func mailAddressesOfPage(pageID int) []string {
	tags, err := cms.TagsOfPage(database.DB, pageID)
	if err != nil {
		return nil
	}
	return tags[contacts.EmailTag]
}

// bareMailAddress は `名前 <アドレス>` からアドレスだけを取り出します。
//
// ⚠ **SMTP の `RCPT TO:` は素のアドレスしか受けません**（返信の宛先と同じ都合・
// `ext/comm/mail/reply.go` の `bareAddress`）。
func bareMailAddress(s string) string {
	v := strings.TrimSpace(s)
	if i := strings.LastIndex(v, "<"); i >= 0 {
		if j := strings.Index(v[i:], ">"); j > 0 {
			v = v[i+1 : i+j]
		}
	}
	return strings.TrimSpace(v)
}

// orderMailSubject は発注書メールの件名です。
//
// ⚠ **発注書番号を入れます**——相手が問い合わせてきたとき、**件名だけで紙を
// 特定できます**。
func orderMailSubject(orderID, supplier string) string {
	s := "発注書の送付（" + orderID + "）"
	if supplier != "" {
		s = supplier + "　御中　" + s
	}
	return s
}

// orderMailBody はメールの下書きです。
//
// ⚠ **署名は「メールの署名」から引きます**（`発注書の署名` ではありません）。
// 紙の右上に刷る署名と、メールの末尾に置く署名は**同じ人でも別の文面**です。
//
// ⚠ **引けなくても黙って空にしません**——署名の無いメールが出てしまうので、
// どこに書けばよいかを本文の中に残します（送る前に人が消せます）。
func orderMailBody(head map[string]string, supplier, orderID string) string {
	var b strings.Builder
	if supplier != "" {
		b.WriteString(supplier + "　御中\n\n")
	}
	b.WriteString("いつもお世話になっております。\n")
	b.WriteString("発注書（" + orderID + "）をお送りいたします。\n")
	b.WriteString("添付のPDFをご確認ください。\n\n")
	b.WriteString("よろしくお願いいたします。\n\n")

	lines := signerLinesFor(head, MailSignatureHeading)
	if len(lines) == 0 {
		b.WriteString("（⚠ 署名がありません。連絡帳の担当者ページに「" +
			MailSignatureHeading + "」の見出しで署名を書くと、ここに入ります）\n")
	} else {
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	return b.String()
}

// signerLinesFor は発注書ページのタグ（`発注担当`）から、その人の署名を読みます。
//
// ⚠ **読めるかは見ません**——メールの下書きは、書いている人自身がその署名を
// 選んだ発注書ページで組むものです。紙に刷る側（`senderLines`）は見ます。
func signerLinesFor(head map[string]string, heading string) []string {
	n, ok := signerPage(head)
	if !ok {
		return nil
	}
	return SignatureOf(n, heading)
}

// orderSendFormHTML は発注明細の足元に出す「送る」欄です（鏡）。
//
// ⚠ **3つの道を最初から見せます。** 隠すと、FAXしか使わない相手のときに
// 「この画面では送れない」と読まれます。
func orderSendFormHTML(user *auth.User, pageIDInt int, head map[string]string,
	counts OrderSendCounts) string {
	orderID := page.FormatID(pageIDInt)
	supplier := strings.TrimSpace(head[SupplierTag])
	addrs := supplierAddresses(user, supplier)

	var b strings.Builder
	b.WriteString(`<div class="order-send" data-order-page="` + orderID + `">`)
	b.WriteString(`<p class="order-send-state">` + orderSendStateHTML(counts) + `</p>`)
	b.WriteString(`<div class="order-send-ways">`)
	// ⚠ **PDFを作る道が画面にありませんでした**（2026-09-22 ユーザー報告:「発注書の
	//    ページにPDFが表示されていません」）。口（`/api/order-pdf`）は 09-21 から
	//    在りましたが、**呼ぶボタンがどこにも無く**、API を直に叩いて確かめただけ
	//    でした。⚠ **試した経路と、人が使う経路が違っていた**わけです。
	b.WriteString(`<button type="button" class="chip-btn" data-order-pdf="1"` +
		` title="発注書のPDFを作って、このページに表示します（添付にも残ります）">` +
		`📄 PDFを作る</button>`)
	b.WriteString(`<button type="button" class="chip-btn" data-order-sent="` + sendByFax +
		`" title="FAXサーバーはまだありません。送ったら押してください">📠 FAXで送った</button>`)
	b.WriteString(`<button type="button" class="chip-btn" data-order-sent="` + sendByHand +
		`" title="印刷して手渡し・郵送したら押してください">🤝 手渡した</button>`)
	b.WriteString(`</div>`)

	// ── メールの箱（はじめは畳んでおく） ──────────────────────────────
	//
	// ⚠ **`<details>` で畳みます**——素のHTMLだけで開閉できるので、CSP strict の
	//    下でも動きます（受注ページの原本と同じ手）。
	b.WriteString(`<details class="order-mail"><summary>📧 メールで送る</summary>`)
	b.WriteString(`<label class="matsearch-field"><span>宛先</span>` +
		`<input type="text" class="matsearch-input" data-order="to" value="` +
		stdhtml.EscapeString(strings.Join(addrs, ", ")) +
		`" placeholder="eigyo@example.co.jp"/></label>`)
	if len(addrs) == 0 {
		// ⚠ **引けなかったことを黙りません**——空欄だと「連絡帳に居ないから出せない」
		//    のか「引く仕掛けが壊れている」のかが分かりません。
		b.WriteString(`<p class="unorder-help">⚠ 「` + stdhtml.EscapeString(supplier) +
			`」の連絡先が連絡帳にありません（題が一致する組織ページに「` +
			contacts.EmailTag + `」のタグを付けると、ここに出ます）。</p>`)
	}
	b.WriteString(`<label class="matsearch-field"><span>件名</span>` +
		`<input type="text" class="matsearch-input" data-order="subject" value="` +
		stdhtml.EscapeString(orderMailSubject(orderID, supplier)) + `"/></label>`)
	b.WriteString(`<label class="matsearch-field order-mail-body"><span>本文</span>` +
		`<textarea class="matsearch-input" data-order="body" rows="10">` +
		stdhtml.EscapeString(orderMailBody(head, supplier, orderID)) + `</textarea></label>`)
	// ⚠ **PDFは押したときに作ります**（下書きを開いただけで添付を増やさない）。
	b.WriteString(`<p class="unorder-help">⚠ 送るときに<strong>発注書のPDFを作って添付</strong>します` +
		`（このページの添付にも残ります）。</p>`)
	b.WriteString(`<button type="button" class="matsearch-go" data-order-send="1">送信</button>`)
	b.WriteString(`</details>`)
	b.WriteString(`<div class="unorder-result" data-order-result="1"></div>`)
	b.WriteString(`</div>`)
	return b.String()
}

// orderSendStateHTML は「いまどこまで出ているか」の一文です。
//
// ⚠ **合っているときも黙りません**——黙ると「出した」と「まだ数えていない」が
// 見分けられません（検算と同じ規律）。
func orderSendStateHTML(c OrderSendCounts) string {
	switch {
	case c.Total == 0 && c.Cancelled > 0:
		return `<strong>全部取り消しました</strong>（` + strconv.Itoa(c.Cancelled) + `行）`
	case c.Total == 0:
		return `明細がありません`
	case c.Sent == 0:
		return `⚠ <strong>まだ発注していません</strong>（` + strconv.Itoa(c.Total) + `行）`
	case c.Sent < c.Total:
		return `⚠ <strong>` + strconv.Itoa(c.Total-c.Sent) + `行がまだ発注済みになっていません</strong>（` +
			strconv.Itoa(c.Sent) + `/` + strconv.Itoa(c.Total) + `行）`
	default:
		return `✓ 発注済み（` + strconv.Itoa(c.Total) + `行）` + cancelledNote(c)
	}
}

// cancelledNote は取り消した行があればそう言います。
func cancelledNote(c OrderSendCounts) string {
	if c.Cancelled == 0 {
		return ""
	}
	return `　<span class="matsearch-src">取消 ` + strconv.Itoa(c.Cancelled) + `行</span>`
}
