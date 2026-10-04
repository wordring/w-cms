package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestEstimateImportPastPrice は、過去の販売価格を見積書ページへ移す口（2026-10-04・estimate_import.go）を固定します——
// 管理者だけ・見積先は加工製品の客先・置き場はその日の年月・見積日も送付日もその日（未送付に並ばない）・品番と品名は加工製品から・
// 備考の欄に note。利用者:「顧客向けの見積書ページとして残してください」。
func TestEstimateImportPastPrice(t *testing.T) {
	const product = "000021"
	setupExtTest(t, product, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, product, `<h1>カバー</h1><dl data-type="tags"><dt>品番</dt><dd>K-1</dd><dt>品名</dt><dd>カバー</dd>`+
		`<dt>`+ClientNameTag+`</dt><dd>みなと商店</dd></dl>`)
	in := map[string]any{"date": "2026-01-29", "note": "過去の販売価格（ワンノートから移した）",
		"lines": []map[string]string{{"product_id": product, "quantity": "20", "unit": "個", "price": "9580", "note": "元: 2026-01-29 ロット20単価9580円"}}}
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, EstimateImportAPIHandler, in); code != 403 {
		t.Errorf("管理者でない人が移せてしまう: %d", code)
	}
	root := &auth.User{Username: "root", IsAdmin: true}
	code, out := postRFQ(t, root, EstimateImportAPIHandler, in)
	id, _ := out["page_id"].(string)
	if code != 200 || id == "" {
		t.Fatalf("移せません: %d %v", code, out)
	}
	body := readPageBody(t, id)
	for _, want := range []string{"<h1>見積　みなと商店</h1>", "<dt>" + EstimateDateTag + "</dt><dd>2026-01-29</dd>",
		"<dt>" + EstimateSentTag + "</dt><dd>2026-01-29</dd>", ">K-1<", ">カバー<", ">20<", ">9580<", ">個<",
		"元: 2026-01-29 ロット20単価9580円", "<p>過去の販売価格（ワンノートから移した）</p>"} {
		if !strings.Contains(body, want) {
			t.Errorf("移した見積書ページに %q がありません:\n%s", want, body)
		}
	}
	meta, _ := page.ReadSidecar(id)
	month, _ := page.ReadSidecar(meta.ParentID)
	if cms.PageTitleByID(pageNum(meta.ParentID)) != "01月" || cms.PageTitleByID(pageNum(month.ParentID)) != "2026年" {
		t.Errorf("置き場がその日の年月ではありません: %s ← %s", cms.PageTitleByID(pageNum(meta.ParentID)), cms.PageTitleByID(pageNum(month.ParentID)))
	}
	list, err := unsentEstimates(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if page.FormatID(e.PageID) == id {
			t.Errorf("出した値段の見積書が未送付に並んでいます: %s", id)
		}
	}
	// 見積先を渡せばそれ（加工製品の客先より先）。
	in["client"] = "かなめ商会"
	if _, out := postRFQ(t, root, EstimateImportAPIHandler, in); !strings.Contains(readPageBody(t, out["page_id"].(string)), "<h1>見積　かなめ商会</h1>") {
		t.Errorf("渡した見積先になっていません")
	}
	if code, _ := postRFQ(t, root, EstimateImportAPIHandler, map[string]any{"date": "2026/01/29", "lines": in["lines"]}); code != 400 {
		t.Errorf("日付の形が違うのに %d", code)
	}
	if code, _ := postRFQ(t, root, EstimateImportAPIHandler, map[string]any{"date": "2026-01-29",
		"lines": []map[string]string{{"product_id": "000999", "price": "1"}}}); code != 400 {
		t.Errorf("無い加工製品ページなのに %d", code)
	}
}
