package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材発注明細表の鏡——行ごとの印と、足元の「送る」欄（2026-09-22）
//
// ⚠ **本文には何も書きません。** 足すのは**押すためのもの**だけで、印そのものは
// 本文の `状態` の列にあります（`order_status.go`）。**鏡に印を持たせると、紙と
// 画面で違うことが書いてある**状態が作れてしまいます。
//
// ⚠ **既にある部材発注書ページにも出ます。** 器（`<section>`）を本文に置く形ではなく、
// **部材発注明細表そのものを引き金**にしているためです——09-21 までに作った紙にも、
// 今日から送信の欄が付きます。
//
// ⚠ **閲覧モードから押せることが肝**です。紙を出したあとに印を付けるのに、
// いちいち編集モードへ入るのは道が遠すぎます（発注部材表の「戻す」と同じ判断）。
// （2026-10-12 に編集モードをやめ、パソコンは書ける形で開きます。業務のボタンは書ける形でも押せます。
// 行末の「必要部材表へ戻す」も、2026-10-19 から書ける形でも出します——下の `orderRowReturnButtonHTML`。）
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

// renderOurOrderChrome は部材発注明細表に、行ごとの印と足元の送信欄を足します。
//
// ⚠ **2枚目以降の部材発注明細表には送信欄を出しません。** 部材発注書は**1枚に1社**なので、
// 1ページに明細が2つ並ぶのは**人が手で足したとき**だけです——そこに送信の
// ボタンを2組出すと、**どちらを押したのか**が誰にも分からなくなります。
func renderOurOrderChrome(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	first := ctx.Counter(ourOrderItemsType) == 0

	orderID := page.FormatID(ctx.PageID)
	// ⚠ 2026-10-21 夕から、表の中にボタンを置かない（利用者:「部材発注書の部材発注明細表も、行の先頭のチェックと表の下のボタンにしてください。」）。
	//    行の先頭にチェック（その行に効く操作と、押す前に確かめる文を持たせる——orderRowActions）、操作と送る欄は表の下。
	// 外注加工の資料（2026-09-28・order_docs.go）——取り消していない行の「資料 <番号>」から（読むのはチェックを足す前）。
	// ⚠ ここでは「FAX・印刷用（資料を綴じる）」を出すかだけに使う（メールの添付の候補は送る欄の初期値）。
	var docs []orderDoc
	if first {
		rows, _ := orderTableRows(el)
		docs, _ = orderDocs(ctx.Viewer, rows)
	}
	bar := ""
	// 行の操作は1つ目の表だけ——サーバーの行の操作（状態・外す）は行の番号だけを受け取り、1つ目の表に効く。2つ目の表に
	// チェックを出すと、選んだのと違う表の行が変わった（2026-10-22 の見直し）。
	if writableMirror(ctx) && first {
		addOrderRowPicks(el)
		bar = orderRowBarHTML(orderID)
	}
	if first {
		bar += orderSendFormHTML(ctx.PageID, OrderSendStateOf(ctx.DB, ctx.PageID), docs, orderNoteBoxFor(ctx),
			sendWayFor(ctx.Viewer, ctx.PageID, orderDocName, SupplierTag, "")) // 仕入先の連絡帳の「送り方」の勧め（2026-10-26）
	}
	if bar != "" {
		appendBarAfter(el, "order-bar", bar)
	}
	return false, nil
}

// orderNoteBoxFor は備考の入力欄です（2026-10-14・見積書・部材見積依頼書と同じ部品 noteBoxHTML）。書ける人にだけ出し、
// テンプレートの中では出しません。保存先は本文の見出し「備考」の節で、紙はそこを刷ります（readOrderDoc）。
func orderNoteBoxFor(ctx *cms.MirrorContext) string {
	pid := page.FormatID(ctx.PageID)
	if !writableMirror(ctx) {
		return ""
	}
	note := ""
	if body, err := cms.ReadPageBody(pid); err == nil {
		note = orderNoteOf(body)
	}
	return noteBoxHTML(pid, note, "/api/our-order/note", "備考（相手に伝えること——紙では明細の下に刷ります）")
}

// 行の操作の名前です（行の先頭のチェックの data-can に、効くものを空白で並べる）。
const (
	orderActSent      = "sent"     // ✓ 発注済
	orderActDelivered = "delivered" // 📦 納品済
	orderActUnsent    = "unsent"   // ↩ 未発注に戻す（取消をやめる）
	orderActCancel    = "cancel"   // ✕ 取消
	orderActReturn    = "return"   // ↩ 発注から外す
)

