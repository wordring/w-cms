package toho

// ─────────────────────────────────────────────────────────────────────────
// 出荷済数量は納品書から数える（2026-10-17）
//
// 利用者（2026-10-16）:「A. 納品書を正本にします。」——受注明細の「出荷済数量」は、人が書かない鏡にします。その行が載った
// 納品書の数を、機械が足して見せます（【考察】全体の設計 §3.6・直す順の8番）。
//
//   - 数えるのは `ページ種類：弊社納品書` のページの納品明細表の行（テンプレートの中は数えない）。**発行済かどうかは見ない**
//     ——品物が先に出たときも、未発行の納品書ページで数える（利用者:「納品書を作る前に、品物だけ先に出ることはありました」）。
//   - 受注明細の行とは **受注ページ（`受注` の列。2026-10-19 から列の名前は `案件`・受注ページは案件ページ）と品番** で結ぶ
//     （`弊社品番` では結ばない——shippedKey）。品番が空の行は**品名**で結ぶ（職場の受注明細に、品番の無い
//     行が21行あった——品番だけで結ぶと、空の行どうしの数が重なる）。同じ受注に同じ品番（品番が空なら同じ品名）の行が2つあると、
//     2行に同じ数が出る（区別できない——そういう受注は人が直す）。
//   - 人が受注明細表の「出荷済数量」に書いた値は**読みません**。2026-10-17 までに人が書いた値は、移行用の仮納品書へ移しました
//     （利用者:「これも納品書ページを作って移行用の仮納品書だと書いておいてはどうでしょう？」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// shippedLookup は描画1回ぶんの「受注ページ×品番 → 納めた数の合計」です。
type shippedLookup map[string]float64

func shippedKey(orderPage, itemNo, itemName string) string {
	order := strings.TrimPrefix(strings.TrimSpace(orderPage), "/")
	if item := strings.TrimSpace(itemNo); item != "" {
		return order + "|" + cms.NormalizeCode(item)
	}
	return order + "|品名:" + cms.NormalizeText(strings.TrimSpace(itemName))
}

// newShippedLookup は、納品書（`ページ種類：弊社納品書`）の納品明細表の行を全部数えます。読めなければ空（0 として数える）。
func newShippedLookup(db cms.ReadOnlyDB) shippedLookup {
	out := shippedLookup{}
	rows, err := cms.VocabRowsOfType(db, DeliveryItemsType)
	if err != nil {
		return out
	}
	kinds := map[int]bool{}
	for _, r := range rows {
		ok, seen := kinds[r.PageID]
		if !seen {
			ok = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindOurDelivery
			kinds[r.PageID] = ok
		}
		if !ok {
			continue
		}
		q, okQ := estimateNumber(r.Values["quantity"])
		if !okQ || q <= 0 {
			continue
		}
		out[shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])] += q
	}
	return out
}

// shippedAmount は、案件の1つの品物の、納品書に載った数と金額です（2026-10-28・原価と利益の売上——case_profit.go）。
type shippedAmount struct {
	Qty     float64 // 納めた数（単価の無い行も）
	Amount  float64 // 単価のある行の 単価 × 数量 の和
	NoPrice int     // 単価の無い行の数
}

// newShippedAmounts は、納品書の納品明細表の行を、案件×品番ごとに数と金額で数えます（newShippedLookup と同じ行を数える）。
func newShippedAmounts(db cms.ReadOnlyDB) map[string]shippedAmount {
	out := map[string]shippedAmount{}
	rows, err := cms.VocabRowsOfType(db, DeliveryItemsType)
	if err != nil {
		return out
	}
	kinds := map[int]bool{}
	for _, r := range rows {
		ok, seen := kinds[r.PageID]
		if !seen {
			ok = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindOurDelivery
			kinds[r.PageID] = ok
		}
		if !ok {
			continue
		}
		q, okQ := estimateNumber(r.Values["quantity"])
		if !okQ || q <= 0 {
			continue
		}
		k := shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])
		a := out[k]
		a.Qty += q
		if p, okP := estimateNumber(r.Values["price"]); okP {
			a.Amount += q * p
		} else {
			a.NoPrice++
		}
		out[k] = a
	}
	return out
}

// of は受注ページ orderPage の品番 itemNo（空なら品名 itemName）の、納めた数の合計です。
// newShippedOutsideMfg は、納品書の行のうち、どの製造ページの分でもないもの（移行用の仮納品書・製造ページを通らずに作った
// 納品書）だけを数えます（2026-10-29・mfgLookup.handled）。製造ページの分は、行の「製造」の列が製造ページを指す行（2026-10-21
// から）と、列が空でも納品書が製造ページの子の行（2026-10-20 までに作った納品書——mfgDeliveryRows と同じ読み方）です。
func newShippedOutsideMfg(db cms.ReadOnlyDB) shippedLookup {
	out := shippedLookup{}
	rows, err := cms.VocabRowsOfType(db, DeliveryItemsType)
	if err != nil {
		return out
	}
	kinds, underMfg := map[int]bool{}, map[int]bool{}
	for _, r := range rows {
		ok, seen := kinds[r.PageID]
		if !seen {
			ok = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindOurDelivery
			kinds[r.PageID] = ok
		}
		if !ok || strings.TrimSpace(r.Values["mfg"]) != "" {
			continue
		}
		under, seenU := underMfg[r.PageID]
		if !seenU {
			if meta, okM := page.ReadSidecar(page.FormatID(r.PageID)); okM {
				if pid, okP := page.NormalizeID(meta.ParentID); okP {
					under = pageKindOf(pageNum(pid)) == PageKindMfg
				}
			}
			underMfg[r.PageID] = under
		}
		if under {
			continue
		}
		if q, okQ := estimateNumber(r.Values["quantity"]); okQ && q > 0 {
			out[shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])] += q
		}
	}
	return out
}

func (s shippedLookup) of(orderPage, itemNo, itemName string) float64 {
	return s[shippedKey(orderPage, itemNo, itemName)]
}

// fmtQty は数を表の文字にします（整数なら小数点を付けない）。
func fmtQty(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// showShippedInOrderTable は受注ページの受注明細表の「出荷済数量」の欄に、納品書から数えた数をクロームで出します（鏡）。
// ⚠ 本文の欄には書きません（保存されない `vocab-chrome` の span）。人が欄に書いた値は使わないので、そう添えます。
func showShippedInOrderTable(ctx *cms.MirrorContext, el *html.Node) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	head := rows[0]
	si, ii, ni := headerIndexOf(head, "出荷済数量"), headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名")
	if si < 0 || ii < 0 {
		return
	}
	look := newShippedLookup(ctx.DB)
	order := page.FormatID(ctx.PageID)
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if si >= len(cells) {
			continue
		}
		name := ""
		if ni >= 0 {
			name = cellText(tr, ni)
		}
		n := look.of(order, cellText(tr, ii), name)
		td := cells[si]
		span := &html.Node{Type: html.ElementNode, Data: "span",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome shipped-mirror"}, {Key: "contenteditable", Val: "false"},
				{Key: "title", Val: "納品書から数えた数（人は書きません）"}}}
		text := fmtQty(n)
		if typed := strings.TrimSpace(textOf(td)); typed != "" {
			text += "（⚠ 欄に書いた " + typed + " は使いません——納品書で数えます）"
		}
		span.AppendChild(&html.Node{Type: html.TextNode, Data: text})
		td.AppendChild(span)
	}
}
