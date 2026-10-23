package toho

// 在庫数の列（2026-10-23・【考察】在庫の記録 §6.3・§6.6 の3番）。
//
// 製造ページの製造明細表（加工製品の在庫）と、発注部材表（部材の在庫）の、数量のすぐ右に「在庫数」の列を出します。
// 置き場「在庫」の在庫表から引く鏡で、本文には書きません。在庫を使うかどうかは、この列を見て人が決めます
// （機械は自動では当てない）。在庫表に行が無ければ空欄です。
//
// 利用者（2026-10-23）:「製造明細表や発注部材表の数量の横に在庫数の列を作って様子を見てみましょう」

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// stockLevels は在庫表の行を、鍵（stockRowKey）→ 在庫数で読みます（見る人が読める在庫表だけ）。
func stockLevels(db cms.ReadOnlyDB, viewer *auth.User) map[string]float64 {
	out := map[string]float64{}
	for _, k := range stockKinds {
		rows, err := cms.VocabRowsOfType(db, k.Type)
		if err != nil {
			continue
		}
		def, _ := cms.VocabDefByType(k.Type)
		for _, r := range rows {
			if viewer == nil || !page.CanView(viewer, r.PageID) {
				continue
			}
			vals := map[string]string{}
			for _, c := range def.Columns {
				vals[c.Label] = labelValue(def, r, c.Label)
			}
			key := stockRowKey(k, vals)
			if key == "" {
				continue
			}
			if n, ok := estimateNumber(vals[StockQtyLabel]); ok {
				out[key] += n
			}
		}
	}
	return out
}

// orderKindOfVals は部材の行（列の名前 → 値）の種類を返します（種類の列、無ければ鍵の列が埋まる最初の種類）。
func orderKindOfVals(vals map[string]string) (orderKind, bool) {
	if k, ok := orderKindByName(vals["種類"]); ok {
		return k, true
	}
	for _, k := range OrderKinds() {
		if k.keyOf(vals) != "" {
			return k, true
		}
	}
	return orderKind{}, false
}

// partStockKey は発注部材表の1行（列の名前 → 値）の、在庫表の鍵です。
func partStockKey(vals map[string]string) string {
	ok, found := orderKindOfVals(vals)
	if !found {
		return ""
	}
	sk, found := stockKindOf(ok.Kind)
	if !found {
		return ""
	}
	return stockRowKey(sk, vals)
}

// productStockKey は製造明細表の1行の、加工製品在庫表の鍵です。
func productStockKey(vals map[string]string) string {
	for _, k := range stockKinds {
		if k.Type == StockProductType {
			return stockRowKey(k, vals)
		}
	}
	return ""
}

// insertStockColumn は表 table の「数量」の列のすぐ右に、在庫数の鏡の列を足します（数量の列が無ければ足さない）。
// keyOf は行（列の名前 → 値）から在庫表の鍵を作ります。列を足すのは、ほかの鏡が列を足す前（見出しの位置で読むため）。
func insertStockColumn(table *html.Node, levels map[string]float64, keyOf func(map[string]string) string) {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return
	}
	labels := cellTexts(rows[0])
	qi := headerIndexOf(rows[0], "数量")
	if qi < 0 {
		return
	}
	cell := func(tr *html.Node, tag, text string) {
		cells := cellsOf(tr)
		td := &html.Node{Type: html.ElementNode, Data: tag,
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome w-no-print stock-qty"}, {Key: "contenteditable", Val: "false"},
				{Key: "title", Val: "置き場「在庫」の在庫表の数です（ここでは直せません）"}}}
		if text != "" {
			td.AppendChild(&html.Node{Type: html.TextNode, Data: text})
		}
		if qi < len(cells) {
			tr.InsertBefore(td, cells[qi].NextSibling)
		} else {
			tr.AppendChild(td)
		}
	}
	cell(rows[0], "th", StockQtyLabel)
	for _, tr := range rows[1:] {
		texts := cellTexts(tr)
		vals := map[string]string{}
		for i, l := range labels {
			if i < len(texts) {
				vals[l] = texts[i]
			}
		}
		text := ""
		if key := keyOf(vals); key != "" {
			if n, ok := levels[key]; ok && n > 0 {
				text = formatStockQty(n)
			}
		}
		cell(tr, "td", text)
	}
}

// trimSlash は参照の値の頭の「/」を落とします（`/001234` と `001234` を同じに読む）。
func trimSlash(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "/") }
