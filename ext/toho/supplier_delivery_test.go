package toho

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 業者の納品書から納品済の部材発注書を作り（supplier_delivery.go）、行を加工製品の部材の行に結び（order_bind.go）、
// 外注加工表に最新単価が出る（part_price.go）までを、通して固定します（2026-10-23）。
//
//   - 🤖 解析が "supplier_delivery" と答えると、部材発注書（行は納品済・単価は納品書のとおり・受信元つき）ができる
//   - 同じ納品書からは2枚目を作らない
//   - 結ぶ相手の候補は、寸法の数が重なる外注加工の行が先頭に来る
//   - 結ぶと、行は外注加工の行と同じ値（弊社品番・種類・番号・加工内容）になり、もとの品名・寸法は備考に残る
//   - 加工製品ページの外注加工表に、その単価・納品日・仕入先が「最新単価」として出る
func TestSupplierDeliveryToBoundPartPrice(t *testing.T) {
	const host = "000012"
	setupExtTest(t, host, page.PageMeta{Owner: "alice", Mode: "330"})
	addPage(t, 31, 0, "据付ブラケット", "root", "302", true)
	product := `<h1>据付ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		`<table><caption>外注加工表</caption><tbody><tr><th>番号</th><th>加工部品番号</th><th>加工内容</th><th>材質</th><th>個数</th><th>推奨業者</th></tr>` +
		`<tr><td>1</td><td>t9*199*208.5 φ68</td><td>溶断</td><td>SS</td><td>1</td><td>みなと商店</td></tr>` +
		`<tr><td>2</td><td>t9*50*60</td><td>曲げ</td><td>SS</td><td>1</td><td></td></tr></tbody></table>`
	writeBodyFile(t, 31, product)
	syncBody(t, 31, product)
	putAttachment(t, host, "e7np.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{DocType: "supplier_delivery", SupplierDelivery: &supplierDeliveryJudgment{
			Supplier: "株式会社みなと商店", DeliveryNo: "N-1", Date: "2026-10-06", Note: "受注日 10/1",
			Items: []supplierDeliveryItem{
				{ItemName: "型切", Size: "9x199x208.5 68Φ", Quantity: "3", Price: "940"},
				{ItemName: "寸法切", Size: "9x120x173.5", Quantity: "3", Price: "380"},
				{}, // 空の行は入れない
			}}}, nil
	})
	u := adminUser()
	rr := postAnalyze(t, u, map[string]string{"page_id": host, "file": "e7np.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var res struct {
		DocType string `json:"doc_type"`
		PageID  string `json:"page_id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if res.DocType != "supplier_delivery" || res.PageID == "" {
		t.Fatalf("応答が違います: %s", rr.Body.String())
	}
	po := res.PageID
	body := readPageBody(t, po)
	for _, want := range []string{
		"<dt>" + SourceRefTag + "</dt><dd>" + host + "-e7np</dd>",
		"<dt>" + DeliveredAtTag + "</dt><dd>2026-10-06</dd>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindOurPurchase + "</dd>",
		`data-ref="` + host + `-e7np"`,
		"受注日 10/1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 部材発注書に %q がありません:\n%s", want, body)
		}
	}
	if n := strings.Count(body, ">"+OrderLineDelivered+"<"); n != 2 {
		t.Errorf("⚠ 納品済の行が %d 行です（2行のはず。空の行は入れない）:\n%s", n, body)
	}
	// 同じ納品書から2枚目は作らない。
	if rr := postAnalyze(t, u, map[string]string{"page_id": host, "file": "e7np.pdf"}); rr.Code != 409 || !strings.Contains(rr.Body.String(), po) {
		t.Errorf("⚠ 同じ納品書から2枚目を作ろうとしました: %d %s", rr.Code, rr.Body.String())
	}

	// 結ぶ相手の候補——1行目（型切 9x199x208.5 68Φ）には、溶断の行が先頭。
	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+po, nil), u), pageNum(po), body)
	m := regexp.MustCompile(`class="order-pick"[^>]*data-row="1" data-sig="([0-9a-f]+)"`).FindStringSubmatch(view)
	if m == nil || !strings.Contains(view, "order-bind-open") {
		t.Fatalf("⚠ 行の指紋か「🔗 加工製品の行に結ぶ」がありません:\n%s", view)
	}
	req := auth.WithUser(httptest.NewRequest("GET", "/api/our-order/bind-candidates?page_id="+po+"&row=1&sig="+m[1], nil), u)
	cr := httptest.NewRecorder()
	OrderBindCandidatesAPIHandler(cr, req)
	var cands struct {
		Candidates []bindCandidate `json:"candidates"`
	}
	json.Unmarshal(cr.Body.Bytes(), &cands)
	if cr.Code != 200 || len(cands.Candidates) == 0 || !strings.Contains(cands.Candidates[0].Label, "溶断") || cands.Candidates[0].ProductID != "000031" {
		t.Fatalf("⚠ 候補の先頭が溶断の行ではありません: %d %s", cr.Code, cr.Body.String())
	}
	// 結ぶ。
	if code, out := postRFQ(t, u, OrderBindAPIHandler, map[string]any{"page_id": po, "row": 1, "sig": m[1],
		"product_id": cands.Candidates[0].ProductID, "key": cands.Candidates[0].Key}); code != 200 {
		t.Fatalf("結べません: %d %v", code, out)
	}
	body = readPageBody(t, po)
	for _, want := range []string{"<td>000031</td>", "<td>外注加工</td>", "<td>溶断</td>", "<td>t9*199*208.5 φ68</td>", "納品書の記載: 型切 9x199x208.5 68φ"} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 結んだ行に %q がありません:\n%s", want, body)
		}
	}
	// 古い指紋では結べない（ほかの人が直したあと）。
	if code, _ := postRFQ(t, u, OrderBindAPIHandler, map[string]any{"page_id": po, "row": 1, "sig": m[1],
		"product_id": "000031", "key": cands.Candidates[0].Key}); code != 409 {
		t.Errorf("⚠ 中身の変わった行を古い指紋で結べました: %d", code)
	}

	// 外注加工表の最新単価——溶断の行に 940円・2026-10-06・仕入先。曲げの行は空欄。
	pv := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000031", nil), u), 31, readPageBody(t, "000031"))
	at := strings.Index(pv, "<caption>外注加工表</caption>")
	if at < 0 {
		t.Fatalf("外注加工表がありません:\n%s", pv)
	}
	tbl := pv[at : at+strings.Index(pv[at:], "</table>")]
	if !strings.Contains(tbl, "最新単価") || !strings.Contains(tbl, "940円") || !strings.Contains(tbl, "2026-10-06") {
		t.Errorf("⚠ 外注加工表に最新単価 940円（2026-10-06）が出ていません:\n%s", tbl)
	}
	if strings.Count(tbl, "940円") != 1 {
		t.Errorf("⚠ 結んでいない行にも単価が出ています:\n%s", tbl)
	}
}
