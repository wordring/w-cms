package toho

// 進捗ボード（2026-10-21 夜・【要求】製造 §5 の下書き「状態をもとに、進捗を見渡す画面。部品ごとに、どの部材発注書に紐づいているかも、
// 一目で分かるようにします」）。
//
// > 2026-10-21 利用者:「順にお願いします」（下書きのうち、まだ作っていなかったものを順に——進捗ボードが3つ目）
//
// スラッシュメニューから挿すビュー「進捗ボード」です。並べる行は案件残表と同じ（backlogGroups——移行中・完了・出し終えた行は入らない）。
// 段ごとに分けて、納期の早い順に並べます（筆者の決め——違えば直します）。
//
//	未着手 → 製造待ち（「製造」へ入れた）→ 製造中（製造ページがある）→ 製造完了（納品待ち）
//
// 1行に、客先・納期・品番・品名・残・製造ページ・部材発注書（この案件×加工製品に当たる行のある部材発注書と、その行の状態）を出します。
// 部材発注書の当て方は手配済みの帳簿と同じです（行の「案件」の列、まとめた行は「内訳」——costEntries）。取消の行も状態として見せます。
//
// ⚠ 見せるだけです（本文に書かない・押して変える所は無い）。段を進めるのは、いままでどおり案件残表・製造ページです。

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

// ProgressBoardViewType は進捗ボードのビューの形式です。
const ProgressBoardViewType = "progress-board"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type: ProgressBoardViewType, DisplayName: "進捗ボード", Category: "ビュー", Icon: "📊", Element: "section",
		View: true,
	})
	cms.RegisterView(ProgressBoardViewType, progressBoardHTML)
}

// 進捗ボードの段（並べる順）。
var progressStages = []string{"未着手", MfgWaitLabel, "製造中", "製造完了（納品待ち）"}

// progressStageOf は行の段です（いちばん進んだ段）。
func progressStageOf(r backlogRow) string {
	switch {
	case r.Made == MarkDone:
		return progressStages[3]
	case len(r.MfgPages) > 0:
		return progressStages[2]
	case r.Waiting > 0:
		return progressStages[1]
	}
	return progressStages[0]
}

// partOrderLink は部材発注書1枚と、この案件×加工製品に当たる行の状態です。
type partOrderLink struct {
	Page     int
	Statuses []string
}

// partOrdersByCase は「案件|弊社品番」ごとに、当たる部材発注書と行の状態を返します（見える部材発注書だけ・ページ番号の順）。
func partOrdersByCase(canView func(int) bool) map[string][]partOrderLink {
	rows, err := cms.VocabRowsOfType(database.DB, ourOrderItemsType)
	if err != nil {
		return nil
	}
	type acc struct {
		seen map[string]bool
		link *partOrderLink
	}
	byKey := map[string]map[int]*acc{}
	visible := map[int]bool{}
	for _, r := range rows {
		v, ok := visible[r.PageID]
		if !ok {
			v = canView(r.PageID) && !cms.IsTemplateArea(page.FormatID(r.PageID))
			visible[r.PageID] = v
		}
		if !v {
			continue
		}
		st := strings.TrimSpace(r.Values["status"])
		if st == "" {
			st = OrderLineUnsent
		}
		for _, e := range costEntries(r) {
			if e.Order == "" {
				continue
			}
			k := e.Order + "|" + e.Product
			m := byKey[k]
			if m == nil {
				m = map[int]*acc{}
				byKey[k] = m
			}
			a := m[r.PageID]
			if a == nil {
				a = &acc{seen: map[string]bool{}, link: &partOrderLink{Page: r.PageID}}
				m[r.PageID] = a
			}
			if !a.seen[st] {
				a.seen[st] = true
				a.link.Statuses = append(a.link.Statuses, st)
			}
		}
	}
	out := map[string][]partOrderLink{}
	for k, m := range byKey {
		var list []partOrderLink
		for _, a := range m {
			list = append(list, *a.link)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Page < list[j].Page })
		out[k] = list
	}
	return out
}

