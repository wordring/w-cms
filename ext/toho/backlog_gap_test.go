package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestBacklogShowsGaps は、受注残表の「まだ無いもの」の欄が（2026-10-17・backlogGaps）、
//   - 発注書番号の空いた受注の行に「発注書番号」
//   - 単価の空いた行に「単価」（両方なら「発注書番号・単価」）
//   - 埋まっている行は空
//
// と出し、紙には出さないことを固定します（知らせるだけで、行は受注残表から消さない）。
func TestBacklogShowsGaps(t *testing.T) {
	setupExtTest(t, "000940", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 941, -1, "受注", "alice", "302", true)
	seedOrder(t, 942, 941, "あけぼの精工", "2026-11-01",
		pricedItem("A-1", "そろっている", "5", "100", "", "未着手")+pricedItem("A-2", "単価がまだ", "5", "", "", "未着手"))
	// 発注書なしの製造依頼（電話のメモから人が作った受注ページ——発注書番号が空）。
	addPage(t, 943, 941, "受注", "alice", "302", true)
	body := `<h1>受注</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerOrder + `</dd>` +
		`<dt>発注書番号</dt><dd><br/></dd><dt>発注元</dt><dd>やまと工作所</dd><dt>納期</dt><dd>2026-11-05</dd></dl>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>受注明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>` +
		`<th>単価</th><th>納期</th><th>出荷済数量</th><th>備考</th><th>状態</th></tr>` +
		pricedItem("B-1", "電話で頼まれた", "1", "", "", "未着手") + `</tbody></table>`
	if err := cms.SyncIndex(page.FormatID(943), body); err != nil {
		t.Fatal(err)
	}

	out := backlogViewHTML(adminUser(), 941)
	if !strings.Contains(out, `<th class="no-print">まだ無いもの</th>`) {
		t.Fatalf("「まだ無いもの」の欄が無い（か、紙に出す形）:\n%s", out)
	}
	gapOf := func(item string) string {
		i := strings.Index(out, `data-order-item="`+item+`"`)
		if i < 0 {
			t.Fatalf("%s の行がありません:\n%s", item, out)
		}
		row := out[i:]
		row = row[:strings.Index(row, `</tr>`)]
		j := strings.Index(row, `backlog-gap">`)
		if j < 0 {
			t.Fatalf("%s の行に「まだ無いもの」のセルがありません:\n%s", item, row)
		}
		rest := row[j+len(`backlog-gap">`):]
		return rest[:strings.Index(rest, `</td>`)]
	}
	for item, want := range map[string]string{"A-1": "", "A-2": "単価", "B-1": "発注書番号・単価"} {
		if got := gapOf(item); got != want {
			t.Errorf("⚠ %s の「まだ無いもの」が %q です（%q を期待）", item, got, want)
		}
	}
}
