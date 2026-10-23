package toho

import (
	"strings"
	"testing"
)

// 届いた部材を在庫表へ入れる（2026-10-23・stock_in.go）——
//   - 案件の必要を超えて届いた分だけが、在庫表に増える（案件 000030 は ブラケット 3台 → 板 6・FB 3）
//   - 案件の書かれていない行は、全部が在庫になる
//   - 同じ部材がもう在庫表にあれば、その行に足す
//   - 「納品済」から戻すと同じ数を引き、0 になった行は消す
func TestDeliveredLinesGoIntoStock(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 40, 0, StockBoxTitle, "root", "302", true)
	stock := `<h1>在庫</h1>` +
		`<table><caption>材料在庫表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>在庫数</th><th>単位</th><th>棚卸し日</th><th>備考</th></tr>` +
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td><td></td><td>2026-10-01</td><td></td></tr></tbody></table>` +
		`<table><caption>購入部品在庫表</caption><tbody><tr><th>品名</th><th>仕様</th><th>在庫数</th><th>単位</th><th>棚卸し日</th><th>備考</th></tr>` +
		`<tr><td></td><td></td><td></td><td></td><td></td><td></td></tr></tbody></table>`
	writeBodyFile(t, 40, stock)
	syncBody(t, 40, stock)
	order := `<h1>発注 みなと商店</h1>` +
		`<table><caption>部材発注明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>仕様</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>000030</td><td>材料</td><td></td><td></td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>8</td><td>発注済</td></tr>` +
		`<tr><td>000031</td><td>000030</td><td>材料</td><td></td><td></td><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>5</td><td>発注済</td></tr>` +
		`<tr><td>000031</td><td></td><td>購入部品</td><td>ボルト</td><td>M8</td><td></td><td></td><td></td><td>4</td><td>発注済</td></tr>` +
		`</tbody></table>`
	writeBodyFile(t, 32, order)
	syncBody(t, 32, order)

	u := adminUser()
	code, out := postRFQ(t, u, OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 0, "value": OrderLineDelivered})
	if code != 200 {
		t.Fatalf("納品済にできません: %d %v", code, out)
	}
	if n, _ := out["stocked"].(float64); n != 3 {
		t.Errorf("⚠ 在庫表へ入れた件数が %v です（板の余り・FBの余り・ボルトの3件のはず）: %v", out["stocked"], out)
	}
	body := readPageBody(t, "000040")
	for _, want := range []string{
		"<td>鉄</td><td>板</td><td>t3.2*100*200</td><td>2</td>",  // 8 届いて、案件の必要 6 を引いた残り
		"<td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>3</td>", // もとの 1 に、5 − 3 を足した
		"<td>ボルト</td><td>M8</td><td>4</td>",                    // 案件の無い行は全部
		"<td>2026-10-01</td>",                                  // 棚卸し日は機械が書き換えない
	} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 在庫表に %q がありません:\n%s", want, body)
		}
	}
	// 板の行を「発注済」へ戻すと、足した 2 を引き、0 になった行を消す。
	if code, out := postRFQ(t, u, OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 1, "value": OrderLineSent}); code != 200 {
		t.Fatalf("発注済へ戻せません: %d %v", code, out)
	}
	body = readPageBody(t, "000040")
	if strings.Contains(body, "t3.2*100*200") {
		t.Errorf("⚠ 0 になった板の行が在庫表に残っています:\n%s", body)
	}
	if !strings.Contains(body, "<td>FB</td><td>t4.5*75*1090</td><td>3</td>") {
		t.Errorf("⚠ 戻していない FB の行まで変わりました:\n%s", body)
	}
}

// 在庫表の行を消して、データ行が無くなるときは、空の行を1つ残す（人が書き足せるように）。
func TestApplyStockMovesKeepsABlankRow(t *testing.T) {
	setupMaterialsPermsTest(t)
	sk, _ := stockKindOf("購入部品")
	body := `<table><caption>購入部品在庫表</caption><tbody><tr><th>品名</th><th>仕様</th><th>在庫数</th></tr>` +
		`<tr><td>ボルト</td><td>M8</td><td>4</td></tr></tbody></table>`
	cells := map[string]string{"品名": "ボルト", "仕様": "M8"}
	out, notes := applyStockMoves(body, []stockMove{{kind: sk, key: stockRowKey(sk, cells), cells: cells, qty: -4}})
	if len(notes) != 0 || strings.Contains(out, "ボルト") || !strings.Contains(out, "<tr><td><br/></td><td><br/></td><td><br/></td></tr>") {
		t.Errorf("⚠ 最後の行を消したとき、空の行が残っていません（断り %v）:\n%s", notes, out)
	}
	// 無い行は引けない（断る）。
	if _, notes := applyStockMoves(out, []stockMove{{kind: sk, key: stockRowKey(sk, cells), cells: cells, qty: -1}}); len(notes) != 1 {
		t.Errorf("⚠ 在庫表に無い行を引いたのに断りません: %v", notes)
	}
}
