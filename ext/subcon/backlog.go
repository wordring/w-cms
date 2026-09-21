package subcon

// ─────────────────────────────────────────────────────────────────────────
// 受注残表——納期順・顧客×納期ごとに1枚（2026-09-21）
//
// ユーザー:「**受注フォルダのトップページには、納期順の受注残表が必要です。**
// 顧客、納期ごとに別の表として分けて、**ワンタッチで印刷**もできるとありがたい」。
//
// ⚠ **顧客×納期の組が、そのまま納品の単位になります**（ユーザー決定）。だから
// 1枚が1回の納品に対応し、**そのまま納品書の下敷き**になります。
//
// ⚠ **製造順はここで決まります。** ユーザー:「製造順なんですが、**基本的には納期順**
// なので、受注明細の納期で決まります。同じ納期の品物同士で順番は作業者が決めます」
// ——つまり**並べる軸のために新しいデータは1つも要りません**。受注明細を納期で並べる
// だけで、同じ納期の中の順は**人がその場で決める**ので記録しません
// （[docs/【考察】製造という軸.md] §2.1・§4）。
//
// ⚠ **残っている行だけ出します**（`数量 - 出荷済み > 0`）。全部出すと、納品済みの
// 行に埋もれて「あと何を作るか」が見えません——**受注残表の役目はそこだけ**です。
//
// ⚠ **スコープは「このビューを置いたページの子孫」**です。受注箱に置けば全部、
// `受注／2026年／09月` に置けばその月だけ。**箱を名前で決め打ちしません**
// ——決め打ちすると、置き場所を変えた日に黙って空になります。
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

// BacklogViewType は受注残表の形式名です。
const BacklogViewType = "order-backlog"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type:        BacklogViewType,
		DisplayName: "受注残",
		Category:    "ビュー",
		Icon:        "📋",
		Element:     "section",
		View:        true,
	})
	cms.RegisterView(BacklogViewType, backlogViewHTML)
}

// backlogRow は受注残の1行です。
type backlogRow struct {
	OrderPageID string // どの受注ページの行か（押せば飛べる）
	OrderNo     string
	OurItemNo   string
	ItemNo      string
	ItemName    string
	Quantity    int
	Shipped     int
	Remaining   int
	Status      string
	Note        string
}

// backlogGroup は「顧客×納期」1組＝**1回の納品の単位**です。
type backlogGroup struct {
	Client string
	Due    string // 生の値（日付とは限らない——「最短納期」など）
	DueISO string // 日付として読めたときの並べ替え用（読めなければ空）
	Rows   []backlogRow
}

// backlogGroups は指定ページの子孫から受注残を集め、顧客×納期で束ねて返します。
func backlogGroups(user *auth.User, rootID int) []backlogGroup {
	db := database.DB
	// ⚠ **先に読み切ってから解釈します**——行を読みながら別のクエリを投げると、
	// `:memory:` では別の空DBに当たります（2026-09-03 に本番で踏んだ罠）。
	rows, err := db.Query(
		`SELECT DISTINCT page_id FROM vocab_index WHERE data_type = ?`, clientOrderItemsType)
	if err != nil {
		return nil
	}
	var pageIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			pageIDs = append(pageIDs, id)
		}
	}
	rows.Close()

	byKey := map[string]*backlogGroup{}
	for _, id := range pageIDs {
		// ⚠ **子孫だけ**（自分自身は受注ページではないので含めない）。
		if !cms.IsDescendantOf(db, id, rootID) {
			continue
		}
		// ⚠ **読めないページは黙って落とします**（見せ分け・C案）。
		if !page.CanView(user, id) {
			continue
		}
		tags, err := cms.TagsOfPage(db, id)
		if err != nil {
			continue
		}
		client := cms.FirstTag(tags, OrderClientTag)
		pageDue := cms.FirstTag(tags, DueDateTag)
		orderNo := cms.FirstTag(tags, OrderNoTag)

		items, err := cms.VocabTableRowsOf(db, id, clientOrderItemsType)
		if err != nil {
			continue
		}
		for _, r := range items {
			qty := r.Num("quantity")
			shipped := r.Num("shipped")
			rem := qty - shipped
			// ⚠ **残っている行だけ**。`数量` が読めない行（0）も落ちます——
			// 数が無ければ「あと何個」が言えないので、残表には載せられません。
			if rem <= 0 {
				continue
			}
			due := strings.TrimSpace(r.Values["due"])
			if due == "" {
				// ⚠ **行に無ければページの納期**。受注時は同じ値が配られますが、
				// 人が行を空にすることもあるので、ページ側へ落ちます。
				due = pageDue
			}
			key := client + "\x00" + due
			g := byKey[key]
			if g == nil {
				iso, _ := cms.NormalizeValue(cms.ColDate, due)
				g = &backlogGroup{Client: client, Due: due, DueISO: iso}
				byKey[key] = g
			}
			g.Rows = append(g.Rows, backlogRow{
				OrderPageID: page.FormatID(id),
				OrderNo:     orderNo,
				OurItemNo:   strings.TrimSpace(r.Values["our-item-id"]),
				ItemNo:      strings.TrimSpace(r.Values["item-id"]),
				ItemName:    strings.TrimSpace(r.Values["item-name"]),
				Quantity:    qty,
				Shipped:     shipped,
				Remaining:   rem,
				Status:      strings.TrimSpace(r.Values["status"]),
				Note:        strings.TrimSpace(r.Values["note"]),
			})
		}
	}

	out := make([]backlogGroup, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, *g)
	}
	sortBacklogGroups(out)
	return out
}

