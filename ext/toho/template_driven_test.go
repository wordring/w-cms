package toho

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 機械が作るページのテンプレート駆動（2026-09-27・テンプレート駆動の D）の番人です。
// 利用者:「テンプレートにはスラッシュメニューから表などの印を置き、コードはそれを埋めては
// どうでしょう？」——**形はテンプレート、値と行の数は機械**。

// 受注明細は**テンプレートの列の並び**に従い、見出しの言葉で値を合わせる。テンプレートに無い
// 列は値があれば右端へ足す。タグもテンプレートの並びのまま、無いものは末尾へ足す。
func TestOrderPageFollowsTemplateColumns(t *testing.T) {
	tmpl := `<h1>受注ページ</h1><dl data-type="tags"><dt>` + OrderClientTag + `</dt><dd><br/></dd></dl>` +
		`<table><caption>受注明細</caption><tbody>` +
		`<tr><th>品名</th><th>数量</th><th>弊社品番</th><th>状態</th><th>社内メモ</th></tr>` +
		`<tr><td>見本</td><td></td><td></td><td></td><td></td></tr></tbody></table>`
	j := &orderJudgment{IsClientOrder: true, OrderNo: "PO-1", Customer: "みなと商店", Items: []orderPDFItem{
		{ItemNo: "A100-B01-001", ItemName: "ブラケット", Quantity: "4"},
		{ItemNo: "A100-B01-002", ItemName: "台座", Quantity: "2"},
	}}
	body, err := buildOrderPageHTML(tmpl, "000001", "pdf001", j)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// タグ: テンプレートの 発注元 が先、無かった 発注書番号 などは末尾へ。
		`<dl data-type="tags"><dt>` + OrderClientTag + `</dt><dd>みなと商店</dd><dt>` + OrderNoTag + `</dt><dd>PO-1</dd>`,
		// 列: テンプレートの並び＋値のある無かった列（品番）を右端へ。見本の行は消える。
		`<tr><th>品名</th><th>数量</th><th>弊社品番</th><th>状態</th><th>社内メモ</th><th>品番</th></tr>` +
			`<tr><td>ブラケット</td><td>4</td><td></td><td>未着手</td><td></td><td>A100-B01-001</td></tr>` +
			`<tr><td>台座</td><td>2</td><td></td><td>未着手</td><td></td><td>A100-B01-002</td></tr></tbody>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "見本") {
		t.Errorf("見本の行が残っています:\n%s", body)
	}
	// 原本PDFの印・読んだままの枠が無いテンプレートでも作れる（飾りは無ければ出さない）。
	if strings.Contains(body, "file-view") {
		t.Errorf("テンプレートに無いファイル表示を足しました:\n%s", body)
	}

	// 受注明細の表が無いテンプレートでは作らない（明細の行き場が無い）。
	_, err = buildOrderPageHTML(`<h1>受注ページ</h1>`, "000001", "pdf001", j)
	var se *cms.TemplateSlotError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "受注明細") {
		t.Errorf("受注明細の表が無いのに作りました: %v", err)
	}
}

// 読んだままの枠は、読めた表を入れ、読めなければ枠ごと消す。
func TestOrderPageSourceTableGoesIntoTheFold(t *testing.T) {
	j := &orderJudgment{IsClientOrder: true, OrderNo: "PO-1"}
	j.SourceTable = orderSourceTable{Headers: []string{"図番", "数量"}, Rows: [][]string{{"K120-1", "3"}}}
	body := testOrderPage("000001", "pdf001", j)
	if !strings.Contains(body, `<details><summary>`+sourceTableCaption+`</summary><table><caption>`+sourceTableCaption+`</caption>`) {
		t.Errorf("読んだままの表が枠の中にありません:\n%s", body)
	}
	if !strings.Contains(body, `<section data-type="file-view" data-ref="000001-pdf001">`) {
		t.Errorf("原本PDFの印へ配線していません:\n%s", body)
	}
	j.SourceTable = orderSourceTable{}
	if body := testOrderPage("000001", "pdf001", j); strings.Contains(body, sourceTableCaption) {
		t.Errorf("読めなかったのに読んだままの枠が残っています:\n%s", body)
	}
}

// 加工製品は**図面ブロックを見出しで探す**——テンプレートで材料の節を図面より前に置いても、
// 改定の合流が運ぶのは図面ブロック。改訂明細の1版目は行にブロックIDがあり、改定で版2になる。
func TestProductPageFromTemplateWithMaterialsFirst(t *testing.T) {
	tmpl := `<h1>加工製品</h1><section><h2>材料</h2><table><caption>材料</caption><tbody><tr><th>材質</th></tr></tbody></table></section>` +
		`<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd><br/></dd></dl>` +
		`<section data-type="file-view" data-ref=""></section></section>` +
		`<table><caption>改訂明細</caption><tbody><tr><th>版</th><th>図面番号</th><th>受領日</th></tr>` +
		`<tr><td>1</td><td></td><td></td></tr></tbody></table>`
	j := &orderJudgment{DocType: "drawing", DrawingNo: "K120-1", DrawingName: "ブラケット", Customer: "みなと商店"}
	body, err := buildProductPageHTML(tmpl, "000001", "pdf001", j, []matchedDXF{{AttachID: "dxf001"}, {AttachID: "dxf002"}})
	if err != nil {
		t.Fatal(err)
	}
	blk := drawingBlockOf(body)
	for _, want := range []string{
		"<h2>図面</h2>", "<dt>図面番号</dt><dd>K120-1</dd>", "<dt>客先</dt><dd>みなと商店</dd>",
		"<dt>対応DXF</dt><dd>000001-dxf001</dd><dt>対応DXF</dt><dd>000001-dxf002</dd>",
		`data-ref="000001-pdf001"`,
	} {
		if !strings.Contains(blk, want) {
			t.Errorf("図面ブロックに %s がありません:\n%s", want, blk)
		}
	}
	if strings.Contains(blk, "材料") || !strings.HasPrefix(blk, `<section data-id="`) {
		t.Errorf("図面ブロックの取り出しが違います（最初の節を取っていないか・ブロックIDはあるか）:\n%s", blk)
	}
	if n := len(revisionRowRe.FindAllString(body, -1)); n != 1 {
		t.Errorf("改訂明細の1版目が数えられません（行のブロックID）: %d\n%s", n, body)
	}
	if !strings.Contains(InsertRevisionRow(body, "K120-1A"), "<td>2</td><td>K120-1A</td>") {
		t.Errorf("改定で版2になりません:\n%s", InsertRevisionRow(body, "K120-1A"))
	}
	// 図面ブロックの無いテンプレートでは作らない。
	if _, err := buildProductPageHTML(`<h1>加工製品</h1>`+emptyVocabTable(revisionItemsType), "000001", "pdf001", j, nil); err == nil {
		t.Error("図面ブロックの無いテンプレートで加工製品ページを作りました")
	}
}

