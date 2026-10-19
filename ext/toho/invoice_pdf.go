package toho

// ─────────────────────────────────────────────────────────────────────────
// 請求書の紙（PDF）と、請求明細表の足元（2026-10-17）
//
// 紙の形は、ウェブの見本と国税庁のインボイス Q&A から起こした（【考察】請求書 §5）。上から——
// 題「請　求　書」・請求日（締め日）・№・宛名「客先　御中」・差出人（請求担当の「部材発注書の署名」）と登録番号・
// 「下記のとおりご請求申し上げます。」・請求期間・ご請求金額（税込）・支払条件・明細（No.・納品日・品番・品名・数量・単位・単価・金額）・
// 小計（税率ごと）・消費税・合計・お振込先。刷る道具は部材発注書・納品書の紙と共有。
//
//   - **登録番号**は、請求担当の人が属する連絡帳の組織ページ（自社）のタグ `登録番号`。書いてなければ刷らない。
//     2026-10-17 利用者:「前に見たことがあるのでしてると思います。」
//   - **お振込先**は、同じ組織ページの節「請求書の振込先」（自由文）。書いてなければ刷らない（手形の客先が多い）。
//     2026-10-17 利用者:「お勧め通りで、手形の場合が多いです」
//     ⚠ 口座番号は公開リポジトリ（config/settings.json）には書かない——連絡帳（data/）に置く。
//   - **消費税は1枚に1回だけ丸める**（明細の行ごとに丸めて足さない——インボイスの決まり・国税庁 Q&A 問57）。
//   - PDF を作ると `発行日` に今日を書く（空のときだけ）——空なら「未発行」、入っていれば「発行済」。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

func init() {
	cms.RegisterMirror(InvoiceItemsType, cms.MirrorHandlerFunc(renderInvoiceItems))
}

// readInvoiceDoc は請求書ページの本文から、タグと明細（見出しの名前で）を読みます。
func readInvoiceDoc(body string) (head map[string]string, rows []map[string]string, err error) {
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return nil, nil, perr
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	head = map[string]string{}
	for _, k := range []string{InvoiceNoTag, DeliveryClientTag, InvoiceDateTag, InvoicePeriodTag, PayTermsTag, DeliveryIssuedTag, InvoiceSignerTag} {
		head[k] = strings.TrimSpace(cms.TagValue(root, k))
	}
	tables := tablesOfType([]*html.Node{root}, InvoiceItemsType)
	if len(tables) == 0 {
		return nil, nil, errors.New("請求明細表がありません")
	}
	return head, itemRowsOf(tables[0]), nil
}

// invoiceTotals は小計・消費税・合計です（消費税は1枚に1回だけ丸める）。単価の無い行があれば ok=false。
func invoiceTotals(rows []map[string]string) (sub, tax int, label string, ok bool) {
	sub, ok, _ = deliveryTotals(rows)
	label = "消費税"
	if rate, has := TaxRate(); has {
		tax = int(float64(sub)*rate/100 + 0.5)
		label = "消費税（" + rateText(rate) + "%）"
	}
	return sub, tax, label, ok
}

// ownCompanyOf は請求担当の人が属する連絡帳の組織ページ（自社）から、登録番号と振込先の行を読みます（読めなければ空）。
func ownCompanyOf(head map[string]string, viewer *auth.User) (regNo string, bank []string) {
	id, ok := page.NormalizeID(strings.TrimSpace(head[InvoiceSignerTag]))
	if !ok {
		return "", nil
	}
	org, _, ok := contacts.PartnerOfPage(pageNum(id))
	if !ok || (viewer != nil && !page.CanView(viewer, org)) {
		return "", nil
	}
	if tags, err := cms.TagsOfPage(database.DB, org); err == nil {
		regNo = strings.TrimSpace(cms.FirstTag(tags, RegistrationNoTag))
	}
	return regNo, contacts.SignatureOf(org, BankHeading)
}

// shortDate は YYYY-MM-DD を「10/17」にします（明細の納品日。読めなければそのまま）。
func shortDate(s string) string {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return s
	}
	return strconv.Itoa(int(t.Month())) + "/" + strconv.Itoa(t.Day())
}

