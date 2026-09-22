package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注明細の鏡——行ごとの印と、足元の「送る」欄（2026-09-22）
//
// ⚠ **本文には何も書きません。** 足すのは**押すためのもの**だけで、印そのものは
// 本文の `状態` の列にあります（`order_status.go`）。**鏡に印を持たせると、紙と
// 画面で違うことが書いてある**状態が作れてしまいます。
//
// ⚠ **既にある発注書ページにも出ます。** 器（`<section>`）を本文に置く形ではなく、
// **発注明細の表そのものを引き金**にしているためです——09-21 までに作った紙にも、
// 今日から送信の欄が付きます。
//
// ⚠ **閲覧モードから押せることが肝**です。紙を出したあとに印を付けるのに、
// いちいち編集モードへ入るのは道が遠すぎます（発注部材表の「戻す」と同じ判断）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

func init() {
	cms.RegisterMirror(ourOrderItemsType, cms.MirrorHandlerFunc(renderOurOrderChrome))
}

// renderOurOrderChrome は発注明細に、行ごとの印と足元の送信欄を足します。
//
// ⚠ **2枚目以降の発注明細には送信欄を出しません。** 発注書は**1枚に1社**なので、
// 1ページに明細が2つ並ぶのは**人が手で足したとき**だけです——そこに送信の
// ボタンを2組出すと、**どちらを押したのか**が誰にも分からなくなります。
func renderOurOrderChrome(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	first := ctx.Counter(ourOrderItemsType) == 0

	orderID := page.FormatID(ctx.PageID)
	addOrderRowButtons(el, orderID)

	if !first {
		return false, nil
	}
	span := headerCellCount(el)
	if span <= 0 {
		span = len(columnsOf(ourOrderItemsType))
	}
	head := orderHeadTags(ctx)
	counts := OrderSendStateOf(ctx.DB, ctx.PageID)
	appendFootHTML(el, span, "order-send-row",
		orderSendFormHTML(ctx.Viewer, ctx.PageID, head, counts))
	return false, nil
}

// orderHeadTags は発注書ページのタグを名前→値で読みます（仕入先・発注担当）。
//
// ⚠ **索引から読みます**（本文ではなく）。鏡は表の要素しか渡されないので、
// ページ全体のタグは自分では見えません。
func orderHeadTags(ctx *cms.MirrorContext) map[string]string {
	out := map[string]string{}
	tags, err := cms.TagsOfPage(ctx.DB, ctx.PageID)
	if err != nil {
		return out
	}
	for k, v := range tags {
		// ⚠ **同じ名前のタグは何個でも置けます**（`TagsOfPage` は値の並びを返す）。
		//    ここで見たいのは `仕入先`・`発注担当` のように**1つしかないもの**なので、
		//    先頭を採ります。
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// addOrderRowButtons は各行の末尾に、印を変えるボタンを足します。
//
// ⚠ **出すのは「次の一手」だけ**です。4つの値を全部ボタンにすると、**紙の上の
// 状態と関係なく押せてしまいます**——`未発注` の行に「納品済」が並ぶのは、
// 手が滑るのを待っているのと同じです。
func addOrderRowButtons(table *html.Node, orderID string) {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return
	}
	si := headerIndexOf(rows[0], "状態")
	for i, tr := range rows {
		cell := &html.Node{Type: html.ElementNode, Data: "td",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome order-row-act"}}}
		if i == 0 {
			// 見出し行。⚠ **足さないと列がずれて見えます**。
			cell.Data = "th"
		} else {
			status := ""
			if si >= 0 {
				status = strings.TrimSpace(cellText(tr, si))
			}
			if inner := orderRowButtonsHTML(orderID, i, status); inner != "" {
				if nodes, err := htmldoc.ParseFragment(inner); err == nil {
					for _, n := range nodes {
						cell.AppendChild(n)
					}
				}
			}
		}
		tr.AppendChild(cell)
	}
}

// orderRowButtonsHTML は1行ぶんのボタンです。
//
// ⚠ **`取消` からは「戻す」だけ**です。取り消した行をいきなり `納品済` にできると、
// **取り消したはずのものが手配済みに戻り**、未手配の一覧から静かに消えます。
func orderRowButtonsHTML(orderID string, row int, status string) string {
	btn := func(value, label, title string) string {
		return `<button type="button" class="chip-btn order-row-set"` +
			` data-order-page="` + stdhtml.EscapeString(orderID) + `"` +
			` data-order-row="` + strconv.Itoa(row) + `"` +
			` data-order-status="` + stdhtml.EscapeString(value) + `"` +
			` title="` + stdhtml.EscapeString(title) + `">` + label + `</button>`
	}
	switch strings.TrimSpace(status) {
	case OrderLineCancelled:
		return btn(OrderLineUnsent, "↩ 戻す", "取り消しをやめて「未発注」に戻します")
	case OrderLineSent, orderLineLegacySent:
		return btn(OrderLineDelivered, "📦 納品済", "この品が届きました") +
			btn(OrderLineCancelled, "✕ 取消", "この行の発注を取り消します")
	case OrderLineDelivered:
		return btn(OrderLineSent, "↩ 戻す", "納品済を取り消して「発注済」に戻します")
	default: // 未発注・空欄
		return btn(OrderLineSent, "✓ 発注済", "この行だけ発注済みにします") +
			btn(OrderLineCancelled, "✕ 取消", "この行は発注しません")
	}
}
