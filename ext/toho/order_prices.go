package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品ページの「受注の単価」（2026-10-04）
//
// 利用者:「発注書から最新価格が分かるという目論見もあります」→ 問いへの答え「客先の注文の単価」→ 筆者の勧め（加工製品ページに
// 「受注の単価」を並べる）に「次に進みましょう」。客先の注文（受注明細）の単価は**弊社の販売価格**で、加工製品ごとに
// 「最後にいくらで受けたか」を引きたい。業者の値段の「💴 見積回答」（rfq_quotes.go）と同じ形の鏡で、支給部品の表の下に並べる。
//
//   - 並べるのは、受注明細の行のうち **弊社品番がこのページ**の行と、**弊社品番は空でも、発注元がこのページの客先で品番が合う**
//     行（受注の行を加工製品に結ぶときと同じ照合——productPagesForCustomer。品名でも絞る）。別の加工製品に結ばれた行は並べない。
//     品番で当てた行には印（※）——人が結んだのではないことが分かるように。
//   - 読める受注ページだけ。新しい順（発注日）。数量・単価・状態（納品済・完了など）も出す。鏡なので本文には書かない。
//   - 置き場所は見積計算表の下（無ければ支給部品の下）——下の insertOrderPrices の前の注。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// orderPriceRow は「受注の単価」の1行です。
type orderPriceRow struct {
	OrderPage int
	Date      string // 発注日
	No        string // 発注書番号
	Code      string // 品番（客先の言葉）
	Qty       string
	Unit      string
	Price     string
	Status    string
	ByCode    bool // 弊社品番が空で、客先と品番で当てた
}

// orderPricesForProduct は、このページの受注明細の行を新しい順に返します（読める受注ページだけ）。
func orderPricesForProduct(db cms.ReadOnlyDB, user *auth.User, productID int) []orderPriceRow {
	rows, err := cms.VocabRowsOfType(db, clientOrderItemsType)
	if err != nil {
		return nil
	}
	want := page.FormatID(productID)
	// このページの番号（品番・図面番号など）を畳んだ形——品番で当てる行の先絞り（照合そのものは productPagesForCustomer）。
	codes := map[string]bool{}
	if tags, err := cms.TagsOfPage(db, productID); err == nil {
		for _, name := range ProductCodeTags() {
			for _, v := range tags[name] {
				if k := cms.NormalizeCode(v); k != "" {
					codes[k] = true
				}
			}
		}
	}
	canView := viewCheck(user)
	heads := map[int]map[string][]string{}
	var out []orderPriceRow
	for _, r := range rows {
		v := r.Values
		linked := strings.TrimSpace(v["our-item-id"])
		byCode := false
		switch {
		case linked == want:
		case linked != "":
			continue // 別の加工製品に結ばれた行
		default:
			if !codes[cms.NormalizeCode(v["item-id"])] {
				continue
			}
			byCode = true
		}
		if !canView(r.PageID) || cms.IsTemplateArea(page.FormatID(r.PageID)) {
			continue
		}
		h, ok := heads[r.PageID]
		if !ok {
			h, _ = cms.TagsOfPage(db, r.PageID)
			heads[r.PageID] = h
		}
		if byCode && !containsID(productPagesForCustomer(db, cms.FirstTag(h, OrderClientTag), v["item-id"], v["item-name"]), productID) {
			continue
		}
		out = append(out, orderPriceRow{OrderPage: r.PageID, Date: strings.TrimSpace(cms.FirstTag(h, OrderedAtTag)),
			No: strings.TrimSpace(cms.FirstTag(h, OrderNoTag)), Code: strings.TrimSpace(v["item-id"]),
			Qty: strings.TrimSpace(v["quantity"]), Unit: strings.TrimSpace(v["unit"]), Price: strings.TrimSpace(v["price"]),
			Status: strings.TrimSpace(v["status"]), ByCode: byCode})
	}
	key := func(d string) string {
		if n, ok := cms.NormalizeValue(cms.ColDate, d); ok {
			return n
		}
		return ""
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := key(out[i].Date), key(out[j].Date); a != b {
			return a > b
		}
		return out[i].No > out[j].No
	})
	return out
}

