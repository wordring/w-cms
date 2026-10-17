package toho

// ─────────────────────────────────────────────────────────────────────────
// 請求書——締め日までの納品書の行をまとめて、客先へ出す請求書を作る（2026-10-17）
//
// 決めたことは【考察】請求書（§4 の答え・§6 の案・§7 の答え）。
//
//	2026-10-17 利用者:「1．締め日でまとめる 2．会社ごとに違う 3．集金は板金部でやっていないので、今回の市絵の請求のみでよいです。
//	5．決まるまで請求書に入れません。」
//	2026-10-17 利用者（§7）:「1．前に見たことがあるのでしてると思います。2．お勧め通りで、手形の場合が多いです 3．会社ごとに違う 4．連絡帳」
//
//   - 1枚に1社。締め日までの**発行済の納品書**の行を集める（未発行の納品書——移行用の仮納品書も——は載せない）。
//     前回の請求額・入金・繰越は載せない（今回の締めの分だけ）。
//   - 締め日と支払条件は、連絡帳の客先の組織ページのタグ（`締め日`・`支払条件`）。締め日が書いてなければ末日。
//   - 単価の決まっていない行は載せない（決まったら、次の請求書に入る）。
//   - 一度請求書に載せた行は、次の請求書の候補から外す（二重に請求しない）。結びは納品書ページと品番（品番が空なら品名）。
//   - 置き場は `記録／年／月／製造記録`（作った日）。題は「【弊社】請求書　<客先>」。請求書番号はページ番号。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// InvoiceTemplate は請求書ページのテンプレートの題です。
	InvoiceTemplate = "請求書"
	// InvoiceItemsType は請求明細表の形式です。
	InvoiceItemsType = "invoice-items"
	// InvoiceViewType は「請求書を作る」の欄の形式です。
	InvoiceViewType = "invoice-make"
	// 請求書のタグ。
	InvoiceNoTag       = "請求書番号"
	InvoiceDateTag     = "請求日" // 締め日
	InvoicePeriodTag   = "請求期間"
	InvoiceSignerTag   = "請求担当"
	PayTermsTag        = "支払条件" // 客先の組織ページにも書く（会社ごとに違う）
	PageKindOurInvoice = "弊社請求書"
	// 連絡帳の組織ページに書くもの。
	ClosingDayTag     = "締め日"   // 客先の組織ページ。「末日」か日にち（例: 20）
	RegistrationNoTag = "登録番号" // 自社の組織ページ。インボイスの登録番号（T＋13桁）
	BankHeading       = "請求書の振込先"
)

func invoiceItemColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "delivered-at", Label: DeliveredAtTag, Type: cms.ColDate},
		{Field: "delivery", Label: "納品書", Type: cms.ColRef},
		{Field: "order", Label: "受注", Type: cms.ColRef},
		{Field: "order-no", Label: OrderNoTag, Type: cms.ColCode},
		{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
		{Field: "item-id", Label: ItemNoTag, Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		{Field: "price", Label: "単価", Type: cms.ColNumber},
		{Field: "note", Label: "備考", Type: cms.ColText},
	}
}

func init() {
	cms.RegisterVocab(cms.VocabDef{
		// **請求明細表**——請求書ページの明細。機械が納品書の行から作るので、スラッシュメニューには出さない。
		Type: InvoiceItemsType, DisplayName: "請求明細表", Category: "業務", Icon: "🧾", Element: "table", Hidden: true,
		Columns: invoiceItemColumns(),
	})
	cms.RegisterVocab(cms.VocabDef{
		Type: InvoiceViewType, DisplayName: "請求書を作る", Category: "ビュー", Icon: "🧾", Element: "section", View: true,
	})
	cms.RegisterView(InvoiceViewType, invoiceViewHTML)
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     InvoiceTemplate,
		Extension: "toho",
		Why: "「請求書を作る」が、請求書ページ（記録／年／月／製造記録）を作るときに写します。" +
			"キャプション「請求明細表」の表が要ります（タグ 請求書番号・客先・請求日・請求期間・支払条件・発行日・請求担当・ページ種類〔弊社請求書〕は、あれば埋めます）。",
	})
}

