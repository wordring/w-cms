package toho

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 見積計算表と見積表（2026-10-15・estimate.go）——利用者:「見積もり計算表に総計は必要ないです」「ロットごとに計算表を
// 分けてはどうでしょう？」「提案通りの構成でお願いします」。

// calcTable は見積計算表を1枚組みます（行は「工程・価格」）。
func calcTable(rows ...[2]string) string {
	b := `<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr>`
	for _, r := range rows {
		b += `<tr><td>` + r[0] + `</td><td>` + r[1] + `</td><td></td></tr>`
	}
	return b + `</tbody></table>`
}

// recordTable は見積表を1枚組みます（ロット・表面処理・原価・利益率・単価・備考の1行）。
func recordTable(cells ...string) string {
	b := `<table><caption>見積表</caption><tbody><tr><th>個数</th><th>表面処理</th><th>原価</th><th>利益率</th><th>単価</th><th>備考</th></tr><tr>`
	for i := 0; i < 6; i++ {
		v := ""
		if i < len(cells) {
			v = cells[i]
		}
		b += `<td>` + v + `</td>`
	}
	return b + `</tr></tbody></table>`
}

func parseBody(t *testing.T, body string) []*html.Node {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

// TestEstimateBlocksPairInOrder は、見積表がすぐ上の見積計算表と組になることを固定します（文書の順・見積計算表の無い見積表は1組）。
func TestEstimateBlocksPairInOrder(t *testing.T) {
	body := `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "100"}) + recordTable("1") +
		`<h3>20個</h3>` + calcTable([2]string{"材料", "80"}) + // 見積表がまだ無い
		`<section><h3>40個</h3>` + calcTable([2]string{"材料", "70"}) + recordTable("40") + `</section>` +
		recordTable("99") // 計算表の無い見積表
	b := estimateBlocksOf(parseBody(t, body))
	if len(b) != 4 {
		t.Fatalf("組の数が %d（4のはず）", len(b))
	}
	lot := func(i int) string {
		if b[i].Record == nil {
			return "-"
		}
		return estimateRecordOf(b[i].Record).Lot
	}
	got := []string{lot(0), lot(1), lot(2), lot(3)}
	if strings.Join(got, ",") != "1,-,40,99" || b[3].Calc != nil {
		t.Errorf("組が違います: %v", got)
	}
	if blockOfRecord(b, 2) != 3 || blockOfCalc(b, 1) != 1 || blockOfCalc(b, 3) != -1 {
		t.Errorf("番号の引き方が違います")
	}
}

// TestEstimateCostAndPlan は原価（価格の列の合計・空は飛ばす・式は計算した値）と、単価＝原価＋利益（四捨五入）を固定します。
func TestEstimateCostAndPlan(t *testing.T) {
	nodes := parseBody(t, calcTable([2]string{"材料", "1,200円"}, [2]string{"ブランク", ""}, [2]string{"塗装", "=材料/4"}))
	c := estimateCostOf(tablesOfType(nodes, estimateType)[0])
	if !c.OK || c.Cost != 1500 {
		t.Fatalf("原価が違います: %+v", c)
	}
	// 数でない価格は理由を言う（黙って飛ばさない）。
	bad := parseBody(t, calcTable([2]string{"材料", "100"}, [2]string{"塗装", "7000（8400）"}))
	if c := estimateCostOf(tablesOfType(bad, estimateType)[0]); c.OK || !strings.Contains(c.Why, "「塗装」の価格が数ではありません") {
		t.Errorf("数でない価格の理由が違います: %+v", c)
	}
	// 価格が全部空なら計算しない。
	empty := parseBody(t, calcTable([2]string{"材料", ""}))
	if c := estimateCostOf(tablesOfType(empty, estimateType)[0]); c.OK {
		t.Errorf("価格が空なのに計算しました: %+v", c)
	}

	// 利益率は見積表の欄（12.5%）——365 × 12.5% = 45.625 → 46、単価 411。
	blocks := estimateBlocksOf(parseBody(t, calcTable([2]string{"材料", "300"}, [2]string{"板金", "65"})+recordTable("20", "", "", "12.5")))
	p := estimatePlanOf(blocks[0])
	if !p.OK || p.Cost != 365 || p.Rate != 12.5 || p.RateDefault || p.Profit != 46 || p.Unit != 411 {
		t.Errorf("計算が違います: %+v", p)
	}
	if p.matches(estimateRecordOf(blocks[0].Record)) {
		t.Errorf("まだ記録していないのに合っているとしました")
	}
	if !p.matches(estimateRecord{Cost: "365", Rate: "12.5", Unit: "411"}) || p.matches(estimateRecord{Cost: "365", Rate: "12.5", Unit: "410"}) {
		t.Errorf("記録との突き合わせが違います")
	}
	if p := estimatePlanOf(estimateBlock{Record: blocks[0].Record}); p.OK || !strings.Contains(p.Why, "見積計算表がありません") {
		t.Errorf("計算表の無い見積表の理由が違います: %+v", p)
	}
}

// TestWithEstimateRecord は「📌 この単価を記録」を固定します——見積表があればその行へ、無ければ見積計算表のすぐ後ろに作る。
func TestWithEstimateRecord(t *testing.T) {
	setupExtTest(t, "000030", page.PageMeta{Owner: "root", Mode: "330"})
	body := `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "1000"}) + recordTable("1", "塗装あり", "", "20", "999", "メモ") +
		`<h3>20個</h3>` + calcTable([2]string{"材料", "900"}) + `<p>あと</p>`
	got, err := withEstimateRecord(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := estimateRecordOf(tablesOfType(parseBody(t, got), estimateRecordType)[0])
	if r.Lot != "1" || r.Surface != "塗装あり" || r.Cost != "1000" || r.Rate != "20" || r.Unit != "1200" || r.Note != "メモ" {
		t.Errorf("記録が違います（利益率は見積表の 20%%・ロットと表面処理と備考は残す）: %+v", r)
	}
	// 見積表の無い組——見積計算表のすぐ後ろ（「あと」の段落より前）に作る。利益率は既定（10%）。
	got, err = withEstimateRecord(got, 1)
	if err != nil {
		t.Fatal(err)
	}
	recs := tablesOfType(parseBody(t, got), estimateRecordType)
	if len(recs) != 2 {
		t.Fatalf("見積表が作られていません:\n%s", got)
	}
	if r := estimateRecordOf(recs[1]); r.Cost != "900" || r.Rate != "10" || r.Unit != "990" || r.Lot != "" {
		t.Errorf("作った見積表の記録が違います: %+v", r)
	}
	if strings.Index(got, "<caption>見積表</caption><tbody><tr><th>個数") > strings.LastIndex(got, "<p>あと</p>") {
		t.Errorf("見積表が見積計算表のすぐ後ろにありません:\n%s", got)
	}
	// 計算できない組は理由を返す。
	if _, err := withEstimateRecord(calcTable([2]string{"材料", ""}), 0); err == nil || !strings.Contains(err.Error(), "記録できません") {
		t.Errorf("計算できない組を記録しました: %v", err)
	}
	if _, err := withEstimateRecord(body, 5); err == nil {
		t.Errorf("無い組に書きました")
	}
}