// sortBacklogGroups は納期順に並べます。
//
// ⚠ **日付として読めない納期を、後ろへ埋めません。** 実データの1通目が「**最短納期**」
// でした——いちばん急ぐものを最後に置くと、**見落とします**。日付でないものは
// **先頭**にまとめ、画面で「日付として読めない納期」と断ります。
// 「都度」のように急がないものも混じりますが、**急ぎを埋もれさせるより、
// 急がないものが目に付くほうが安全**です。
func sortBacklogGroups(gs []backlogGroup) {
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if (a.DueISO == "") != (b.DueISO == "") {
			return a.DueISO == "" // 読めないものが先
		}
		if a.DueISO != b.DueISO {
			return a.DueISO < b.DueISO
		}
		return a.Client < b.Client
	})
}

// backlogViewHTML は受注残表を組みます（サーバー事前描画）。
func backlogViewHTML(user *auth.User, pageIDInt int) string {
	groups := backlogGroups(user, pageIDInt)
	if len(groups) == 0 {
		return `<p class="child-list-empty">受注残はありません（このページの下に、` +
			`まだ出していない明細がありません）。</p>`
	}
	var b strings.Builder
	for i, g := range groups {
		b.WriteString(`<section class="backlog-sheet" data-backlog="` + strconv.Itoa(i) + `">`)
		title := g.Client
		if title == "" {
			title = "（発注元なし）"
		}
		due := g.Due
		if due == "" {
			due = "（納期なし）"
		}
		b.WriteString(`<h3 class="backlog-title">` + stdhtml.EscapeString(title) +
			`　納期 ` + stdhtml.EscapeString(due))
		if g.DueISO == "" {
			// ⚠ **黙って並べない。** 日付として読めなかったことを言います。
			b.WriteString(`<span class="backlog-warn">⚠ 日付として読めない納期</span>`)
		}
		b.WriteString(`</h3>`)
		// 印刷ボタンは**クローム**なので保存されません。配線は assets/app.js。
		b.WriteString(`<button type="button" class="backlog-print" ` +
			`data-backlog-print="` + strconv.Itoa(i) + `">🖨 この表を印刷</button>`)
		b.WriteString(`<table class="backlog-table"><tbody>` +
			`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>残</th>` +
			`<th>数量</th><th>出荷済み</th><th>状態</th><th>備考</th><th>受注</th></tr>`)
		for _, r := range g.Rows {
			b.WriteString(`<tr>` +
				`<td>` + refCellHTML(r.OurItemNo) + `</td>` +
				`<td>` + stdhtml.EscapeString(r.ItemNo) + `</td>` +
				`<td>` + stdhtml.EscapeString(r.ItemName) + `</td>` +
				`<td class="backlog-remaining">` + strconv.Itoa(r.Remaining) + `</td>` +
				`<td>` + strconv.Itoa(r.Quantity) + `</td>` +
				`<td>` + shippedCell(r.Shipped) + `</td>` +
				`<td>` + stdhtml.EscapeString(r.Status) + `</td>` +
				`<td>` + stdhtml.EscapeString(r.Note) + `</td>` +
				`<td><a href="/` + stdhtml.EscapeString(r.OrderPageID) + `">` +
				stdhtml.EscapeString(orderLabel(r)) + `</a></td></tr>`)
		}
		b.WriteString(`</tbody></table></section>`)
	}
	return b.String()
}

// refCellHTML は弊社品番を押せる形にします（空なら空欄のまま）。
//
// ⚠ **`RenderReferenceLinks` は通りません**——あれは本文の表だけを見るので、
// クロームの中には掛かりません。ここで自分でリンクにします（サニタイズの**後**に
// HTMLを足す関数は、自分でエスケープの責任を負う、という既存の規律のとおり）。
func refCellHTML(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return `<a href="/` + stdhtml.EscapeString(id) + `">` + stdhtml.EscapeString(id) + `</a>`
}

// shippedCell は出荷済みを出します（0 は空欄——「まだ出していない」の見た目を揃える）。
func shippedCell(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// orderLabel は受注の見出しです（発注書番号があればそれ、無ければページID）。
func orderLabel(r backlogRow) string {
	if r.OrderNo != "" {
		return r.OrderNo
	}
	return r.OrderPageID
}
