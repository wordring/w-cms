package toho

// ─────────────────────────────────────────────────────────────────────────
// 「納品書発行」「請求書発行」の印を、納品書と請求書から数える（2026-10-19）
//
//	2026-10-19 利用者:「おすすめの順（削除と保存の行き違い → 印の自動化 → 外へ出す前の安全）で進めてください」
//	（筆者の案:「納品書と請求書がもう正本なので、そこから数えた鏡にできます（出荷済数量と同じ形）」）
//
//   - 納品書発行: その案件の行（案件ページ×品番・品番が空なら品名）を載せた、**発行済**（`発行日` のある）納品書の数の合計。
//   - 請求書発行: その案件の行を載せた、**発行済**の請求書（`ページ種類：弊社請求書`）の請求明細の数の合計。
//   - 見せ方は製造完了と同じ——全部の数なら「済」、一部なら「8/20」、無ければ空（madeText）。
//   - 案件残表と、案件ページの案件明細表に出す（鏡）。人は書かない。案件明細表の欄に人が書いた値は使わない（⚠ で添える）。
//     2026-10-19 に職場で数えたとき、人が書いた値は1つも無かった（339行とも空）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// issuedQtyLookup は、ページ種類 kind の発行済（`発行日` のある）ページの表 itemsType の行の数を、案件ページ×品番で足します。
func issuedQtyLookup(db cms.ReadOnlyDB, itemsType, kind string) shippedLookup {
	out := shippedLookup{}
	rows, err := cms.VocabRowsOfType(db, itemsType)
	if err != nil {
		return out
	}
	issued := map[int]bool{}
	for _, r := range rows {
		ok, seen := issued[r.PageID]
		if !seen {
			if !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == kind {
				tags, _ := cms.TagsOfPage(db, r.PageID)
				ok = strings.TrimSpace(cms.FirstTag(tags, DeliveryIssuedTag)) != ""
			}
			issued[r.PageID] = ok
		}
		if !ok {
			continue
		}
		if q, okQ := estimateNumber(r.Values["quantity"]); okQ && q > 0 {
			out[shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])] += q
		}
	}
	return out
}

// newIssuedDeliveryLookup は発行済の納品書の数です（納品書発行の印）。
func newIssuedDeliveryLookup(db cms.ReadOnlyDB) shippedLookup {
	return issuedQtyLookup(db, DeliveryItemsType, PageKindOurDelivery)
}

// newIssuedInvoiceLookup は発行済の請求書の数です（請求書発行の印）。
func newIssuedInvoiceLookup(db cms.ReadOnlyDB) shippedLookup {
	return issuedQtyLookup(db, InvoiceItemsType, PageKindOurInvoice)
}

// showCountInOrderTable は案件明細表の列 label の欄に、of で数えた印（済・N/M）をクロームで出します（鏡）。
// ⚠ 本文の欄には書きません。人が欄に書いた値は使わないので、そう添えます（where は「どこで付けるか」の言葉）。
func showCountInOrderTable(ctx *cms.MirrorContext, el *html.Node, label, title, where string, of func(order, item, name string) float64) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	head := rows[0]
	mi, ii, ni, qi := headerIndexOf(head, label), headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名"), headerIndexOf(head, "数量")
	if mi < 0 || qi < 0 {
		return
	}
	order := page.FormatID(ctx.PageID)
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if mi >= len(cells) {
			continue
		}
		item, name := "", ""
		if ii >= 0 {
			item = cellText(tr, ii)
		}
		if ni >= 0 {
			name = cellText(tr, ni)
		}
		qty, _ := estimateNumber(cellText(tr, qi))
		text := madeText(of(order, item, name), int(qty))
		if typed := strings.TrimSpace(textOf(cells[mi])); typed != "" {
			text += "（⚠ 欄に書いた " + typed + " は使いません——" + where + "）"
		}
		if text == "" {
			continue
		}
		cells[mi].AppendChild(&html.Node{Type: html.ElementNode, Data: "span",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome made-mirror"}, {Key: "contenteditable", Val: "false"},
				{Key: "title", Val: title}},
			FirstChild: &html.Node{Type: html.TextNode, Data: text}})
	}
}

// showIssuedInOrderTable は案件明細表の「納品書発行」「請求書発行」に、発行済の納品書・請求書から数えた印を出します（鏡）。
func showIssuedInOrderTable(ctx *cms.MirrorContext, el *html.Node) {
	dl, inv := newIssuedDeliveryLookup(ctx.DB), newIssuedInvoiceLookup(ctx.DB)
	showCountInOrderTable(ctx, el, "納品書発行", "発行済の納品書から数えた（人は書きません）", "納品書の紙を作ると付きます", dl.of)
	showCountInOrderTable(ctx, el, "請求書発行", "発行済の請求書から数えた（人は書きません）", "請求書の紙を作ると付きます", inv.of)
}
