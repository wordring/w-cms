package toho

// 原価と利益（2026-10-21 夜・【要求】案件 §5 の下書き「原価と利益」）。
//
// > 2026-10-21 利用者:「順にお願いします」（下書きのうち、まだ作っていなかったものを順に——粗利と利益率が2つ目）
//
// 下書きの式のうち、記録から数えられる「確定」の分を出します（筆者の決め——違えば直します）。
//
//	売上   ＝ 案件明細表の 単価 × 数量（状態が「完了」の行は入れない——もう出さない行・改定で差し替えた行。受注残表と同じ線）
//	原価   ＝ 部材発注明細表の 単価 × 数量のうち、この案件に当たるぶん（行の「案件」の列、まとめた行は「内訳」で分ける——rowSupplies と同じ）
//	         取消の行は入れない。発注済・納品済は「発注済」、未発注の行は「未発注」として分けて見せる。備考「追加」の行（作り直し）も入れる。
//	粗利   ＝ 売上 − 原価（発注済＋未発注）
//	利益率 ＝ 粗利 ÷ 売上
//
//   - ⚠ 原価は部材（材料・外注加工・購入部品）だけです。加工の手間（加工時間）は入りません。
//   - 単価の無い行は金額に入れず、行の数を添えます（売上・原価とも）。
//   - 下書きの「予定売上（弊社が出した見積）」「予定原価（業者から貰った見積）」は、まだ出しません。
//   - 見積の依頼の案件・自社案件には出しません（売上ではない）。
//
// 出す場所: 案件ページの案件明細表の下（鏡）と、スラッシュメニューから挿すビュー「原価と利益の一覧」（案件ごとに1行）。

import (
	stdhtml "html"
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// CaseProfitListViewType は「原価と利益の一覧」のビューの形式です。
const CaseProfitListViewType = "case-profit-list"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type: CaseProfitListViewType, DisplayName: "原価と利益の一覧", Category: "ビュー", Icon: "💹", Element: "section",
		View: true,
	})
	cms.RegisterView(CaseProfitListViewType, caseProfitListHTML)
}

// caseProfit は1つの案件の売上と部材の原価です。
type caseProfit struct {
	Sales        float64
	SalesNoPrice int // 数量はあるのに単価の読めない案件明細の行
	CostSent     float64
	CostUnsent   float64
	CostNoPrice  int // この案件に当たるのに単価の読めない部材発注の行
	CostRows     int // この案件に当たる部材発注の行（単価の無い行も）——0なら原価は「記録なし」
}

func (p caseProfit) cost() float64   { return p.CostSent + p.CostUnsent }
func (p caseProfit) profit() float64 { return p.Sales - p.cost() }

// hasCost は、この案件に当たる部材発注の行があるかです。無ければ原価は分からない（0円ではない）ので、粗利と利益率も出しません。
func (p caseProfit) hasCost() bool { return p.CostRows > 0 }

// rate は利益率（%）です。売上が0か、原価の記録が無ければ ok=false。
func (p caseProfit) rate() (float64, bool) {
	if p.Sales <= 0 || !p.hasCost() {
		return 0, false
	}
	return p.profit() / p.Sales * 100, true
}

// costEntries は部材発注の行が当たる案件×加工製品と数です（rowSupplies と同じ分け方。ただし備考「追加」の行も入れる——
// 手配済みには数えないが、買ったものは原価です）。
func costEntries(r cms.VocabRow) []breakdownEntry {
	if isExtraLine(r.Values["note"]) {
		v := make(map[string]string, len(r.Values))
		for k, x := range r.Values {
			v[k] = x
		}
		v["note"] = ""
		r.Values = v
	}
	return rowSupplies(r)
}

