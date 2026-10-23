package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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

// TestEstimateCalcNewAddsTemplateRecord は（2026-10-23）、
//   - 写す中身（加工製品ページの「見積もり計算」の節）に見積表が無ければ、見積計算表のすぐ後ろにテンプレートの見積表（単価の式つき）を足す
//   - 見積表がある組には足さない
//   - 見積計算表の足元に「価格の合計」が出る（見積表があってもなくても）
//
// ことを固定します。
// > 2026-10-23 利用者:「見積表の単価に=原価*(1+利益率/100)と入れています。これをテンプレートに入れて欲しいです。出来ますか？」
// > 2026-10-23 利用者:「【弊社】見積計算書の見積もり計算表の下に価格の合計を入れてください。」
func TestEstimateCalcNewAddsTemplateRecord(t *testing.T) {
	setupExtTest(t, "000800", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withCalcTemplate(t)
	u := adminUser()
	addPage(t, 801, -1, CustomerBoxTitle, "alice", "302", true)
	productBody := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		`<h2>` + estimateSectionHeading + `</h2><h3>2個</h3>` + calcTable([2]string{"材料", "980"}, [2]string{"塗装", "1000"}) +
		`<h3>10個</h3>` + calcTable([2]string{"材料", "900"}) + recordTable("10", "", "900", "10", "990", "")
	addPage(t, 802, 801, "ブラケット", "alice", "302", true)
	writeBodyFile(t, 802, productBody)
	if err := cms.SyncIndex("000802", productBody); err != nil {
		t.Fatal(err)
	}
	code, out := postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": "000802"})
	if code != 200 || out["page_id"] == nil {
		t.Fatalf("作れません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body := readPageBody(t, id)
	if n := strings.Count(body, "<caption>見積表</caption>"); n != 2 {
		t.Errorf("⚠ 見積表が %d 枚です（2個の組に1枚足して、10個の組の1枚と合わせて2枚のはず）:\n%s", n, body)
	}
	if n := strings.Count(body, unitFormula); n != 1 {
		t.Errorf("⚠ テンプレートの単価の式が %d 個です（足した見積表に1個のはず）:\n%s", n, body)
	}
	if i, j := strings.Index(body, "<h3>2個</h3>"), strings.Index(body, unitFormula); j < i || j > strings.Index(body, "<h3>10個</h3>") {
		t.Errorf("⚠ 足した見積表が2個の組の見積計算表のすぐ後ろにありません:\n%s", body)
	}
	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), u), pageNum(id), body)
	for _, want := range []string{"価格の合計: 1,980円", "価格の合計: 900円"} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 見積計算表の足元に %q がありません:\n%s", want, view)
		}
	}
	if strings.Contains(readPageBody(t, id), "価格の合計") {
		t.Errorf("⚠ 価格の合計が本文に書かれました")
	}
}