// ── 締め日 ──────────────────────────────────────────────────────────────

// closingDayOf は締め日の書き方を読みます。「末日」「月末」・空は 0（月の末日）、「20」「20日」は 20。読めなければ ok=false。
func closingDayOf(v string) (int, bool) {
	v = strings.TrimSpace(cms.NormalizeText(v))
	switch v {
	case "", "末日", "月末", "末":
		return 0, true
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(v, "締め"), "日"))
	if err != nil || n < 1 || n > 31 {
		return 0, false
	}
	if n >= 28 {
		return 0, true // 28日より後は、短い月で末日と同じになるので末日として扱う
	}
	return n, true
}

// closingIn は y年m月の締め日です（day 0 は末日）。
func closingIn(y int, m time.Month, day int) time.Time {
	if day == 0 {
		return time.Date(y, m+1, 0, 0, 0, 0, 0, time.Local)
	}
	return time.Date(y, m, day, 0, 0, 0, 0, time.Local)
}

// latestClosing は today 以前でいちばん新しい締め日です（今日が締め日なら今日）。
func latestClosing(today time.Time, day int) time.Time {
	c := closingIn(today.Year(), today.Month(), day)
	if c.After(today) {
		c = closingIn(today.Year(), today.Month()-1, day)
	}
	return c
}

// previousClosing は締め日 c の1つ前の締め日です。
func previousClosing(c time.Time, day int) time.Time {
	return closingIn(c.Year(), c.Month()-1, day)
}

// closingLabel は締め日を画面に出す言葉です。
func closingLabel(day int) string {
	if day == 0 {
		return "末日"
	}
	return strconv.Itoa(day) + "日"
}

// ── 客先の組織ページ（連絡帳） ────────────────────────────────────────────

// clientTerms は客先の締め日と支払条件です（連絡帳の組織ページのタグ）。
type clientTerms struct {
	OrgPage    string // 連絡帳の組織ページ（無ければ空）
	ClosingDay int
	ClosingSet bool   // 締め日が書いてある
	ClosingBad string // 読めなかった書き方（読めなければ末日として扱う）
	PayTerms   string
}

func clientTermsOf(user *auth.User, client string) clientTerms {
	var t clientTerms
	id, ok := contacts.PartnerByTitle(user, client)
	if !ok {
		return t
	}
	t.OrgPage = id
	tags, err := cms.TagsOfPage(database.DB, pageNum(id))
	if err != nil {
		return t
	}
	if raw := strings.TrimSpace(cms.FirstTag(tags, ClosingDayTag)); raw != "" {
		if d, ok := closingDayOf(raw); ok {
			t.ClosingDay, t.ClosingSet = d, true
		} else {
			t.ClosingBad = raw
		}
	}
	t.PayTerms = strings.TrimSpace(cms.FirstTag(tags, PayTermsTag))
	return t
}

// ── 請求書にしていない納品書の行 ───────────────────────────────────────────

// invoiceLine は請求明細表の1行（＝発行済の納品書の1行）です。
type invoiceLine struct {
	Delivery, Date, Order, OrderNo, OurItemNo, ItemNo, ItemName, Qty, Unit, Price, Note string
	Client                                                                             string
	RowNo                                                                              int
}

// invoiceKey は納品書の行と請求明細の行を結ぶ鍵です（納品書ページと品番。品番が空なら品名）。
func invoiceKey(delivery, itemNo, itemName string) string {
	return shippedKey(delivery, itemNo, itemName)
}

