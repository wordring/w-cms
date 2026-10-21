package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 追加の発注の行（備考「追加」・2026-10-21・order_unsend.go）は手配済みに数えない——作り直しのぶんを、余りとして
// ほかの案件に回さない。⚠ 数えると、案件40の6枚が A を埋め、余り2枚が B に回る（A 0・B 4）。
func TestLedgerIgnoresExtraLines(t *testing.T) {
	seedLedger(t)
	addPage(t, 50, 0, "発注 わかば鋼業", "root", "302", true)
	syncBody(t, 50, `<h1>発注 わかば鋼業</h1><table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>案件</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>備考</th></tr>`+
		`<tr><td>000041</td><td>000040</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>追加</td></tr>`+
		`</tbody></table>`)
	if got := remainingByOrder(t); got[40] != 4 || got[42] != 6 {
		t.Errorf("⚠ 追加の発注の行を手配済みに数えています: %v（A 4・B 6 のはず）", got)
	}
}

// 送ったことを取り消す（2026-10-21）——発注済の行を未発注に戻す。納品済の行があれば戻さない。
func TestOrderUnsend(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "みなと商店",
		"lines":    []map[string]any{{"product_id": "000031", "material": "鉄", "shape": "FB", "size": "t4.5*75*1090", "quantity": "2"}},
	})
	if code != 200 {
		t.Fatalf("部材発注書を作れません: %d %v", code, out)
	}
	po := out["page_id"].(string)
	set := func(v string) {
		if err := cms.RewriteBody(po, "root", func(cur string) string { o, _ := setOrderLineStatus(cur, 0, v); return o }); err != nil {
			t.Fatal(err)
		}
	}
	set(OrderLineSent)
	if body := readPageBody(t, po); !unsendButtonVisible(t, po) || !strings.Contains(body, OrderLineSent) {
		t.Fatalf("⚠ 送った部材発注書に「↩ 未送付に戻す」がありません:\n%s", body)
	}
	if code, out := postRFQ(t, adminUser(), OrderUnsendAPIHandler, map[string]any{"page_id": po}); code != 200 || out["rows"].(float64) != 1 {
		t.Fatalf("⚠ 未送付に戻せません: %d %v", code, out)
	}
	if body := readPageBody(t, po); strings.Contains(body, ">"+OrderLineSent+"<") || !strings.Contains(body, ">"+OrderLineUnsent+"<") {
		t.Errorf("⚠ 発注済の行が未発注に戻っていません:\n%s", body)
	}
	set(OrderLineDelivered)
	if code, out := postRFQ(t, adminUser(), OrderUnsendAPIHandler, map[string]any{"page_id": po}); code != 409 {
		t.Errorf("⚠ 納品済の行がある部材発注書を戻せます: %d %v", code, out)
	}
}

// unsendButtonVisible は部材発注書 id の送る欄に「↩ 未送付に戻す」が出るかです。
func unsendButtonVisible(t *testing.T, id string) bool {
	t.Helper()
	return strings.Contains(unsendButtonHTML(OrderSendStateOf(database.DB, pageNum(id))), "data-order-unsend")
}
