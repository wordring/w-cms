package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注ページに残る「発注書へのリンク」（2026-09-22）
//
// ユーザー:「**発注書ページが出来て、実際に発注するまで発注ページに発注書ページへの
// リンクが残れば良いのでは？**」
//
// 発注部材表は、発注書ができると**このリンクに化けます**（`replaceDraftWithLink`）。
// そして**出したら「まだ発注していません」が消えます**——ここがその仕掛けです。
//
// ⚠ **印を二重に持ちません。** 最初の実装は「まだ発注していません」を**本文に
// 焼き込んで**いました。すると**発注書を出しても発注ページの文は古いまま**で、
// 直すには2か所を書き換えることになります——そして片方は必ず忘れられます。
//
// いまは**本文に残すのはリンクと行数だけ**（変わらないもの）で、**進み具合は
// 毎回発注書ページから読み直します**（変わるもの）。材料の参考単価と同じ形です。
//
// ⚠ **`data-ref` で指します**（題でも URL でもなく）。題は改名で切れ、URL は
// 配信のアドレスが変わると切れます——**本文は正本なので参照IDを置きます**
// （ファイル表示の配線と同じ判断・2026-09-15）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// OrderLinkType は発注ページに残る「発注書へのリンク」の形式です。
const OrderLinkType = "our-order-link"

func init() {
	cms.RegisterMirror(OrderLinkType, cms.MirrorHandlerFunc(renderOrderLinkState))
}

// renderOrderLinkState はリンクの後ろへ「いまどこまで出ているか」を足します。
//
// ⚠ **読めない発注書は「読めない」と言いません**——行数も仕入先も漏らさないため、
// **何も足さずに黙ります**（見せ分けと同じ規律：読めないと存在しないを区別させない）。
func renderOrderLinkState(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	id, ok := page.NormalizeID(strings.TrimSpace(cms.Attr(el, "data-ref")))
	if !ok {
		return false, nil
	}
	n := pageNum(id)
	if !page.CanView(ctx.Viewer, n) {
		return false, nil
	}
	counts := OrderSendStateOf(ctx.DB, n)
	appendHTML(el, `<span class="vocab-chrome order-link-state">`+
		orderSendStateHTML(counts)+`</span>`)
	return false, nil
}

// orderLinkHTML は発注部材表と入れ替える本文です。
//
// ⚠ **書くのは変わらないものだけ**です——リンク・仕入先・行数。**進み具合は
// 書きません**（鏡が毎回読み直します）。
func orderLinkHTML(orderID, supplier string, rows int) string {
	return `<section data-type="` + OrderLinkType + `" data-ref="` +
		stdhtml.EscapeString(orderID) + `"><p>📄 <a href="/` + stdhtml.EscapeString(orderID) +
		`">発注書 ` + stdhtml.EscapeString(orderID) + `　` +
		stdhtml.EscapeString(supplier) + `</a>（` + strconv.Itoa(rows) + `行）</p></section>`
}
