package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注部材表の足元に「発注書を作る」を出す（2026-09-22）
//
// ユーザー:「**発注部材表から発注書を作るので「発注書を作る」ボタンは発注部材表の下に
// あるはずです**」。
//
// ⚠ **最初の実装は、未発注の表のフォームに両方のボタンを置いていました**——
// 「表作成」と「発注書作成」が並んでいて、**どちらの表から発注書ができるのか**が
// 画面から読み取れませんでした。**ボタンは、その相手の隣にあるべき**です。
//
// ⚠ **表が何枚あってもかまいません**（同日のユーザー訂正）。**1枚＝1社**なので、
// **業者ごとに同時に進める**のが普通の形です——だから**それぞれの表に**自分の
// 「発注書を作る」が付きます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

func init() {
	cms.RegisterMirror(OrderDraftType, cms.MirrorHandlerFunc(renderOrderDraftForm))
}

// renderOrderDraftForm は発注部材表の足元に、発注書を作る欄を足します。
//
// ⚠ **鏡なので本文には残りません**（`.vocab-chrome`）——人が画面の表をコピーして
// 貼っても、入力欄が本文に焼き付くことはありません。
func renderOrderDraftForm(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	span := headerCellCount(el)
	if span <= 0 {
		span = len(columnsOf(OrderDraftType))
	}
	// この表が何枚目かを数えます（画面が「どの表から作るか」を送るため）。
	idx := draftIndexOf(ctx)
	// ⚠ **行ごとに「戻す」を付けます**（2026-09-22 ユーザー:「**発注部材表から
	//    未手配の一覧へ戻す方法がありません**」）。
	//    ⚠ **「戻す」は「外す」です**——一覧は毎回計算される鏡なので、ここから消せば
	//    **自動的に戻ってきます**。戻す先へ何かを書く必要はありません。
	addDraftRowButtons(el, page.FormatID(ctx.PageID), idx)
	appendFootHTML(el, span, "draft-form-row",
		draftFormHTML(ctx.Viewer, page.FormatID(ctx.PageID), idx))
	return false, nil
}

// addDraftRowButtons は各行の末尾に「戻す」のセルを足します。
//
// ⚠ **鏡なので本文には残りません**（`.vocab-chrome`）。⚠ **見出し行にも1つ足します**
// ——足さないと**列がずれて見えます**（見出しが1つ足りない表になる）。
func addDraftRowButtons(table *html.Node, pageID string, idx int) {
	addRowChromeCells(table, "draft-row-act", func(row int, _ *html.Node) string {
		return `<button type="button" class="chip-btn draft-row-back"` +
			` data-draft-page="` + stdhtml.EscapeString(pageID) + `"` +
			` data-draft-table="` + strconv.Itoa(idx) + `"` +
			` data-draft-row="` + strconv.Itoa(row) + `"` +
			` title="必要部材表へ戻します（この行を外します）">↩ 戻す</button>`
	})
}

// draftIndexOf は、その表がページの何枚目の発注部材表かを返します（1始まり）。
//
// ⚠ **鏡は表ごとに呼ばれる**ので、自分が何枚目かは自分では分かりません。
// `MirrorContext.Counter` が形式ごとの文書順の連番を配るので、それを使います。
func draftIndexOf(ctx *cms.MirrorContext) int {
	return ctx.Counter(OrderDraftType) + 1
}

// draftFormHTML は「発注書を作る」の欄です。
//
// ⚠ **仕入先と差出人はここで決めます**——未発注の表ではありません。
// **発注書は1枚に1社**で、その1社が決まるのは**この表を作り終えたとき**だからです。
func draftFormHTML(user *auth.User, pageID string, idx int) string {
	f := func(id, label, ph, typ string) string {
		return searchFieldHTML("draft", id, label, ph, typ)
	}
	return `<div class="matsearch-form draft-form" data-draft-page="` +
		stdhtml.EscapeString(pageID) + `" data-draft-index="` + strconv.Itoa(idx) + `">` +
		signerFieldHTML(user) +
		f("supplier", "仕入先", "みなと商店", "text") +
		f("order_at", "発注日", "", "date") +
		f("due", "納期", "", "date") +
		f("note", "備考", "定尺で結構です", "text") +
		`<button type="button" class="matsearch-go" data-draft-go="1">発注書を作る</button>` +
		`</div><div class="unorder-result" data-draft-result="1"></div>`
}