func containsID(ids []int, id int) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// orderPricesHTML は「受注の単価」を描きます（行が無ければ短い断り）。
func orderPricesHTML(list []orderPriceRow) string {
	var b strings.Builder
	b.WriteString(`<div class="vocab-chrome rfq-quotes order-prices" contenteditable="false"><p class="materials-title">🧾 受注の単価</p>`)
	if len(list) == 0 {
		b.WriteString(`<p class="materials-empty">まだありません（受注明細の弊社品番がこのページの行と、客先と品番が合う行がここに並びます）。</p></div>`)
		return b.String()
	}
	b.WriteString(`<div class="rfq-quotes-scroll"><table class="materials-table rfq-quotes-table"><thead><tr><th>発注日</th><th>発注書番号</th>` +
		`<th>品番</th><th class="num">数量</th><th class="num">単価</th><th>状態</th></tr></thead><tbody>`)
	marked := false
	for _, q := range list {
		id := page.FormatID(q.OrderPage)
		label := q.Date
		if label == "" {
			label = "/" + id
		}
		qty := q.Qty
		if qty != "" && q.Unit != "" {
			qty += q.Unit
		}
		price := q.Price
		if price != "" && !strings.HasSuffix(price, "円") {
			price += "円"
		}
		code := stdhtml.EscapeString(q.Code)
		if q.ByCode {
			code += "※"
			marked = true
		}
		status := q.Status
		if status == "" {
			status = "—"
		}
		b.WriteString(`<tr><td><a href="/` + id + `">` + stdhtml.EscapeString(label) + `</a></td><td>` + stdhtml.EscapeString(q.No) +
			`</td><td>` + code + `</td><td class="num">` + stdhtml.EscapeString(qty) + `</td><td class="num">` + stdhtml.EscapeString(price) +
			`</td><td>` + stdhtml.EscapeString(status) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	if marked {
		b.WriteString(`<p class="materials-empty">※ 受注明細の弊社品番は空で、客先と品番で当てた行（受注フォルダの受注残表を開くと、受注残の行は結ばれます）。</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// 置き場所（2026-10-04 利用者:「受注の単価は見積もり計算表の下が良いかもしれませんね」）——売る値段なので、確定単価の出る
// 見積計算表のそばに。見積計算表があれば**最後の1枚の直後**、無ければ支給部品の表の下（見積回答の後ろ）。
//
// ⚠ 本文の上の段の節どうしは、鏡からは互いに見えない（ParseFragment の上の段は親も兄弟も持たない）——支給部品の表を描くときに、
// 後ろに見積計算表があるかは分からない。だから見積計算表の枚数を**本文の正本ファイル**で数えて分ける（estimateTablesIn）。
// 版の表示など、描いている本文と正本の枚数が違うときは、置かれないか支給部品の下になるだけ（1ページに1回は Counter で守る）。

var estimateCaptionRe = regexp.MustCompile(`<caption>\s*見積計算表\s*</caption>`)

// estimateTablesIn は、このページの見積計算表の枚数です（本文の正本ファイルのキャプションで数える）。
func estimateTablesIn(pageID int) int {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return 0
	}
	return len(estimateCaptionRe.FindAllStringIndex(body, -1))
}

// appendOrderPrices は支給部品の表から呼ばれます——見積計算表が無いページだけ、ここ（見積回答の後ろ）に出す。
func appendOrderPrices(ctx *cms.MirrorContext, tbl *html.Node) {
	if estimateTablesIn(ctx.PageID) > 0 {
		return // 見積計算表の下に出す（placeOrderPricesAfterEstimate）
	}
	insertOrderPrices(ctx, tbl, true)
}

// placeOrderPricesAfterEstimate は見積計算表の表から呼ばれます——最後の1枚の直後に出す。
func placeOrderPricesAfterEstimate(ctx *cms.MirrorContext, tbl *html.Node) {
	if seen := ctx.Counter("order-prices-estimate"); seen+1 < estimateTablesIn(ctx.PageID) {
		return // まだ後ろに見積計算表がある
	}
	insertOrderPrices(ctx, tbl, false)
}

// insertOrderPrices は「受注の単価」を描きます（1ページに1回）。sectionEnd なら表を包む節の末尾、そうでなければ表の直後
// （後ろに続く鏡は飛ばす）——⚠ 表の中へ div を入れない（HTMLパーサが表の外へ追い出す）。
func insertOrderPrices(ctx *cms.MirrorContext, tbl *html.Node, sectionEnd bool) {
	if ctx.Counter("order-prices") > 0 || cms.IsTemplateArea(page.FormatID(ctx.PageID)) {
		return
	}
	frag := orderPricesHTML(orderPricesForProduct(ctx.DB, ctx.Viewer, ctx.PageID))
	if p := tbl.Parent; sectionEnd && p != nil && p.Data == "section" {
		appendHTML(p, frag)
		return
	}
	if tbl.Parent == nil {
		return
	}
	at := tbl.NextSibling
	for at != nil && at.Type == html.ElementNode && hasClassName(at, "vocab-chrome") {
		at = at.NextSibling
	}
	nodes, err := htmldoc.ParseFragment(frag)
	if err != nil {
		return
	}
	for _, n := range nodes {
		tbl.Parent.InsertBefore(n, at)
	}
}

func hasClassName(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == name {
					return true
				}
			}
		}
	}
	return false
}
