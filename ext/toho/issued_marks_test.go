package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 「納品書発行」「請求書発行」の印を、発行済の納品書・請求書から数える（2026-10-19・issued_marks.go）。

// TestIssuedMarksFromDeliveryAndInvoice は、
//   - 案件残表の納品書発行は、発行済の納品書の数（未発行の納品書は数えない）
//   - 請求書発行は、発行済の請求書の数（未発行の請求書は数えない）
//   - 案件ページの案件明細表の欄にも同じ印が出て、人が欄に書いた値は使わない
//
// ことを固定します。
func TestIssuedMarksFromDeliveryAndInvoice(t *testing.T) {
	setupExtTest(t, "000930", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 931, -1, "案件", "alice", "302", true)
	seedOrder(t, 932, 931, "あけぼの精工", "2026-11-01", item("A-1", "ブラケット", "10", "2026-11-01", ""))
	seedIssuedDelivery(t, 933, -1, "あけぼの精工", "2026-10-05", "2026-10-05", [5]string{"000932", "A-1", "ブラケット", "6", "100"})
	seedIssuedDelivery(t, 934, -1, "あけぼの精工", "2026-10-06", "", [5]string{"000932", "A-1", "ブラケット", "3", "100"}) // 未発行
	invoice := func(id int, issued, qty string) {
		addPage(t, id, -1, "請求書", "alice", "302", true)
		syncBody(t, id, `<h1>【弊社】請求書</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurInvoice+`</dd>`+
			`<dt>`+DeliveryIssuedTag+`</dt><dd>`+issued+`</dd></dl>`+
			`<table><caption>請求明細表</caption><tbody><tr><th>納品書</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th></tr>`+
			`<tr><td>000933</td><td>000932</td><td>A-1</td><td>ブラケット</td><td>`+qty+`</td><td>100</td></tr></tbody></table>`)
	}
	invoice(935, "2026-10-31", "6")
	invoice(936, "", "3") // 未発行の請求書

	view := backlogViewHTML(u, 931)
	for _, want := range []string{`title="発行済の納品書から数えた">6/10</td>`, `title="発行済の請求書から数えた">6/10</td>`} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 案件残表に %q がありません（未発行は数えない）:\n%s", want, view)
		}
	}

	body := `<h1>案件</h1><table data-type="` + clientOrderItemsType + `"><caption>案件明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>納品書発行</th><th>請求書発行</th></tr>` +
		`<tr><td></td><td>A-1</td><td>ブラケット</td><td>10</td><td>済</td><td></td></tr></tbody></table>`
	got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000932", nil), u), 932, body)
	if strings.Count(got, `class="vocab-chrome made-mirror"`) < 2 || !strings.Contains(got, ">6/10（⚠ 欄に書いた 済 は使いません——納品書の紙を作ると付きます）<") {
		t.Errorf("⚠ 案件明細表の納品書発行・請求書発行に鏡が出ていないか、人が書いた値の断りがありません:\n%s", got)
	}
}
