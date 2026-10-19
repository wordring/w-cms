package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestQuotesPutButtonFollowsEstimateCalcDoc は、「💴 見積回答」の「計算表へ」が、写す先（いちばん新しい見積計算書）に
// 見積計算表があれば、加工製品ページそのものに見積計算表が無くても出ることを固定します（2026-10-19）。
//
// 2026-10-19 までは加工製品ページそのものを見ていたので、見積計算表を見積計算書へ移した加工製品ページ（職場の 23枚・10-17）では、
// 「計算表へ」が出ませんでした（写す口 /api/estimate/rows は見積計算書を読むのに）。見積計算表がどこにも無ければ、出しません。
func TestQuotesPutButtonFollowsEstimateCalcDoc(t *testing.T) {
	setupExtTest(t, "000040", page.PageMeta{Owner: "root", Mode: "330"})
	root := &auth.User{Username: "root", IsAdmin: true}
	// 加工製品 000041 の見積回答（業者の単価）。
	if code, out := postRFQ(t, root, RFQImportAPIHandler, map[string]any{"supplier": "ふじ鍍金", "date": "2026-08-07",
		"lines": []map[string]string{{"product_id": "000041", "kind": "外注加工", "work": "塗装", "quantity": "20", "cost": "800"}}}); code != 200 {
		t.Fatalf("部材見積依頼書を移せません: %d %v", code, out)
	}
	show := func() string {
		req := auth.WithUser(httptest.NewRequest("GET", "/000041", nil), root)
		return cms.RenderComputedViews(req, 41, productWithSupplied)
	}
	if got := show(); !strings.Contains(got, "800円") || strings.Contains(got, ">計算表へ<") {
		t.Fatalf("見積計算表がどこにも無いのに「計算表へ」が出ているか、見積回答が出ていません:\n%s", got)
	}
	// 見積計算書（弊社品番 000041）に見積計算表を置く——加工製品ページそのものには無いまま。
	addPage(t, 70, 0, "【弊社】見積計算書　カバー", "root", "302", true)
	writeBodyFile(t, 70, `<h1>【弊社】見積計算書　カバー</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurEstimateCalc+
		`</dd><dt>`+OurItemNoTag+`</dt><dd>000041</dd></dl>`+
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr><tr><td>材料</td><td>620</td><td></td></tr></tbody></table>`)
	if got := show(); !strings.Contains(got, ">計算表へ<") {
		t.Errorf("見積計算書に見積計算表があるのに「計算表へ」が出ていません:\n%s", got)
	}
}
