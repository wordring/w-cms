package subcon

// ─────────────────────────────────────────────────────────────────────────
// 未発注の発注書（2026-09-24）
//
// 要求（【要求】発注フォルダ）:「ここには未だ発注されていない発注書ページへのリンクを
// 表示します。DBから『未発注の発注書』を探します。作業者はここを見て残りの作業を進め
// ます。発注書ページを削除するとリンクも消えます」。
// 定義:「『未発注の発注書』を探すには、発注明細表のDBから状態が『未発注』の行の集合を
// 作り、その集合が持つページIDの集合を抽出します」。
//
// ⚠ **鏡です**（本文には空のマーカーだけ）——索引から毎回数えるので、発注書ページを
// 消せば（索引から行が消えて）**リンクも自動で消えます**。本文にリンクを書く形
// （発注部材表が化けるリンク・`our-order-link`）と違い、**消し忘れが起きません**。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// UnsentOrdersViewType は「未発注の発注書」の形式名です。
const UnsentOrdersViewType = "unsent-orders"

// UnsentOrder は未発注の行を持つ発注書ページ1枚です。
type UnsentOrder struct {
	PageID   int
	Title    string
	Supplier string
	Unsent   int // 未発注の行
	Total    int // 行の総数（取消も含む）
}

// orderLineUnsent は「まだ出していない行」かを返します。
//
// ⚠ **空欄も未発注に数えます**——人が手で足した行は `状態` が空のことがあり、
// それも**まだ出していない**ことに変わりありません。数えないと、その行だけの
// 発注書が**一覧から黙って消えます**。
func orderLineUnsent(status string) bool {
	s := strings.TrimSpace(status)
	return s == OrderLineUnsent || s == ""
}

// UnsentOrders は、閲覧者に見える発注書のうち、未発注の行を持つものを返します。
func UnsentOrders(user *auth.User) ([]UnsentOrder, error) {
	db := database.DB
	rows, err := cms.VocabRowsOfType(db, ourOrderItemsType)
	if err != nil {
		return nil, err
	}
	canView := viewCheck(user)
	by := map[int]*UnsentOrder{}
	var ids []int
	for _, r := range rows {
		if !canView(r.PageID) {
			continue
		}
		o, ok := by[r.PageID]
		if !ok {
			o = &UnsentOrder{PageID: r.PageID}
			by[r.PageID] = o
			ids = append(ids, r.PageID)
		}
		o.Total++
		if orderLineUnsent(r.Values["status"]) {
			o.Unsent++
		}
	}
	sort.Ints(ids) // 古い（先に作った）ものから
	var out []UnsentOrder
	for _, id := range ids {
		o := by[id]
		if o.Unsent == 0 {
			continue
		}
		o.Title = cms.PageTitleByID(id)
		if tags, err := cms.TagsOfPage(db, id); err == nil {
			o.Supplier = cms.FirstTag(tags, SupplierTag)
		}
		out = append(out, *o)
	}
	return out, nil
}

// unsentOrdersViewHTML は「未発注の発注書」の一覧を描きます。
func unsentOrdersViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">📮 未発注の発注書</h3>`
	list, err := UnsentOrders(user)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「全部出した」と「数えていない」を見分けるため。
		return head + `<p class="materials-empty">未発注の発注書はありません` +
			`（発注明細に「未発注」の行がある発注書ページが、ここに並びます）。</p>`
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`<table class="materials-table unsent-table"><thead><tr>` +
		`<th>発注書</th><th>仕入先</th><th class="num">未発注</th></tr></thead><tbody>`)
	for _, o := range list {
		id := page.FormatID(o.PageID)
		title := o.Title
		if title == "" {
			title = id
		}
		b.WriteString(`<tr><td><a href="/` + id + `">` + stdhtml.EscapeString(title) + `</a></td>` +
			`<td>` + stdhtml.EscapeString(o.Supplier) + `</td>` +
			`<td class="num">` + strconv.Itoa(o.Unsent) + ` / ` + strconv.Itoa(o.Total) + ` 行</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}