// caseProfits は、見える案件ページごとの売上と部材の原価を数えます（テンプレートの中は除く）。
// unassigned は、どの案件にも当たらない部材発注の行の数です（「案件」の列が空の行——2026-10-19 より前の部材発注書は書いていない）。
func caseProfits(db cms.ReadOnlyDB, canView func(int) bool) (out map[int]*caseProfit, unassigned int) {
	out = map[int]*caseProfit{}
	of := func(id int) *caseProfit {
		p := out[id]
		if p == nil {
			p = &caseProfit{}
			out[id] = p
		}
		return p
	}
	visible := map[int]bool{}
	ok := func(id int) bool {
		v, seen := visible[id]
		if !seen {
			v = canView(id) && !cms.IsTemplateArea(page.FormatID(id))
			visible[id] = v
		}
		return v
	}
	if rows, err := cms.VocabRowsOfType(db, clientOrderItemsType); err == nil {
		for _, r := range rows {
			if !ok(r.PageID) || strings.TrimSpace(r.Values["status"]) == StatusDone {
				continue
			}
			qty, okQ := parseMoney(r.Values["quantity"])
			if !okQ || qty <= 0 {
				continue
			}
			price, okP := parseMoney(r.Values["price"])
			if !okP {
				of(r.PageID).SalesNoPrice++
				continue
			}
			of(r.PageID).Sales += qty * price
		}
	}
	if rows, err := cms.VocabRowsOfType(db, ourOrderItemsType); err == nil {
		for _, r := range rows {
			st := r.Values["status"]
			if !ok(r.PageID) || orderLineCancelled(st) {
				continue
			}
			price, okP := parseMoney(r.Values["cost"])
			hit := false
			for _, e := range costEntries(r) {
				id, err := strconv.Atoi(e.Order)
				if err != nil || id <= 0 || e.Qty <= 0 {
					continue
				}
				hit = true
				p := of(id)
				p.CostRows++
				switch {
				case !okP:
					p.CostNoPrice++
				case orderLineSent(st):
					p.CostSent += float64(e.Qty) * price
				default:
					p.CostUnsent += float64(e.Qty) * price
				}
			}
			if !hit {
				unassigned++
			}
		}
	}
	return out, unassigned
}

// signedYen は金額を「1,234円」に（負なら「−1,234円」——粗利は負になる。estimate.go の yen は負を考えない）。
func signedYen(v float64) string {
	n := int(math.Round(v))
	if n < 0 {
		return "−" + comma(-n) + "円"
	}
	return comma(n) + "円"
}

// profitRateText は利益率の文字です（売上が0か、原価の記録が無ければ「—」）。
func profitRateText(p caseProfit) string {
	r, ok := p.rate()
	if !ok {
		return "—"
	}
	return strconv.FormatFloat(math.Round(r*10)/10, 'f', -1, 64) + "%"
}

// caseProfitHTML は案件ページの「💹 原価と利益」です。
func caseProfitHTML(p caseProfit) string {
	var b strings.Builder
	b.WriteString(`<div class="vocab-chrome rfq-quotes case-profit" contenteditable="false"><p class="materials-title">💹 原価と利益（部材のぶん）</p><ul class="case-profit-list">`)
	sales := signedYen(p.Sales)
	if p.SalesNoPrice > 0 {
		sales += `（⚠ 単価の無い行 ` + strconv.Itoa(p.SalesNoPrice) + ` は入っていません）`
	}
	b.WriteString(`<li>売上（案件明細表の 単価 × 数量）: ` + sales + `</li>`)
	cost := signedYen(p.cost()) + `（発注済 ` + signedYen(p.CostSent) + `・未発注 ` + signedYen(p.CostUnsent) + `）`
	if !p.hasCost() {
		cost = `記録なし（この案件を「案件」の列に書いた部材発注書の行がありません）`
	}
	if p.CostNoPrice > 0 {
		cost += `（⚠ 単価の無い部材発注の行 ` + strconv.Itoa(p.CostNoPrice) + ` は入っていません）`
	}
	b.WriteString(`<li>部材の原価（部材発注書の 単価 × 数量・取消を除く）: ` + cost + `</li>`)
	profit := "—"
	if p.hasCost() {
		profit = signedYen(p.profit())
	}
	b.WriteString(`<li>粗利: ` + profit + `・利益率: ` + profitRateText(p) + `</li></ul>`)
	b.WriteString(`<p class="materials-empty w-view-empty">原価は部材だけです（加工の手間は入りません）。見積の値段（予定）は、まだ入れていません。</p></div>`)
	return b.String()
}

// appendCaseProfit は案件明細表の直後に「💹 原価と利益」を出します（表の中には入れない）。
func appendCaseProfit(ctx *cms.MirrorContext, table *html.Node) {
	if table.Parent == nil {
		return
	}
	all, _ := caseProfits(ctx.DB, func(id int) bool { return page.CanView(ctx.Viewer, id) })
	p := caseProfit{}
	if x := all[ctx.PageID]; x != nil {
		p = *x
	}
	nodes, err := htmldoc.ParseFragment(caseProfitHTML(p))
	if err != nil {
		return
	}
	at := table.NextSibling
	for at != nil && at.Type == html.ElementNode && hasClassName(at, "vocab-chrome") {
		at = at.NextSibling
	}
	for _, n := range nodes {
		table.Parent.InsertBefore(n, at)
	}
}

