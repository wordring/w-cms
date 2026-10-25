package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/htmldoc"
)

// 部材発注書の行の操作（状態を変える・外す）は、画面が描いたときの行の指紋で同じ行を引き直す（2026-10-26）——
// ほかの人が上に行を足した・外したあとに押しても、選んだ行に効く。合う行が無ければ断る。

// orderRowSigs は本文の1つ目の部材発注明細表の、データ行の指紋を並べます。
func orderRowSigs(t *testing.T, body string) []string {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i, tr := range rowsOf(tablesOfType(nodes, ourOrderItemsType)[0]) {
		if i > 0 {
			out = append(out, rowSig(tr))
		}
	}
	return out
}

func TestOrderRowOpsFollowTheSig(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	row := func(name, status string) string {
		return `<tr><td>000031</td><td></td><td>購入部品</td><td>` + name + `</td><td>4</td><td>` + status + `</td></tr>`
	}
	head := `<h1>発注 みなと商店</h1><table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>数量</th><th>状態</th></tr>`
	before := head + row("ボルト", "発注済") + row("ナット", "発注済") + `</tbody></table>`
	sigs := orderRowSigs(t, before) // 画面が描いたとき——ナットは2行目
	// ほかの人が上に1行足した。
	after := head + row("座金", "発注済") + row("ボルト", "発注済") + row("ナット", "発注済") + `</tbody></table>`
	writeBodyFile(t, 32, after)
	syncBody(t, 32, after)

	u := adminUser()
	if code, out := postRFQ(t, u, OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 2, "sig": sigs[1], "value": OrderLineCancelled}); code != 200 {
		t.Fatalf("取消にできません: %d %v", code, out)
	}
	b := readPageBody(t, "000032")
	if !strings.Contains(b, "<td>ナット</td><td>4</td><td>"+OrderLineCancelled+"</td>") || strings.Contains(b, "<td>ボルト</td><td>4</td><td>"+OrderLineCancelled) {
		t.Errorf("⚠ 選んだナットではない行が変わりました:\n%s", b)
	}
	// 合う行の無い指紋は断る。
	if code, _ := postRFQ(t, u, OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 1, "sig": "nope", "value": OrderLineDelivered}); code != 409 {
		t.Errorf("⚠ 合わない指紋で状態を変えました: %d", code)
	}
	// 外す: 画面が描いたときのボルト（1行目）の指紋で、いまは2行目のボルトを外す。
	if code, out := postRFQ(t, u, OrderReturnRowAPIHandler, map[string]any{"page_id": "000032", "row": 1, "sig": sigs[0]}); code != 200 {
		t.Fatalf("外せません: %d %v", code, out)
	}
	b = readPageBody(t, "000032")
	if strings.Contains(b, "<td>ボルト</td>") || !strings.Contains(b, "<td>座金</td>") {
		t.Errorf("⚠ 選んだボルトではない行を外しました:\n%s", b)
	}
	if code, _ := postRFQ(t, u, OrderReturnRowAPIHandler, map[string]any{"page_id": "000032", "row": 1, "sig": "nope"}); code != 409 {
		t.Errorf("⚠ 合わない指紋で行を外しました: %d", code)
	}
}

// TestOrderReturnRowRefusesDelivered は、⚠ **納品済の行は「発注から外す」で外さない**ことを固定します（2026-10-26——届いた余りは
// もう在庫表へ入っていて、外すと必要部材表に戻ってまた発注され、在庫は多いまま残った）。
func TestOrderReturnRowRefusesDelivered(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	body := `<h1>発注 みなと商店</h1><table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td></td><td>購入部品</td><td>ボルト</td><td>4</td><td>` + OrderLineDelivered + `</td></tr></tbody></table>`
	writeBodyFile(t, 32, body)
	syncBody(t, 32, body)
	sigs := orderRowSigs(t, body)
	if code, out := postRFQ(t, adminUser(), OrderReturnRowAPIHandler, map[string]any{"page_id": "000032", "row": 1, "sig": sigs[0]}); code != 409 {
		t.Errorf("⚠ 納品済の行を外しました: %d %v", code, out)
	}
	if b := readPageBody(t, "000032"); !strings.Contains(b, "<td>ボルト</td>") {
		t.Errorf("⚠ 納品済の行が消えました:\n%s", b)
	}
}
