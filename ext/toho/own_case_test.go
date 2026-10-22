package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 自社案件（2026-10-21・own_case.go）。

// TestOwnCaseInBacklogAndProcurement は、自社案件が
//   - 案件残表に「自社」として並ぶ・受注残高に入らない・納品書は作れない
//   - 製造ページを始められる
//   - 必要部材が出る（客先の注文と同じく手配する）
//
// ことを固定します。
func TestOwnCaseInBacklogAndProcurement(t *testing.T) {
	setupExtTest(t, "000700", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 701, -1, "案件", "alice", "302", true)
	seedOrder(t, 702, 701, "あけぼの精工", "2026-11-01", pricedItem("A-1", "注文の品", "2", "1000", "", "未着手"))
	addPage(t, 703, 701, "自社案件", "alice", "302", true)
	body := `<h1>【自社】案件　治具</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindOwnCase + `</dd>` +
		`<dt>発注元</dt><dd>みらい産業</dd><dt>納期</dt><dd>2026-11-01</dd></dl>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>案件明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>納期</th><th>出荷済数量</th><th>備考</th><th>状態</th></tr>` +
		pricedItem("J-1", "治具", "3", "500", "", "未着手") + `</tbody></table>`
	if err := cms.SyncIndex("000703", body); err != nil {
		t.Fatal(err)
	}
	view := backlogViewHTML(u, 701)
	for _, want := range []string{`>自社 000703</a>`, `<p class="backlog-total">受注残高: 2,000円</p>`} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 案件残表に %q がありません:\n%s", want, view)
		}
	}
	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000703", "row": 0, "item": "J-1", "qty": "3"}}})
	if code != 200 {
		t.Errorf("⚠ 自社案件から製造ページを始められません: %d %v", code, out)
	}
	mfg, _ := out["page_id"].(string)
	if code, _ := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": mfg, "items": []map[string]any{{"order": "000703", "row": 0, "item": "J-1", "qty": "1"}}}); code == 200 {
		t.Errorf("⚠ 自社案件から納品書を作れてしまいます")
	}
}

// TestBuildOwnCaseHTML は自社案件ページの本文（題・種類・発注元・受信元）を固定します。
func TestBuildOwnCaseHTML(t *testing.T) {
	got := buildOwnCaseHTML(testOrderPageTemplate(), "治具の材料", "みらい産業", "000555")
	for _, want := range []string{"<h1>【自社】案件　治具の材料</h1>", "<dd>" + PageKindOwnCase + "</dd>", "<dd>みらい産業</dd>", "<dd>000555</dd>", "案件明細表"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 自社案件ページに %q がありません:\n%s", want, got)
		}
	}
}