// orderRowActions は、状態 status の行に効く操作と、押す前に確かめる文（空なら確かめない）です。
//
// ⚠ **効くのは「次の一手」と「取消」だけ**です。4つの値をどの行にも効かせると、
// **紙の上の状態と関係なく押せてしまいます**——`未発注` の行に「納品済」が効くのは、
// 手が滑るのを待っているのと同じです。
//
// ⚠ **取消はどの段からも押せます**（2026-09-22 ユーザー:「**発注した後でも取り消す
// 場合があり得ます**…**発注後に材料屋から取り扱いが無いと連絡が来てキャンセルに
// なることもあります**」）。⚠ **納品済からも**——届いたあとに返すこともあるので、
// 「2手かければ戻せる」では足りません。
//
// ⚠ **紙が外へ出たあとの取消は、押しただけでは終わりません**（ユーザー:「電話などで材料屋に取り消しを依頼して、**OKが出たら**
// 取り消しボタンを押します」）——確かめずに押せる形にすると、「押したから片付いた」と読まれ、**材料屋には注文が残ったまま**になります。
// 発注前は確かめません（ユーザー:「発注前なら**単純に**取り消します」）。
//
// 「↩ 発注から外す」（2026-09-24。2026-10-21 までの名前は「必要部材表へ戻す」）は**どの段からも押せ**、**いつも確かめます**（行が
// 部材発注書から消える）。送付後は相手に出した紙に載っている行なので、そう書きます（ユーザー:「発注書の送付後でも押せます」）。
// （2026-10-21 夕まで、これらは行の末尾のボタンでした——orderRowButtonsHTML。表の中にボタンを置かない形にした。）
func orderRowActions(status string) map[string]string {
	// ⚠ **取り消しても製造ページの「🧾 部材作業」には戻りません**（2026-09-23 に意味が変わった・order_status.go）。
	//    **確認の文にそう書くこと**——書かないと人は「どこかに戻った」と思い、**誰も買わないまま納期が来ます**。
	const backNote = "取り消すと、この部材はもう発注しません（製造ページの「🧾 部材作業」にも、また出てきません）。\n" +
		"もう一度発注したいときは「↩ 発注から外す」を押してください。"
	ret := "この行を部材発注書から外します（製造ページの「🧾 部材作業」で、また入れられます）。"
	if orderLineSent(status) { // 出した行（発注済・納品済・それまでの未納品——order_status.go の決まり）
		ret = "⚠ この行は既に相手に送った部材発注書に載っています。\n" +
			"外したら、訂正した部材発注書を再送してください。\n\n" + ret
	}
	out := map[string]string{orderActReturn: ret}
	switch strings.TrimSpace(status) {
	case OrderLineCancelled:
		out[orderActUnsent] = ""
	case OrderLineSent, orderLineLegacySent:
		out[orderActDelivered] = ""
		out[orderActCancel] = "⚠ この行は既に発注済みです。材料屋の了解は取れていますか？\n\n" + backNote
	case OrderLineDelivered:
		out[orderActSent] = "" // 納品済を取り消して「発注済」に戻す
		out[orderActCancel] = "⚠ この行は納品済です。現物を返す話になりますが、取り消しますか？\n\n" + backNote
	default: // 未発注・空欄
		out[orderActSent] = ""
		out[orderActCancel] = ""
	}
	return out
}

// addOrderRowPicks は部材発注明細表の各行の先頭にチェックを足します（その行に効く操作と確かめる文を持たせる）。
func addOrderRowPicks(table *html.Node) {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return
	}
	si := headerIndexOf(rows[0], "状態")
	prependRowChromeCells(table, "order-pick-cell", func(n int, tr *html.Node) string {
		status := ""
		if si >= 0 {
			status = strings.TrimSpace(cellText(tr, si))
		}
		acts := orderRowActions(status)
		var can []string
		for _, k := range []string{orderActSent, orderActDelivered, orderActUnsent, orderActCancel, orderActReturn} {
			if _, ok := acts[k]; ok {
				can = append(can, k)
			}
		}
		return `<input type="checkbox" class="order-pick" aria-label="この行を選ぶ" data-row="` + strconv.Itoa(n+1) + `" data-sig="` + rowSig(tr) + `" data-status="` +
			stdhtml.EscapeString(status) + `" data-can="` + strings.Join(can, " ") + `" data-ask-cancel="` + stdhtml.EscapeString(acts[orderActCancel]) +
			`" data-ask-return="` + stdhtml.EscapeString(acts[orderActReturn]) + `">`
	})
}

// orderRowBarHTML は部材発注明細表の下の、選んだ行への操作です（効かない行は飛ばす——orderRowActions）。
func orderRowBarHTML(orderID string) string {
	b := func(act, value, label, title string) string {
		return `<button type="button" class="chip-btn order-sel-act" data-order-page="` + stdhtml.EscapeString(orderID) + `" data-act="` + act +
			`" data-order-status="` + stdhtml.EscapeString(value) + `" title="` + stdhtml.EscapeString(title) + `">` + label + `</button> `
	}
	return `<div class="order-row-bar">選んだ行を: ` +
		b(orderActSent, OrderLineSent, "✓ 発注済", "選んだ行を発注済みにします（納品済の行は発注済に戻します）") +
		b(orderActDelivered, OrderLineDelivered, "📦 納品済", "選んだ行の品が届きました（発注済の行だけ）") +
		b(orderActUnsent, OrderLineUnsent, "↩ 取消をやめる", "取り消した行を「未発注」に戻します") +
		b(orderActCancel, OrderLineCancelled, "✕ 取消", "選んだ行を取り消します（送ったあとの行は、相手の了解が要ります）") +
		b(orderActReturn, "", "↩ 発注から外す", "選んだ行を部材発注書から外し、製造ページの「🧾 部材作業」で、また入れられるようにします") +
		// 加工製品の部材の行に結ぶ（2026-10-23・order_bind.go——業者の納品書から作った行は、どの部材かを人が選ぶ）。1行ずつ。
		`<button type="button" class="chip-btn order-bind-open" data-order-page="` + stdhtml.EscapeString(orderID) + `" title="選んだ1行を、加工製品ページの部材の行（材料表・外注加工表など）に結びます。結ぶと、その部材の最新単価に出ます">🔗 加工製品の行に結ぶ</button> ` +
		`<span class="order-row-say"></span></div>`
}
