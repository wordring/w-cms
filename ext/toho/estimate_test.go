package toho

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// estimateTable は見積計算表を1枚組みます（行は「工程・数・単位」）。
func estimateTable(rows ...[3]string) string {
	b := `<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>`
	for _, r := range rows {
		b += `<tr><td>` + r[0] + `</td><td>` + r[1] + `</td><td>` + r[2] + `</td><td></td></tr>`
	}
	return b + `</tbody></table>`
}

func firstTable(t *testing.T, body string) *html.Node {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	ts := tablesOfType(nodes, estimateType)
	if len(ts) == 0 {
		t.Fatalf("見積計算表として読めません:\n%s", body)
	}
	return ts[0]
}

// TestEstimateOfComputesFinalPrice は確定単価の計算を固定します（2026-10-01 利用者:「弊社利益はデフォルトで10％なので、
// 単価に1.1をかければ良いのですが、利益率を変更できるようにしたいです」）。
//
//	単価の行がある → 単価 × 率（四捨五入）を足す／単価の行が空 → 円の行を足して単価に／「弊社利益」の行があればその率
func TestEstimateOfComputesFinalPrice(t *testing.T) {
	r := estimateOf(firstTable(t, estimateTable(
		[3]string{"ロット", "20", "個"}, [3]string{"材料", "55", "円"}, [3]string{"板金", "310", "円"},
		[3]string{"単価", "365", "円"}, [3]string{"総計", "", "円"})), 10, true)
	if !r.OK || r.UnitPrice != 365 || r.Summed || r.RateSet || r.Profit != 37 || r.Final != 402 {
		t.Errorf("単価の行から: %+v（利益 37・確定 402 のはず——36.5 は四捨五入）", r)
	}

	r = estimateOf(firstTable(t, estimateTable(
		[3]string{"ロット", "1", "個"}, [3]string{"材料", "620", "円"}, [3]string{"ブランク", "1,500", "円"},
		[3]string{"単価", "", "円"}, [3]string{"総計", "4920", "円"})), 10, true)
	if !r.OK || r.UnitPrice != 2120 || !r.Summed || r.Final != 2332 {
		t.Errorf("単価が空なら円の行（ロット・総計は除く）を足す: %+v", r)
	}

	r = estimateOf(firstTable(t, estimateTable(
		[3]string{"単価", "1000", "円"}, [3]string{"弊社利益", "15", "%"})), 10, true)
	if !r.OK || !r.RateSet || r.Rate != 15 || r.Profit != 150 || r.Final != 1150 {
		t.Errorf("表の「弊社利益」の行の率を使う: %+v", r)
	}

	if r := estimateOf(firstTable(t, estimateTable([3]string{"ロット", "10", "個"}, [3]string{"単価", "", "円"})), 10, true); r.OK {
		t.Errorf("単価が無いのに計算しています: %+v", r)
	}
	if r := estimateOf(firstTable(t, estimateTable([3]string{"単価", "100", "円"})), 0, false); r.OK || !strings.Contains(r.Why, "estimate_profit_rate") {
		t.Errorf("既定の率が無いときはそう言うはず: %+v", r)
	}
	odd := `<table><caption>見積計算表</caption><tbody><tr><th>品名</th><th>ロット20</th><th>ロット40</th></tr>` +
		`<tr><td>カバー</td><td>1200</td><td>1000</td></tr></tbody></table>`
	if r := estimateOf(firstTable(t, odd), 10, true); r.OK || !strings.Contains(r.Why, "計算できません") {
		t.Errorf("列の違う表は計算できないと言うはず: %+v", r)
	}
}

// TestWithEstimateRateWritesRow は、率を書く口が「弊社利益」の行を直し（無ければ末尾に足し）、2枚目の表を
// 取り違えないことを固定します（表は複数ある——塗装あり・なし、ロットごと）。
func TestWithEstimateRateWritesRow(t *testing.T) {
	body := `<h1>品</h1>` + estimateTable([3]string{"単価", "1000", "円"}) +
		estimateTable([3]string{"単価", "2000", "円"}, [3]string{"弊社利益", "10", "%"})
	got, err := withEstimateRate(body, 1, 12.5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "<td>弊社利益</td>") != 1 || !strings.Contains(got, "<td>弊社利益</td><td>12.5</td>") {
		t.Errorf("2枚目の表の行を直していません:\n%s", got)
	}
	got, err = withEstimateRate(got, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(got, "</table>", 2)[0]
	if !strings.Contains(first, "<tr><td>弊社利益</td><td>8</td><td>%</td><td></td></tr>") {
		t.Errorf("1枚目の表の末尾に行を足していません:\n%s", first)
	}
	if _, err := withEstimateRate(body, 2, 10); err == nil {
		t.Error("無い表へ書けたと言っています")
	}
}