// progressBoardHTML は「進捗ボード」です。
func progressBoardHTML(user *auth.User, _ int) string {
	if user == nil {
		return ""
	}
	type item struct {
		g backlogGroup
		r backlogRow
	}
	byStage := map[string][]item{}
	for _, g := range backlogGroups(user) {
		for _, r := range g.Rows {
			if r.Quote {
				continue // 見積の依頼は、まだ作る物ではない
			}
			s := progressStageOf(r)
			byStage[s] = append(byStage[s], item{g, r})
		}
	}
	parts := partOrdersByCase(func(id int) bool { return page.CanView(user, id) })
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">📊 進捗ボード</h3>`)
	total := 0
	for _, s := range progressStages {
		total += len(byStage[s])
	}
	if total == 0 {
		b.WriteString(`<p class="materials-empty w-view-empty">まだありません（案件残表に並ぶ行が、段ごとに並びます）。</p>`)
		return b.String()
	}
	// 段ごとの数を先に1行で。
	var counts []string
	for _, s := range progressStages {
		counts = append(counts, s+" "+strconv.Itoa(len(byStage[s])))
	}
	b.WriteString(`<p class="unorder-help">` + stdhtml.EscapeString(strings.Join(counts, "・")) + `</p>`)
	for _, s := range progressStages {
		list := byStage[s]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool {
			a, c := list[i].g.DueISO, list[j].g.DueISO
			if a == "" || c == "" {
				return a != "" && c == ""
			}
			return a < c
		})
		b.WriteString(`<details open class="progress-stage"><summary>` + stdhtml.EscapeString(s) + `（` + strconv.Itoa(len(list)) + `）</summary>`)
		b.WriteString(`<div class="rfq-quotes-scroll"><table class="materials-table rfq-quotes-table"><thead><tr><th>納期</th><th>客先</th><th>案件</th>` +
			`<th>品番</th><th>品名</th><th class="num">残</th><th>製造ページ</th><th>部材発注書</th></tr></thead><tbody>`)
		for _, it := range list {
			r := it.r
			due := it.g.Due
			if due == "" {
				due = "—"
			}
			mfg := "—"
			if len(r.MfgPages) > 0 {
				var ls []string
				for _, p := range r.MfgPages {
					ls = append(ls, `<a href="/`+stdhtml.EscapeString(p)+`">/`+stdhtml.EscapeString(p)+`</a>`)
				}
				mfg = strings.Join(ls, " ")
			}
			po := "—"
			prod, _ := refPageID(r.OurItemNo)
			order, _ := refPageID(r.OrderPageID)
			if links := parts[order+"|"+prod]; prod != "" && len(links) > 0 {
				var ls []string
				for _, l := range links {
					id := page.FormatID(l.Page)
					ls = append(ls, `<a href="/`+id+`">/`+id+`</a> `+stdhtml.EscapeString(strings.Join(l.Statuses, "・")))
				}
				po = strings.Join(ls, "<br>")
			}
			b.WriteString(`<tr><td>` + stdhtml.EscapeString(due) + `</td><td>` + stdhtml.EscapeString(it.g.Client) + `</td><td><a href="/` +
				stdhtml.EscapeString(r.OrderPageID) + `">/` + stdhtml.EscapeString(r.OrderPageID) + `</a></td><td>` + stdhtml.EscapeString(r.ItemNo) +
				`</td><td>` + stdhtml.EscapeString(r.ItemName) + `</td><td class="num">` + strconv.Itoa(r.Remaining) + `</td><td>` + mfg + `</td><td>` + po + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div></details>`)
	}
	b.WriteString(`<p class="materials-empty w-view-empty">並ぶ行は案件残表と同じです。段を進めるのは案件残表と製造ページです（ここは見るだけ）。</p>`)
	return b.String()
}