// billedKeys は、請求書（`ページ種類：弊社請求書`）に載った納品書の行の鍵です。
func billedKeys() map[string]bool {
	out := map[string]bool{}
	rows, err := cms.VocabRowsOfType(database.DB, InvoiceItemsType)
	if err != nil {
		return out
	}
	kinds := map[int]bool{}
	for _, r := range rows {
		ok, seen := kinds[r.PageID]
		if !seen {
			ok = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindOurInvoice
			kinds[r.PageID] = ok
		}
		if ok {
			out[invoiceKey(r.Values["delivery"], r.Values["item-id"], r.Values["item-name"])] = true
		}
	}
	return out
}

// unbilledLines は、user が読める発行済の納品書の行のうち、まだ請求書に載っていないものです（客先ごと・日付の順）。
// 単価の決まっていない行は lines に入れず、noPrice に数えます。
func unbilledLines(user *auth.User) (lines map[string][]invoiceLine, noPrice map[string]int) {
	lines, noPrice = map[string][]invoiceLine{}, map[string]int{}
	rows, err := cms.VocabRowsOfType(database.DB, DeliveryItemsType)
	if err != nil {
		return
	}
	billed := billedKeys()
	type head struct {
		ok           bool
		client, date string
	}
	heads := map[int]head{}
	for _, r := range rows {
		h, seen := heads[r.PageID]
		if !seen {
			if !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindOurDelivery && page.CanView(user, r.PageID) {
				if tags, err := cms.TagsOfPage(database.DB, r.PageID); err == nil {
					issued := strings.TrimSpace(cms.FirstTag(tags, DeliveryIssuedTag))
					date := strings.TrimSpace(cms.FirstTag(tags, DeliveredAtTag))
					if date == "" {
						date = issued
					}
					if d, ok := cms.NormalizeValue(cms.ColDate, date); ok {
						date = d
					}
					h = head{ok: issued != "", client: strings.TrimSpace(cms.FirstTag(tags, DeliveryClientTag)), date: date}
				}
			}
			heads[r.PageID] = h
		}
		if !h.ok || h.client == "" {
			continue
		}
		dlv := page.FormatID(r.PageID)
		v := r.Values
		if billed[invoiceKey(dlv, v["item-id"], v["item-name"])] {
			continue
		}
		if q, ok := estimateNumber(v["quantity"]); !ok || q <= 0 {
			continue
		}
		if _, ok := estimateNumber(v["price"]); !ok {
			noPrice[h.client]++
			continue
		}
		lines[h.client] = append(lines[h.client], invoiceLine{
			Delivery: dlv, Date: h.date, Order: strings.TrimPrefix(strings.TrimSpace(v["order"]), "/"), OrderNo: strings.TrimSpace(v["order-no"]),
			OurItemNo: strings.TrimSpace(v["our-item-id"]), ItemNo: strings.TrimSpace(v["item-id"]), ItemName: strings.TrimSpace(v["item-name"]),
			Qty: strings.TrimSpace(v["quantity"]), Unit: strings.TrimSpace(v["unit"]), Price: strings.TrimSpace(v["price"]),
			Note: strings.TrimSpace(v["note"]), Client: h.client, RowNo: r.RowNo,
		})
	}
	for c := range lines {
		ls := lines[c]
		sort.SliceStable(ls, func(i, j int) bool {
			if ls[i].Date != ls[j].Date {
				return ls[i].Date < ls[j].Date
			}
			if ls[i].Delivery != ls[j].Delivery {
				return ls[i].Delivery < ls[j].Delivery
			}
			return ls[i].RowNo < ls[j].RowNo
		})
	}
	return
}

// linesUntil は、締め日 close（YYYY-MM-DD）までの行と、それより後の行の数です。
func linesUntil(ls []invoiceLine, close string) (in []invoiceLine, later int) {
	for _, l := range ls {
		if l.Date != "" && l.Date > close {
			later++
			continue
		}
		in = append(in, l)
	}
	return in, later
}