// 発注書の備考は、書くときだけ「備考」の節が要る（紙に刷る言葉を黙って捨てない）。
func TestOurOrderNoteNeedsTheSection(t *testing.T) {
	tmpl := `<h1>発注書</h1>` + emptyVocabTable(ourOrderItemsType)
	lines := []ourOrderLine{{ProductID: "000080", Material: "鉄"}}
	if _, err := buildOurOrderHTML(tmpl, "000138", "みなと商店", "2026-09-27", "", "定尺で可", "", lines); err == nil {
		t.Error("備考の節が無いのに備考を捨てて作りました")
	}
	if _, err := buildOurOrderHTML(tmpl, "000138", "みなと商店", "2026-09-27", "", "", "", lines); err != nil {
		t.Errorf("備考が無ければ節は要らないはず: %v", err)
	}
}

// テンプレートが無ければ、**Gemini に聞く前に**断る（有料の問い合わせを無駄にしない）。
func TestAnalyzeRefusesWithoutTemplateBeforeAsking(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "abc123.pdf", []byte("%PDF-1.4 fake"))
	if _, err := cms.DeletePageToTrash("000907"); err != nil { // テンプレート「受注ページ」
		t.Fatal(err)
	}
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		t.Error("テンプレートが無いのに Gemini に聞きました")
		return sampleJudgment, nil
	})
	rr := postAnalyze(t, &auth.User{Username: "alice"}, map[string]string{"page_id": id, "file": "abc123.pdf"})
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), OrderPageTemplate) {
		t.Errorf("テンプレートが無いことを知らせていません: %d %s", rr.Code, rr.Body.String())
	}
}

// 発注書のテンプレートが無ければ、**ページを作る前に**断る（「作成中」のページを残さない）。
func TestNewOurOrderRefusesWithoutTemplateBeforeCreating(t *testing.T) {
	setupExtTest(t, "000012", page.PageMeta{Owner: "alice", Mode: "330"})
	if _, err := cms.DeletePageToTrash("000908"); err != nil { // テンプレート「発注書」
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"supplier": "みなと商店",
		"lines": []map[string]string{{"product_id": "000080", "material": "鉄"}}})
	req := httptest.NewRequest("POST", "/api/our-order/new", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, &auth.User{Username: "alice", IsAdmin: true})
	rr := httptest.NewRecorder()
	NewOurOrderAPIHandler(rr, req)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), PurchaseOrderTemplate) {
		t.Errorf("テンプレートが無いことを知らせていません: %d %s", rr.Code, rr.Body.String())
	}
	var n int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE title = '作成中' OR (title = ? AND parent_id = 0)`, PurchaseOrderBoxTitle).Scan(&n)
	if n != 0 {
		t.Errorf("断る前にページを作りました（%d枚）", n)
	}
}
