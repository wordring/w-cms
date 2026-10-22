package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 見積表の単価の式をテンプレート「見積計算書」に置く（2026-10-19・estimate.go の templateRecordFormulas）。
// 利用者:「この式をテンプレートに書いておく方式はどうでしょう？」「出来るだけハードコーディングを無くしたいです」

const unitFormula = "=ROUND(原価*(1+利益率/100))"

// withCalcTemplate はテンプレート「見積計算書」（setup_test.go の /000917）に、見積もり計算の節（工程2行と、単価に式のある見積表）を置きます。
func withCalcTemplate(t *testing.T) {
	t.Helper()
	writeBodyFile(t, 917, "<h1>"+EstimateCalcTemplate+`</h1><dl data-type="tags"><dt>`+OurItemNoTag+`</dt><dd><br/></dd><dt>`+
		EstimateCalcDateTag+`</dt><dd><br/></dd><dt>`+PageKindTag+`</dt><dd>`+PageKindOurEstimateCalc+`</dd></dl>`+
		`<h2>見積もり計算</h2>`+calcTable([2]string{"材料", ""}, [2]string{"塗装", ""})+recordTable("", "", "", "", unitFormula, ""))
}

// TestEstimateRecordUsesTemplateFormula は、
//   - 📌 で見積表を作るとき・数の単価を記録し直すとき、テンプレートの式を単価に入れる（原価と利益率は数）
//   - 単価が式なら、📌 は式を残す・⧉ の写しも式を残す
//   - 読む側は計算した値（ROUND で円に丸めた値）を読む
//
// ことを固定します。
func TestEstimateRecordUsesTemplateFormula(t *testing.T) {
	setupExtTest(t, "000030", page.PageMeta{Owner: "root", Mode: "330"})
	withCalcTemplate(t)

	// 見積表の無い組——📌 で作ると、単価はテンプレートの式。
	body := `<h2>見積もり計算</h2><h3>12個</h3>` + calcTable([2]string{"材料", "62727"})
	got, err := withEstimateRecord(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<td>"+unitFormula+"</td>") {
		t.Errorf("⚠ 作った見積表の単価がテンプレートの式ではありません:\n%s", got)
	}
	if r := estimateRecordOf(tablesOfType(parseBody(t, got), estimateRecordType)[0]); r.Cost != "62727" || r.Rate != "10" || r.Unit != "69000" {
		t.Errorf("⚠ 読んだ記録が違います（原価 62727・利益率 10・単価は式の値 69000）: %+v", r)
	}
	// 数の単価の見積表——📌 で記録し直すと、式になる。
	body = `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "1000"}) + recordTable("1", "", "", "20", "999", "")
	got, err = withEstimateRecord(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<td>"+unitFormula+"</td>") || strings.Contains(got, "<td>999</td>") {
		t.Errorf("⚠ 数の単価がテンプレートの式に替わっていません:\n%s", got)
	}
	// 式の単価——📌 は式を残す（テンプレートと違う式でも、人が書いた式を上書きしない）。
	mine := "=原価*2"
	body = `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "1000"}) + recordTable("1", "", "", "20", mine, "")
	if got, err = withEstimateRecord(body, 0); err != nil || !strings.Contains(got, "<td>"+mine+"</td>") {
		t.Errorf("⚠ 人が書いた単価の式を上書きしました（%v）:\n%s", err, got)
	}
	// ⧉ の写しも式を残す。
	body = `<h2>見積もり計算</h2><h3>1個</h3>` + calcTable([2]string{"材料", "1000"}) + recordTable("1", "", "1000", "10", unitFormula, "")
	got, err = withEstimateDuplicate(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "<td>"+unitFormula+"</td>") != 2 {
		t.Errorf("⚠ 写しの見積表の単価の式が消えました:\n%s", got)
	}
}

// TestEstimateCalcNewUsesTemplateSection は、写す中身の無い品物の新しい見積計算書が、テンプレートの「見積もり計算」の節
// （工程の行と単価の式）で始まり、節が二重にならないことを固定します。
func TestEstimateCalcNewUsesTemplateSection(t *testing.T) {
	setupExtTest(t, "000030", page.PageMeta{Owner: "root", Mode: "330"})
	withCalcTemplate(t)
	addPage(t, 31, -1, "加工製品", "root", "302", true)
	body := `<h1>カバー</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl><p>見積もり計算の節の無い品物</p>`
	writeBodyFile(t, 31, body)
	syncBody(t, 31, body)
	code, out := postRFQ(t, adminUser(), EstimateCalcNewAPIHandler, map[string]any{"product": "000031"})
	if code != 200 {
		t.Fatalf("見積計算書を作れません: %d %v", code, out)
	}
	got := readPageBody(t, out["page_id"].(string))
	if strings.Count(got, "<h2>見積もり計算</h2>") != 1 || strings.Count(got, unitFormula) != 1 || !strings.Contains(got, "<td>塗装</td>") {
		t.Errorf("⚠ 新しい見積計算書が、テンプレートの節（1つだけ）で始まっていません:\n%s", got)
	}
}