// TestWithEstimateDuplicate は「⧉ このロットを写す」を固定します——見出し・見積計算表・見積表を写して組のすぐ後ろへ。
// ロット・原価・単価は空にし、data-id は写さない。
func TestWithEstimateDuplicate(t *testing.T) {
	body := `<h2>見積もり計算</h2><h3>1個</h3>` +
		`<table data-id="ab12"><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr>` +
		`<tr data-id="cd34"><td>材料</td><td>100</td><td>板</td></tr></tbody></table>` +
		recordTable("1", "黒", "100", "10", "110", "メモ") + `<h3>20個</h3>` + calcTable([2]string{"材料", "90"})
	got, err := withEstimateDuplicate(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	nodes := parseBody(t, got)
	if n := len(tablesOfType(nodes, estimateType)); n != 3 {
		t.Fatalf("見積計算表が %d 枚（3のはず）:\n%s", n, got)
	}
	recs := tablesOfType(nodes, estimateRecordType)
	if r := estimateRecordOf(recs[1]); r.Lot != "" || r.Cost != "" || r.Unit != "" || r.Surface != "黒" || r.Rate != "10" || r.Note != "メモ" {
		t.Errorf("写しの見積表が違います: %+v", r)
	}
	if strings.Count(got, `data-id="ab12"`) != 1 || strings.Count(got, `data-id="cd34"`) != 1 {
		t.Errorf("data-id を写しました:\n%s", got)
	}
	i1, i2 := strings.Index(got, "1個（写し）"), strings.Index(got, "<h3>20個</h3>")
	if i1 < 0 || i1 > i2 {
		t.Errorf("写しの見出しが組のすぐ後ろにありません:\n%s", got)
	}
	if strings.Count(got, "<td>板</td>") != 2 {
		t.Errorf("見積計算表の中身を写していません:\n%s", got)
	}
}

// TestEstimateRecordFollowsSig は、「📌 この単価を記録」を押す前に、ほかの人が前の組を「⧉ このロットを写す」で写しても、
// 押した組（描いたときの指紋の合う組）に記録することを固定します（2026-10-22 の見直し）。それまでは番号だけで選んでいて、
// 写しの組（別のロット）に書いていた。
func TestEstimateRecordFollowsSig(t *testing.T) {
	setupExtTest(t, "000030", page.PageMeta{Owner: "root", Mode: "330"})
	body := `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "1000"}) + recordTable("1", "", "1000", "20", "1200", "") +
		`<h3>20個</h3>` + calcTable([2]string{"材料", "900"})
	blocks := estimateBlocksOf(parseBody(t, body))
	sig20 := estimateBlockSig(blocks[1])
	moved, err := withEstimateDuplicate(body, 0) // ほかの人が写した——ロット20の組は 2 番目→3 番目
	if err != nil {
		t.Fatal(err)
	}
	writeBodyFile(t, 30, moved)
	u := &auth.User{Username: "root", IsAdmin: true}
	if code, out := postRFQ(t, u, EstimateRecordAPIHandler, map[string]any{"page_id": "000030", "block": 1, "sig": sig20}); code != 200 {
		t.Fatalf("記録できません: %d %v", code, out)
	}
	recs := tablesOfType(parseBody(t, readPageBody(t, "000030")), estimateRecordType)
	if len(recs) != 3 {
		t.Fatalf("見積表が %d 枚（3のはず）", len(recs))
	}
	if r := estimateRecordOf(recs[1]); r.Cost != "" {
		t.Errorf("⚠ 写しの組（番号の合う組）に記録しました: %+v", r)
	}
	if r := estimateRecordOf(recs[2]); r.Cost != "900" {
		t.Errorf("⚠ ロット20の組に記録していません: %+v", r)
	}
	if code, _ := postRFQ(t, u, EstimateRecordAPIHandler, map[string]any{"page_id": "000030", "block": 1, "sig": "x/y"}); code == 200 {
		t.Errorf("指紋の合う組が無いのに記録しました")
	}
}
