package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 見積の依頼を案件にする（2026-10-19・quote_case.go）。

// seedQuoteCase は見積の依頼の案件ページ（`ページ種類：顧客見積依頼`）を索引に入れます。
func seedQuoteCase(t *testing.T, idInt, rootInt int, client, due, rows string) {
	t.Helper()
	addPage(t, idInt, rootInt, "見積依頼", "alice", "302", true)
	body := `<h1>【顧客】見積依頼</h1><dl data-type="tags">` +
		`<dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerQuote + `</dd>` +
		`<dt>発注元</dt><dd>` + client + `</dd><dt>納期</dt><dd>` + due + `</dd></dl>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>案件明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>` +
		`<th>単価</th><th>納期</th><th>出荷済数量</th><th>備考</th><th>状態</th></tr>` + rows + `</tbody></table>`
	if err := cms.SyncIndex(page.FormatID(idInt), body); err != nil {
		t.Fatalf("SyncIndex(%d): %v", idInt, err)
	}
}

// TestQuoteCaseInBacklog は、見積の依頼の案件が
//   - 案件残表に「見積」として並ぶ
//   - 受注残高に入らない（単価があっても）・納品書は作れない
//   - 製造ページを始められる
//
// ことを固定します。
func TestQuoteCaseInBacklog(t *testing.T) {
	setupExtTest(t, "000700", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 701, -1, "案件", "alice", "302", true)
	seedOrder(t, 702, 701, "あけぼの精工", "2026-11-01", pricedItem("A-1", "注文の品", "2", "1000", "", "未着手"))
	seedQuoteCase(t, 703, 701, "あけぼの精工", "2026-11-01", pricedItem("Q-1", "見積の品", "50", "9999", "", "未着手"))

	view := backlogViewHTML(u, 701)
	for _, want := range []string{`>見積 000703</a>`, `<p class="backlog-total">受注残高: 2,000円</p>`, `title="見積の依頼です（注文が来たら納品できます）"`} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 案件残表に %q がありません:\n%s", want, view)
		}
	}
	if code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000703", "row": 0, "item": "Q-1", "qty": "50"}}}); code != 200 {
		t.Errorf("⚠ 見積の依頼から製造ページを始められません: %d %v", code, out)
	}
	if code, _ := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000703", "row": 0, "item": "Q-1", "qty": "1"}}}); code == 200 {
		t.Errorf("⚠ 見積の依頼から納品書を作れてしまいます")
	}
}

// TestQuoteCaseNotInProcurement は、見積の依頼の案件の行から必要部材が出ないことを固定します（まだ注文ではない）。
func TestQuoteCaseNotInProcurement(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	user := &auth.User{Username: "root", IsAdmin: true}
	before, err := UnorderedItems(user)
	if err != nil {
		t.Fatal(err)
	}
	// 同じ加工製品（000031）を 10 個、見積の依頼として足す。
	addPage(t, 34, 0, "見積依頼", "root", "302", true)
	syncBody(t, 34, `<h1>見積依頼</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindCustomerQuote+`</dd></dl>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>10</td></tr></tbody></table>`)
	after, err := UnorderedItems(user)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("⚠ 見積の依頼から必要部材が出ています: 前 %d 行・後 %d 行 %+v", len(before), len(after), after)
	}
}

// TestBuildQuoteCaseHTML は見積依頼ページの本文（題・種類・発注元・受信元）を固定します。
func TestBuildQuoteCaseHTML(t *testing.T) {
	tmpl := `<h1>案件ページ</h1><dl data-type="tags"><dt>発注書番号</dt><dd><br/></dd><dt>発注元</dt><dd><br/></dd><dt>` + SourceRefTag +
		`</dt><dd><br/></dd><dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerOrder + `</dd></dl>` + emptyVocabTable(clientOrderItemsType)
	got := buildQuoteCaseHTML(tmpl, "ブラケットの見積のお願い", "あけぼの精工", "000123")
	for _, want := range []string{"<h1>【顧客】見積依頼　ブラケットの見積のお願い</h1>", "<dd>あけぼの精工</dd>", "<dd>000123</dd>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindCustomerQuote + "</dd>", "<caption>案件明細表</caption>"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 見積依頼ページに %q がありません:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<dd>"+PageKindCustomerOrder+"</dd>") {
		t.Errorf("⚠ 種類が顧客発注書のままです:\n%s", got)
	}
}