// buildInvoicePDF は請求書ページの本文から PDF を組みます。
func buildInvoicePDF(body string, viewer *auth.User) ([]byte, error) {
	font, err := paperFont()
	if err != nil {
		return nil, err
	}
	head, rows, err := readInvoiceDoc(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("請求明細表に行がありません")
	}
	sub, tax, taxLabel, okTotal := invoiceTotals(rows)
	if !okTotal {
		return nil, errors.New("単価の決まっていない行があります（請求書には、単価の決まった行だけを載せます）")
	}
	p, err := startPaper(font)
	if err != nil {
		return nil, err
	}
	date := head[InvoiceDateTag]
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	pdfText(p, pdfLeft+190, pdfTop, 18, "請　求　書")
	pdfTextRight(p, pdfRight, pdfTop, pdfFontSz, jpDate(date))
	pdfTextRight(p, pdfRight, pdfTop+pdfLine, pdfFontSz, "№ "+head[InvoiceNoTag])
	y := pdfTop + 34
	y = pdfText(p, pdfLeft, y, 13, strings.TrimSpace(head[DeliveryClientTag])+"　御中")
	p.SetLineWidth(0.6)
	p.Line(pdfLeft, y-2, pdfLeft+260, y-2)
	regNo, bank := ownCompanyOf(head, viewer)
	sender := senderLines(map[string]string{OrderSignerTag: head[InvoiceSignerTag]}, viewer)
	if regNo != "" {
		sender = append(sender, "登録番号： "+regNo)
	}
	y = pdfSender(p, pdfTop+34, y, sender)
	y += 6
	y = pdfText(p, pdfLeft, y, pdfFontSz, "下記のとおりご請求申し上げます。")
	if pr := head[InvoicePeriodTag]; pr != "" {
		y = pdfText(p, pdfLeft, y, pdfFontSz, "請求期間： "+pr)
	}
	y += 4
	y = pdfText(p, pdfLeft, y, 14, "ご請求金額（税込）　"+comma(sub+tax)+" 円")
	p.SetLineWidth(0.8)
	p.Line(pdfLeft, y-2, pdfLeft+300, y-2)
	if pt := head[PayTermsTag]; pt != "" {
		y = pdfText(p, pdfLeft, y+2, pdfFontSz, "お支払条件： "+pt)
	}
	y += 10
	nos := map[string]bool{}
	for _, r := range rows {
		if n := r[OrderNoTag]; n != "" {
			nos[n] = true
		}
	}
	cols := []orderPDFColumn{{Label: "No."}, {Label: "納品日"}}
	if len(nos) > 1 {
		cols = append(cols, orderPDFColumn{Label: "注文番号"})
	}
	cols = append(cols, orderPDFColumn{Label: "品番"}, orderPDFColumn{Label: "品名"},
		orderPDFColumn{Label: "数量", Right: true}, orderPDFColumn{Label: "単位"},
		orderPDFColumn{Label: "単価", Right: true}, orderPDFColumn{Label: "金額", Right: true})
	cells := make([]map[string]string, 0, len(rows))
	for i, r := range rows {
		c := map[string]string{"No.": strconv.Itoa(i + 1), "納品日": shortDate(r[DeliveredAtTag]), "注文番号": r[OrderNoTag],
			"品番": r[ItemNoTag], "品名": r["品名"], "数量": r["数量"], "単位": r["単位"], "単価": r["単価"]}
		if a, ok := estimateAmount(r); ok {
			c["金額"] = comma(a)
		}
		cells = append(cells, c)
	}
	y = pdfTable(p, y, cols, cells)
	y += 6
	rate := ""
	if r, ok := TaxRate(); ok {
		rate = "（" + rateText(r) + "%対象）"
	}
	for _, ln := range [][2]string{{"小計" + rate, comma(sub)}, {taxLabel, comma(tax)}, {"合計", comma(sub + tax)}} {
		pdfTextRight(p, pdfRight, y, 11, ln[0]+"　"+ln[1]+" 円")
		y += pdfLine
	}
	if len(bank) > 0 {
		y += 6
		pdfNote(p, y, "お振込先", bank)
	}
	return finishPaper(p)
}