// linesAmount は行の金額（数量 × 単価）の和です。
func linesAmount(ls []invoiceLine) int {
	sum := 0
	for _, l := range ls {
		if a, ok := estimateAmount(map[string]string{"数量": l.Qty, "単価": l.Price}); ok {
			sum += a
		}
	}
	return sum
}

// ── 「請求書を作る」の欄 ────────────────────────────────────────────────

func invoiceViewHTML(user *auth.User, pageIDInt int) string {
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">🧾 請求書を作る（締め日でまとめる）</h3>`)
	b.WriteString(`<p class="unorder-help">発行済の納品書の行のうち、まだ請求書にしていないものを、客先ごとに数えます。` +
		`締め日（連絡帳の客先のページのタグ「締め日」。書いていなければ末日）までの行を1枚にまとめます。` +
		`単価の決まっていない行は載せません。決まったら、次の請求書に入ります。</p>`)
	lines, noPrice := unbilledLines(user)
	var clients []string
	seen := map[string]bool{}
	for c := range lines {
		clients, seen[c] = append(clients, c), true
	}
	for c := range noPrice {
		if !seen[c] {
			clients = append(clients, c)
		}
	}
	sort.Strings(clients)
	if len(clients) == 0 {
		b.WriteString(`<p class="unorder-help">請求書にしていない納品書の行はありません（数えるのは、紙を作った＝発行済の納品書です）。</p>`)
		return b.String()
	}
	today := time.Now()
	b.WriteString(`<table class="backlog-table invoice-make-table"><tbody><tr><th>客先</th><th>締め日</th><th>締め</th>` +
		`<th>載せる行</th><th>金額（税抜）</th><th>単価がまだ</th><th>支払条件</th><th></th></tr>`)
	for _, c := range clients {
		t := clientTermsOf(user, c)
		close := latestClosing(today, t.ClosingDay).Format("2006-01-02")
		in, later := linesUntil(lines[c], close)
		closeNote := closingLabel(t.ClosingDay)
		switch {
		case t.ClosingBad != "":
			closeNote += `（⚠ 「` + stdhtml.EscapeString(t.ClosingBad) + `」を読めないので末日）`
		case !t.ClosingSet:
			closeNote += `（連絡帳に無いので末日）`
		}
		count := strconv.Itoa(len(in)) + "行"
		if later > 0 {
			count += `<br><span class="unorder-help">締めより後 ` + strconv.Itoa(later) + `行</span>`
		}
		np := ""
		if noPrice[c] > 0 {
			np = strconv.Itoa(noPrice[c]) + "行"
		}
		b.WriteString(`<tr class="invoice-make-row" data-client="` + stdhtml.EscapeString(c) + `">` +
			`<td class="cell-atomic">` + stdhtml.EscapeString(c) + `</td>` +
			`<td class="cell-atomic">` + closeNote + `</td>` +
			`<td class="cell-atomic"><input type="date" class="invoice-close" value="` + close + `" aria-label="締め"></td>` +
			`<td class="cell-atomic">` + count + `</td>` +
			`<td class="cell-atomic">` + comma(linesAmount(in)) + `円</td>` +
			`<td class="cell-atomic backlog-gap">` + np + `</td>` +
			`<td class="cell-wrap">` + stdhtml.EscapeString(t.PayTerms) + `</td>` +
			`<td class="cell-atomic"><button type="button" class="chip-btn invoice-make">🧾 請求書を作る</button> <span class="invoice-make-say"></span></td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// ── 請求書ページを作る ──────────────────────────────────────────────────

// buildInvoiceHTML は請求書ページの本文を、テンプレート tmpl を埋めて組みます。
func buildInvoiceHTML(tmpl, pageID, client, close, period, payTerms, signer string, lines []invoiceLine) (string, error) {
	d := cms.NewPageDraft(InvoiceTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindOurInvoice, "請求書　"+client))
	setHeaderTag(d.DraftBlock, InvoiceNoTag, pageID)
	setHeaderTag(d.DraftBlock, DeliveryClientTag, client)
	setHeaderTag(d.DraftBlock, InvoiceDateTag, close)
	setHeaderTag(d.DraftBlock, InvoicePeriodTag, period)
	if payTerms != "" {
		setHeaderTag(d.DraftBlock, PayTermsTag, payTerms)
	} else {
		d.DraftBlock.EnsureTag(PayTermsTag)
	}
	if signer != "" {
		setHeaderTag(d.DraftBlock, InvoiceSignerTag, signer)
	} else {
		d.DraftBlock.EnsureTag(InvoiceSignerTag)
	}
	d.DraftBlock.EnsureTag(DeliveryIssuedTag) // 空＝未発行
	d.SetTag(PageKindTag, PageKindOurInvoice)
	rows := make([]map[string]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, map[string]string{
			"delivered-at": l.Date, "delivery": l.Delivery, "order": l.Order, "order-no": l.OrderNo, "our-item-id": l.OurItemNo,
			"item-id": l.ItemNo, "item-name": l.ItemName, "quantity": l.Qty, "unit": l.Unit, "price": l.Price, "note": l.Note,
		})
	}
	if _, err := fillVocabTable(d.DraftBlock, InvoiceItemsType, rows); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// jpDay は日付を「2026年10月1日」の形にします。
func jpDay(t time.Time) string {
	return strconv.Itoa(t.Year()) + "年" + strconv.Itoa(int(t.Month())) + "月" + strconv.Itoa(t.Day()) + "日"
}

// InvoiceNewAPIHandler は POST /api/invoice/new です。入力: {client, close}（close は締め日 YYYY-MM-DD）。請求書ページを作り、そのIDを返します。
func InvoiceNewAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Client string `json:"client"`
		Close  string `json:"close"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	client := strings.TrimSpace(req.Client)
	closeT, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(req.Close), time.Local)
	if client == "" || err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "客先と締め日（年-月-日）を選んでください")
		return
	}
	close := closeT.Format("2006-01-02")
	all, noPrice := unbilledLines(user)
	var mine []invoiceLine
	for c, ls := range all {
		if cms.NormalizeText(c) == cms.NormalizeText(client) {
			mine = ls
			client = c
		}
	}
	lines, later := linesUntil(mine, close)
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "「"+client+"」の、"+close+" までの請求書にしていない行がありません（発行済の納品書の、単価の決まった行を数えます）")
		return
	}
	terms := clientTermsOf(user, client)
	period := jpDay(previousClosing(closeT, terms.ClosingDay).AddDate(0, 0, 1)) + "〜" + jpDay(closeT)
	signer := ""
	if s, ok := DefaultSigner(user, Signers(user)); ok {
		signer = page.FormatID(s.PageID)
	}
	tmpl, err := cms.PageTemplateBody(InvoiceTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "請求書ページを作れません: "+err.Error())
		return
	}
	if _, err := buildInvoiceHTML(tmpl, "", client, close, period, terms.PayTerms, signer, lines); err != nil {
		cms.JSONFail(w, http.StatusConflict, "請求書ページを作れません: "+err.Error())
		return
	}
	now := time.Now()
	monthID, err := recordFolder(user.Username, RecordWorkFolder, now)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+recordPath(RecordWorkFolder, now)+"」を用意できません: "+err.Error())
		return
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "請求書ページを作れません: "+err.Error())
		return
	}
	body, err := buildInvoiceHTML(tmpl, newID, client, close, period, terms.PayTerms, signer, lines)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "請求書ページを組めません: "+err.Error())
		return
	}
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return
	}
	auth.Audit(user.Username, "invoice.new", newID+" "+client+" "+close+" "+strconv.Itoa(len(lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "url": "/" + newID,
		"rows": len(lines), "later": later, "no_price": noPrice[client]})
}
