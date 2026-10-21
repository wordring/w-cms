package toho

// 加工時間（2026-10-21 夜・【要求】製造 §4「加工時間は製造のページに書き、加工製品ページに鏡で出します」）。
//
// > 2026-10-21 利用者:「順にお願いします」（【要求】の下書きで、まだ作っていなかったものを順に——加工時間が1つ目）
//
//   - 書くのは製造ページの製造明細表の列「加工時間」です（1行＝この製造で作る、案件×加工製品のぶん）。単位は時間（筆者の決め）。
//   - 加工製品ページには「⏱ 加工時間」として、その品物を作った製造ページの記録を新しい順に並べます（受注・見積の単価の下）。
//     材料の最新単価と同じく鏡で、本文には書きません。見積もりと実績を同じページに並べるためです。
//   - 1個あたりは、加工時間 ÷ 数量（Go で割る——機械の読みを機械に検算させない）。

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

// WorkHoursLabel は製造明細表の加工時間の列の見出しです。
const WorkHoursLabel = "加工時間"

// workHoursRow は「⏱ 加工時間」の1行です。
type workHoursRow struct {
	MfgPage int
	Title   string
	Started string
	Order   string
	Qty     float64
	Hours   float64
}

// workHoursForProduct は、加工製品ページ productID を作った製造ページの、加工時間の書いてある行を新しい順に返します。
// 見えない製造ページ・テンプレートの中は除きます。
func workHoursForProduct(user *auth.User, productID int) []workHoursRow {
	rows, err := cms.VocabRowsOfType(database.DB, MfgItemsType)
	if err != nil {
		return nil
	}
	want := page.FormatID(productID)
	ok := map[int]bool{}
	var out []workHoursRow
	for _, r := range rows {
		pid, okP := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		if !okP || pid != want {
			continue
		}
		h, okH := estimateNumber(r.Values["work-hours"])
		if !okH || h <= 0 {
			continue
		}
		v, seen := ok[r.PageID]
		if !seen {
			v = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindMfg && page.CanView(user, r.PageID)
			ok[r.PageID] = v
		}
		if !v {
			continue
		}
		q, _ := estimateNumber(r.Values["quantity"])
		order, _ := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/"))
		started := ""
		if tags, err := cms.TagsOfPage(database.DB, r.PageID); err == nil {
			started = strings.TrimSpace(cms.FirstTag(tags, MfgStartTag))
		}
		out = append(out, workHoursRow{MfgPage: r.PageID, Title: pageTitleOf(page.FormatID(r.PageID)), Started: started, Order: order, Qty: q, Hours: h})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := calendarKey(out[i].Started), calendarKey(out[j].Started); a != b {
			return a > b
		}
		return out[i].MfgPage > out[j].MfgPage
	})
	return out
}

// fmtHours は時間を短く書きます（小数は2桁まで・末尾の0は落とす）。
func fmtHours(h float64) string {
	s := strconv.FormatFloat(h, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

// workHoursHTML は「⏱ 加工時間」を描きます（行が無ければ短い断り）。
func workHoursHTML(list []workHoursRow) string {
	var b strings.Builder
	b.WriteString(`<div class="vocab-chrome rfq-quotes work-hours" contenteditable="false"><p class="materials-title">⏱ 加工時間</p>`)
	if len(list) == 0 {
		b.WriteString(`<p class="materials-empty w-view-empty">まだありません（製造ページの製造明細表の「` + WorkHoursLabel + `」に、作るのにかかった時間を書くと、ここに並びます）。</p></div>`)
		return b.String()
	}
	b.WriteString(`<div class="rfq-quotes-scroll"><table class="materials-table rfq-quotes-table"><thead><tr><th>始めた日</th><th>製造ページ</th><th>案件</th>` +
		`<th class="num">数量</th><th class="num">加工時間</th><th class="num">1個あたり</th></tr></thead><tbody>`)
	for _, r := range list {
		id := page.FormatID(r.MfgPage)
		started := r.Started
		if started == "" {
			started = "—"
		}
		order := "—"
		if r.Order != "" {
			order = `<a href="/` + r.Order + `">/` + r.Order + `</a>`
		}
		qty, per := "—", "—"
		if r.Qty > 0 {
			qty = fmtQty(r.Qty)
			per = fmtHours(r.Hours/r.Qty) + " 時間"
		}
		b.WriteString(`<tr><td>` + stdhtml.EscapeString(started) + `</td><td><a href="/` + id + `">` + stdhtml.EscapeString(r.Title) + `</a></td><td>` + order +
			`</td><td class="num">` + stdhtml.EscapeString(qty) + `</td><td class="num">` + fmtHours(r.Hours) + ` 時間</td><td class="num">` + per + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}
