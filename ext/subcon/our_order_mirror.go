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
// ⚠ **出すのは「次の一手」と「取消」だけ**です。4つの値を全部ボタンにすると、
// **紙の上の状態と関係なく押せてしまいます**——`未発注` の行に「納品済」が並ぶのは、
// 手が滑るのを待っているのと同じです。
//
// ⚠ **取消はどの段からも押せます**（2026-09-22 ユーザー:「**発注した後でも取り消す
// 場合があり得ます**…**発注後に材料屋から取り扱いが無いと連絡が来てキャンセルに
// なることもあります**」）。⚠ **納品済からも**——届いたあとに返すこともあるので、
// 「2手かければ戻せる」では足りません。
func addOrderRowButtons(table *html.Node, orderID string) {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return
	}
	si := headerIndexOf(rows[0], "状態")
	addRowChromeCells(table, "order-row-act", func(row int, tr *html.Node) string {
		status := ""
		if si >= 0 {
			status = strings.TrimSpace(cellText(tr, si))
		}
		return orderRowButtonsHTML(orderID, row, status)
	})
}

// orderRowButtonsHTML は1行ぶんのボタンです。
//
// ⚠ **`取消` からは「取消をやめる」（→ 未発注）だけ**です。取り消した行をいきなり
// `納品済` にできると、**相手に取り消しを頼んだ品が「届いた」ことになります**。
//
// ⚠ **取消の重さが2通りあります**（2026-09-22 ユーザー）。
//
//	未発注 … 「**発注前なら単純に取り消します**」          → 確かめずに取り消す
//	発注済 … 「電話などで材料屋に取り消しを依頼して、**OKが出たら**取り消しボタンを
//	          押します」／「材料屋から取り扱いが無いと連絡が来て」 → **相手の了解が前提**
//	納品済 … 現物が届いている                              → **返す話**
//
// ⚠ **画面はその違いを出します**（`data-order-confirm`）——**紙が外へ出たあとの取消は、
// 押しただけでは終わりません**。確かめずに押せる形にすると、「押したから片付いた」と
// 読まれ、**材料屋には注文が残ったまま**になります。
func orderRowButtonsHTML(orderID string, row int, status string) string {
	btn := func(value, label, title, confirm string) string {
		s := `<button type="button" class="chip-btn order-row-set"` +
			` data-order-page="` + stdhtml.EscapeString(orderID) + `"` +
			` data-order-row="` + strconv.Itoa(row) + `"` +
			` data-order-status="` + stdhtml.EscapeString(value) + `"`
		if confirm != "" {
			s += ` data-order-confirm="` + stdhtml.EscapeString(confirm) + `"`
		}
		return s + ` title="` + stdhtml.EscapeString(title) + `">` + label + `</button>`
	}
	// ⚠ **取り消しても必要部材表へは戻りません**（2026-09-23 に意味が変わった・
	//    order_status.go）。**確認の文にそう書くこと**——09-22 まで「戻ります」だったので、
	//    書かないと人は「どこかに戻った」と思い、**誰も買わないまま納期が来ます**。
	const backNote = "取り消すと、この部材はもう発注しません（必要部材表へは戻りません）。\n" +
		"もう一度発注したいときは「必要部材表へ戻す」を押してください。"
	return orderRowStatusButtonsHTML(btn, status, backNote) + orderRowReturnButtonHTML(orderID, row, status)
}

// orderRowReturnButtonHTML は行末の「必要部材表へ戻す」ボタンです（2026-09-24）。
//
// ⚠ **どの段からも押せます**（ユーザー:「発注書の送付後でも押せます」）。
// ⚠ **いつも確かめます**——押すと**行が発注書から消える**ので、押し間違いが
// 本文から消えます。送付後は**相手に出した紙に載っている行**なので、そう書きます。
// ⚠ **編集モードでは出しません**（`view-only`・ユーザー:「このボタンは編集モードでは
// 消えます」）——編集中の本文から行を消すと、書きかけと食い違います。
func orderRowReturnButtonHTML(orderID string, row int, status string) string {
	ask := "この行を発注書から外して、必要部材表へ戻します。"
	switch strings.TrimSpace(status) {
	case OrderLineSent, OrderLineDelivered, orderLineLegacySent:
		ask = "⚠ この行は既に相手に送った発注書に載っています。\n" +
			"外したら、訂正した発注書を再送してください。\n\n" + ask
	}
	return `<button type="button" class="chip-btn order-row-return view-only"` +
		` data-order-page="` + stdhtml.EscapeString(orderID) + `"` +
		` data-order-row="` + strconv.Itoa(row) + `"` +
		` data-order-confirm="` + stdhtml.EscapeString(ask) + `"` +
		` title="この行を発注書から外し、必要部材表でまた選べるようにします">↩ 必要部材表へ戻す</button>`
}

// orderRowStatusButtonsHTML は `状態` を変えるボタンです（上の説明のとおり）。
func orderRowStatusButtonsHTML(btn func(value, label, title, confirm string) string,
	status, backNote string) string {
	switch strings.TrimSpace(status) {
	case OrderLineCancelled:
		return btn(OrderLineUnsent, "↩ 取消をやめる", "取り消しをやめて「未発注」に戻します", "")
	case OrderLineSent, orderLineLegacySent:
		return btn(OrderLineDelivered, "📦 納品済", "この品が届きました", "") +
			btn(OrderLineCancelled, "✕ 取消", "発注済みの行を取り消します（相手の了解が要ります）",
				"⚠ この行は既に発注済みです。材料屋の了解は取れていますか？\n\n"+backNote)
	case OrderLineDelivered:
		return btn(OrderLineSent, "↩ 戻す", "納品済を取り消して「発注済」に戻します", "") +
			btn(OrderLineCancelled, "✕ 取消", "納品済の行を取り消します（返す話になります）",
				"⚠ この行は納品済です。現物を返す話になりますが、取り消しますか？\n\n"+backNote)
	default: // 未発注・空欄
		// ⚠ **ここだけ確認を出しません**（ユーザー:「発注前なら**単純に**取り消します」）。
		return btn(OrderLineSent, "✓ 発注済", "この行だけ発注済みにします", "") +
			btn(OrderLineCancelled, "✕ 取消", "この部材はもう発注しません（必要部材表へは戻りません）", "")
	}
}
