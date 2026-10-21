package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 手配不要表（2026-10-21・mfg_skip.go）の行は、手配済みの数え方で引く——案件40の板4枚を不要にすれば、案件40の残りは0。
func TestLedgerCountsSkipRows(t *testing.T) {
	seedLedger(t)
	addPage(t, 60, 0, "製造", "root", "302", true)
	syncBody(t, 60, `<h1>製造</h1><table data-type="`+SkipType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>案件</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>理由</th><th>日付</th></tr>`+
		`<tr><td>000041</td><td>000040</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>4</td><td>在庫で足りる</td><td>2026-10-21</td></tr>`+
		`</tbody></table>`)
	if got := remainingByOrder(t); got[40] != 0 || got[42] != 6 {
		t.Errorf("⚠ 手配不要表の4枚が案件40から引かれていません: %v（A 0・B 6 のはず）", got)
	}
}

// 製造ページの発注部材表の行の「🚫 不要」で手配不要表へ移り（理由と日付）、「↩ 戻す」で外れる。製造ページでなければ断る。
func TestMfgSkipMovesDraftRowAndBack(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	draft := tableOfLinesHTML(OrderDraftType, []ourOrderLine{
		{ProductID: "000031", ForOrder: "000030", Material: "鉄", Shape: "FB", Size: "t4.5*75*1090", Quantity: "2"},
		{ProductID: "000031", ForOrder: "000030", ItemName: "ボルト", Quantity: "8"},
	}, true)
	addPage(t, 991, -1, "ブラケット", "root", "302", true)
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl>` + draft
	writeBodyFile(t, 991, body)
	syncBody(t, 991, body)
	addPage(t, 992, -1, "ふつうのページ", "root", "302", true)
	writeBodyFile(t, 992, `<h1>ふつう</h1>`+draft)
	syncBody(t, 992, `<h1>ふつう</h1>`+draft)

	if code, _ := postRFQ(t, adminUser(), MfgSkipAPIHandler, map[string]any{"page_id": "000992", "table": 1, "row": 1}); code == 200 {
		t.Errorf("⚠ 製造ページでないページで手配不要にできます")
	}
	if code, out := postRFQ(t, adminUser(), MfgSkipAPIHandler, map[string]any{"page_id": "000991", "table": 1, "row": 1, "reason": "在庫で 足りる"}); code != 200 {
		t.Fatalf("手配不要にできません: %d %v", code, out)
	}
	got := readPageBody(t, "000991")
	at := strings.Index(got, "<caption>手配不要表</caption>")
	if at < 0 || !strings.Contains(got[at:], "在庫で 足りる") || !strings.Contains(got[at:], "t4.5*75*1090") {
		t.Fatalf("⚠ 手配不要表に行（理由つき）がありません:\n%s", got)
	}
	if d := strings.Index(got, "<caption>発注部材表</caption>"); d < 0 || strings.Contains(got[d:at], "t4.5*75*1090") || !strings.Contains(got[d:at], "ボルト") {
		t.Errorf("⚠ 発注部材表から板の行だけが消えていません:\n%s", got)
	}
	if code, out := postRFQ(t, adminUser(), MfgSkipRemoveAPIHandler, map[string]any{"page_id": "000991", "row": 1}); code != 200 {
		t.Fatalf("戻せません: %d %v", code, out)
	}
	if got := readPageBody(t, "000991"); strings.Contains(got, "手配不要表") {
		t.Errorf("⚠ 最後の1行を戻したのに、手配不要表が残っています:\n%s", got)
	}
}