// caseProfitListHTML は「原価と利益の一覧」のビューです——見える案件ページ（顧客発注書）ごとに1行、新しい順。
func caseProfitListHTML(user *auth.User, pageIDInt int) string {
	if user == nil {
		return ""
	}
	all, unassigned := caseProfits(database.DB, func(id int) bool { return page.CanView(user, id) })
	type row struct {
		id    int
		title string
		date  string
		p     caseProfit
	}
	var rows []row
	var total caseProfit // 原価の記録のある案件だけを足す（記録の無い案件の売上を入れると、利益率が高く見える）
	withCost := 0
	for id, p := range all {
		if pageKindOf(id) != PageKindCustomerOrder || (p.Sales == 0 && p.cost() == 0 && p.SalesNoPrice == 0) {
			continue
		}
		rows = append(rows, row{id, pageTitleOf(page.FormatID(id)), cms.PageTagValue(database.DB, id, OrderedAtTag), *p})
		if p.hasCost() {
			withCost++
			total.Sales += p.Sales
			total.CostSent += p.CostSent
			total.CostUnsent += p.CostUnsent
			total.CostRows += p.CostRows
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if a, b := calendarKey(rows[i].date), calendarKey(rows[j].date); a != b {
			return a > b
		}
		return rows[i].id > rows[j].id
	})
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">💹 原価と利益の一覧（部材のぶん）</h3>`)
	if len(rows) == 0 {
		b.WriteString(`<p class="materials-empty w-view-empty">まだありません（案件明細表に単価の入った案件ページか、部材発注書の行が当たる案件ページが並びます）。</p>`)
		return b.String()
	}
	if withCost > 0 {
		b.WriteString(`<p class="unorder-help">合計（部材の記録のある案件 ` + strconv.Itoa(withCost) + ` 件）: 売上 ` + signedYen(total.Sales) + `・部材の原価 ` + signedYen(total.cost()) +
			`・粗利 ` + signedYen(total.profit()) + `・利益率 ` + profitRateText(total) + `</p>`)
	} else {
		b.WriteString(`<p class="unorder-help">部材の記録のある案件が、まだありません（粗利と利益率は出せません）。</p>`)
	}
	if unassigned > 0 {
		b.WriteString(`<p class="unorder-help">⚠ 「案件」の列が空の部材発注の行が ` + strconv.Itoa(unassigned) + ` 行あり、どの案件の原価にも入っていません（2026-10-19 より前の部材発注書は、案件を書いていません）。</p>`)
	}
	b.WriteString(`<div class="rfq-quotes-scroll"><table class="materials-table rfq-quotes-table"><thead><tr><th>発注日</th><th>案件</th>` +
		`<th class="num">売上</th><th class="num">部材の原価</th><th class="num">粗利</th><th class="num">利益率</th><th>⚠</th></tr></thead><tbody>`)
	for _, r := range rows {
		warn := ""
		if r.p.SalesNoPrice > 0 {
			warn += "単価の無い明細 " + strconv.Itoa(r.p.SalesNoPrice) + " "
		}
		if r.p.CostNoPrice > 0 {
			warn += "単価の無い部材 " + strconv.Itoa(r.p.CostNoPrice)
		}
		date := r.date
		if date == "" {
			date = "—"
		}
		b.WriteString(`<tr><td>` + stdhtml.EscapeString(date) + `</td><td><a href="/` + page.FormatID(r.id) + `">` + stdhtml.EscapeString(r.title) + `</a></td>` +
			`<td class="num">` + signedYen(r.p.Sales) + `</td><td class="num">` + costCellText(r.p) + `</td><td class="num">` + profitCellText(r.p) +
			`</td><td class="num">` + profitRateText(r.p) + `</td><td>` + stdhtml.EscapeString(strings.TrimSpace(warn)) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div><p class="materials-empty w-view-empty">原価は部材だけです（加工の手間は入りません）。案件明細表の状態が「完了」の行と、取消の部材発注の行は入れません。</p>`)
	return b.String()
}

// costCellText・profitCellText は一覧のセルです（原価の記録が無ければ「記録なし」「—」）。
func costCellText(p caseProfit) string {
	if !p.hasCost() {
		return "記録なし"
	}
	return signedYen(p.cost())
}

func profitCellText(p caseProfit) string {
	if !p.hasCost() {
		return "—"
	}
	return signedYen(p.profit())
}