// makeInvoicePDF は請求書の PDF を作って添付に残し、ページに表示し、`発行日` に今日を書きます（空のときだけ）。
func makeInvoicePDF(w http.ResponseWriter, user *auth.User, pageID string) (madeOrderPDF, bool) {
	made, ok := makePaper(w, user, pageID, paperKind{
		build: buildInvoicePDF,
		name:  func(_, pageID string) string { return "請求書 " + pageID },
		audit: "invoice-pdf",
		show:  showBelowItems(InvoiceItemsType, "請求明細表"),
	})
	if !ok {
		return made, false
	}
	today := time.Now().Format("2006-01-02")
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		if head, _, err := readInvoiceDoc(cur); err == nil && head[DeliveryIssuedTag] != "" {
			return cur
		}
		return withTagValues(cur, DeliveryIssuedTag, []string{today}, false)
	}); err != nil {
		made.ViewNote = strings.TrimSpace(made.ViewNote + "（発行日を書けませんでした: " + err.Error() + "）")
	}
	// 済んだ製造ページを記録へ（2026-10-19・mfg_close.go——請求書の発行のとき）。移したかは請求明細表の足元に出る。
	if body, err := cms.ReadPageBody(pageID); err == nil {
		closeFinishedMfgPages(user, body, time.Now())
	}
	return made, true
}

// InvoicePDFAPIHandler は POST /api/invoice-pdf です（入力: {page_id}）。
func InvoicePDFAPIHandler(w http.ResponseWriter, r *http.Request) {
	servePaper(w, r, makeInvoicePDF)
}

// renderInvoiceItems は請求明細表の足元に、状態（未発行・発行済）・小計・消費税・合計・PDF を作る を出します（鏡）。
func renderInvoiceItems(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	span := headerCellCount(el)
	rows := itemRowsOf(el)
	issued := strings.TrimSpace(cms.TagValue(rootOf(el), DeliveryIssuedTag))
	state := "未発行（紙をまだ作っていません）"
	if issued != "" {
		state = "発行済（" + issued + "）"
	}
	appendFootRow(el, span, "delivery-state", "状態: "+state)
	if sub, tax, label, ok := invoiceTotals(rows); ok {
		appendFootHTML(el, span, "estimate-total", `小計（税抜）: `+comma(sub)+`円　`+stdhtml.EscapeString(label)+`: `+comma(tax)+
			`円　<strong>合計: `+comma(sub+tax)+`円</strong>`)
	} else if len(rows) > 0 {
		appendFootRow(el, span, "estimate-note", "⚠ 単価の決まっていない行があります（紙を作れません——単価を決めるか、行を外してください）")
	}
	// この請求書の製造ページ（2026-10-19・mfg_close.go）——記録へ移したか、まだ製造にあるか。
	for _, id := range mfgPagesOfInvoice(rows) {
		link := `<a href="/` + page.FormatID(id) + `">` + stdhtml.EscapeString(cms.PageTitleByID(id)) + `</a>`
		if mfgIsOpen(id) {
			appendFootHTML(el, span, "estimate-note", `製造 `+link+` はまだ「`+MfgBoxTitle+`」にあります（納め終えていない行か、請求書に載っていない納品書があります）`)
		} else {
			appendFootHTML(el, span, "estimate-note", `製造 `+link+` は記録へ移しました`)
		}
	}
	if ctx.Viewer == nil || !canWritePage(ctx.Viewer, ctx.PageID) || cms.IsTemplateArea(page.FormatID(ctx.PageID)) {
		return false, nil
	}
	pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
	appendFootHTML(el, span, "delivery-actions",
		`<button type="button" class="chip-btn invoice-pdf-go" data-invoice-page="`+pid+`">📄 PDFを作る</button> `+
			`<span class="invoice-pdf-say"></span>`)
	return false, nil
}
