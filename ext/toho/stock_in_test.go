package toho

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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

// 製造明細表と発注部材表の、数量のすぐ右に「在庫数」の鏡の列が出る（2026-10-23・stock_mirror.go）。
// 在庫表に行の無いものは空欄。本文には書かない。
func TestStockColumnNextToQuantity(t *testing.T) {
	mfg := seedMfgWork(t)
	addPage(t, 40, 0, StockBoxTitle, "root", "302", true)
	stock := `<h1>在庫</h1>` +
		`<table><caption>材料在庫表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>在庫数</th></tr>` +
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>7</td></tr></tbody></table>` +
		`<table><caption>加工製品在庫表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>在庫数</th></tr>` +
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>4</td></tr></tbody></table>`
	writeBodyFile(t, 40, stock)
	syncBody(t, 40, stock)
	// 置き場「在庫」の外の在庫表は数えない（2026-10-26）。
	addPage(t, 41, 0, "自分のページ", "root", "302", true)
	other := `<h1>自分のページ</h1><table><caption>加工製品在庫表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>在庫数</th></tr>` +
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>100</td></tr></tbody></table>`
	writeBodyFile(t, 41, other)
	syncBody(t, 41, other)
	u := adminUser()
	render := func(id string) string {
		return cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), u), pageNum(id), readPageBody(t, id))
	}
	view := render(mfg)
	if !strings.Contains(view, `<th>数量</th><th class="vocab-chrome w-no-print stock-qty"`) {
		t.Errorf("⚠ 製造明細表の数量の右に在庫数の見出しがありません:\n%s", view)
	}
	if !regexp.MustCompile(`<td>2</td><td class="vocab-chrome w-no-print stock-qty"[^>]*>4</td>`).MatchString(view) {
		t.Errorf("⚠ ブラケット（000031）の在庫数 4 が数量の右にありません:\n%s", view)
	}
	if !regexp.MustCompile(`<td>5</td><td class="vocab-chrome w-no-print stock-qty"[^>]*></td>`).MatchString(view) {
		t.Errorf("⚠ 弊社品番の無い行の在庫数が空欄ではありません:\n%s", view)
	}
	// 発注部材表（製造ページの部材作業から入れる）。
	if code, out := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": []map[string]any{{"row": 0, "item": "K-1"}}}); code != 200 {
		t.Fatalf("部材を集められません: %d %v", code, out)
	}
	if code, out := postRFQ(t, u, MfgNeedsAPIHandler, map[string]any{"page_id": mfg, "rows": []int{1, 2}, "action": "order"}); code != 200 {
		t.Fatalf("発注へ移せません: %d %v", code, out)
	}
	view = render(mfg)
	at := strings.Index(view, "<caption>発注部材表</caption>")
	if at < 0 {
		t.Fatalf("発注部材表がありません:\n%s", view)
	}
	draft := view[at : strings.Index(view[at:], "</table>")+at]
	if !regexp.MustCompile(`<td>FB</td><td>t4.5\*75\*1090</td>.*?<td class="vocab-chrome w-no-print stock-qty"[^>]*>7</td>`).MatchString(draft) {
		t.Errorf("⚠ 発注部材表の FB の行に在庫数 7 がありません:\n%s", draft)
	}
	if strings.Contains(readPageBody(t, mfg), "stock-qty") {
		t.Errorf("⚠ 在庫数の列が本文に書かれました")
	}
}

// 製造完了で加工製品を在庫表へ入れる（2026-10-23・mfg_made_stock.go）——
//   - 製造数が空なら数量を入れ、在庫表は変わらない（作った数＝納める数）
//   - 製造数が数量より多ければ、その差を加工製品在庫表へ。製造完了を外すと同じ数を引く
//   - 自社案件の行は、作った数が全部在庫
func TestMadeProductsGoIntoStock(t *testing.T) {
	seedMfgWork(t)
	addPage(t, 40, 0, StockBoxTitle, "root", "302", true)
	stock := `<h1>在庫</h1><table><caption>加工製品在庫表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>在庫数</th></tr>` +
		`<tr><td></td><td></td><td></td><td></td></tr></tbody></table>`
	writeBodyFile(t, 40, stock)
	syncBody(t, 40, stock)
	mfg := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl>` +
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>製造数</th><th>製造完了</th></tr>` +
		`<tr><td>000031</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td><td></td><td></td></tr>` +
		`<tr><td>000031</td><td>000030</td><td>K-3</td><td>ブラケット</td><td>2</td><td>5</td><td></td></tr></tbody></table>`
	writeBodyFile(t, 33, mfg)
	syncBody(t, 33, mfg)
	u := adminUser()
	made := func(row int, item, old, value string) map[string]any {
		t.Helper()
		code, out := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": "000033", "row": row, "item_no": item, "old": old, "value": value})
		if code != 200 {
			t.Fatalf("製造完了を書けません: %d %v", code, out)
		}
		return out
	}
	if out := made(0, "K-1", "", MarkDone); out["stocked"].(float64) != 0 {
		t.Errorf("⚠ 数量どおり作った行で在庫表を直しました: %v", out)
	}
	if body := readPageBody(t, "000033"); !strings.Contains(body, "<td>K-1</td><td>ブラケット</td><td>2</td><td>2</td><td>済</td>") {
		t.Errorf("⚠ 空の製造数に数量が入っていません:\n%s", body)
	}
	made(1, "K-3", "", MarkDone)
	if body := readPageBody(t, "000040"); !strings.Contains(body, "<td>000031</td><td>K-3</td><td>ブラケット</td><td>3</td>") {
		t.Errorf("⚠ 多く作った 5 − 2 = 3 が加工製品在庫表にありません:\n%s", body)
	}
	made(1, "K-3", MarkDone, "")
	if body := readPageBody(t, "000040"); strings.Contains(body, "K-3") {
		t.Errorf("⚠ 製造完了を外したのに、在庫表の行が残っています:\n%s", body)
	}
	// 自社案件の行は、作った数が全部在庫。
	syncBody(t, 30, `<h1>受注</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOwnCase+`</dd></dl>`)
	made(1, "K-3", "", MarkDone)
	if body := readPageBody(t, "000040"); !strings.Contains(body, "<td>000031</td><td>K-3</td><td>ブラケット</td><td>5</td>") {
		t.Errorf("⚠ 自社案件で作った 5 が、全部は在庫表に入っていません:\n%s", body)
	}
	// 「済」でない人の書いた値（途中）を消しても、在庫は動かない（2026-10-26）。
	cur := readPageBody(t, "000033")
	cur = strings.Replace(cur, "<td>K-3</td><td>ブラケット</td><td>2</td><td>5</td><td>済</td>", "<td>K-3</td><td>ブラケット</td><td>2</td><td>5</td><td>途中</td>", 1)
	writeBodyFile(t, 33, cur)
	syncBody(t, 33, cur)
	if out := made(1, "K-3", "途中", ""); out["stocked"].(float64) != 0 {
		t.Errorf("⚠ 「済」でなかった行を消して在庫を動かしました: %v", out)
	}
	if body := readPageBody(t, "000040"); !strings.Contains(body, "<td>000031</td><td>K-3</td><td>ブラケット</td><td>5</td>") {
		t.Errorf("⚠ 「済」でなかった行を消して在庫が変わりました:\n%s", body)
	}
}

// TestStockNeedsWriteOnStockBox は、在庫の置き場を読むだけの人が部材発注書を「納品済」にしても、在庫表は変えず、そう断る
// ことを固定します（2026-10-26。それまでは部材発注書に書ければ、在庫表も書き替えられた）。
func TestStockNeedsWriteOnStockBox(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 40, 0, StockBoxTitle, "bob", "302", true) // root は読むだけ
	stock := `<h1>在庫</h1><table><caption>材料在庫表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>在庫数</th><th>単位</th><th>棚卸し日</th><th>備考</th></tr>` +
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td><td></td><td></td><td></td></tr></tbody></table>`
	writeBodyFile(t, 40, stock)
	syncBody(t, 40, stock)
	order := `<h1>発注 みなと商店</h1><table><caption>部材発注明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>仕様</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td></td><td>材料</td><td></td><td></td><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>5</td><td>発注済</td></tr></tbody></table>`
	writeBodyFile(t, 32, order)
	syncBody(t, 32, order)
	code, out := postRFQ(t, &auth.User{Username: "root"}, OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 1, "value": OrderLineDelivered})
	if code != 200 {
		t.Fatalf("納品済にできません: %d %v", code, out)
	}
	if body := readPageBody(t, "000040"); !strings.Contains(body, "<td>t4.5*75*1090</td><td>1</td>") {
		t.Errorf("⚠ 在庫の置き場に書けない人が在庫表を変えました:\n%s", body)
	}
	if !strings.Contains(fmt.Sprint(out), "権限") {
		t.Errorf("在庫表を変えなかったことを言っていません: %v", out)
	}
}

// TestStockWithAnEmptyTableFirst は、行の無い部材発注明細表が前にあっても、届いた行が在庫表へ入ることを固定します
// （2026-10-26。それまでは表の番号が1つずれて、入らなかった）。
func TestStockWithAnEmptyTableFirst(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 40, 0, StockBoxTitle, "root", "302", true)
	stock := `<h1>在庫</h1><table><caption>購入部品在庫表</caption><tbody><tr><th>品名</th><th>仕様</th><th>在庫数</th><th>単位</th><th>棚卸し日</th><th>備考</th></tr>` +
		`<tr><td></td><td></td><td></td><td></td><td></td><td></td></tr></tbody></table>`
	writeBodyFile(t, 40, stock)
	syncBody(t, 40, stock)
	head := `<tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>仕様</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>状態</th></tr>`
	order := `<h1>発注 みなと商店</h1><table><caption>部材発注明細表</caption><tbody>` + head + `</tbody></table>` +
		`<table><caption>部材発注明細表</caption><tbody>` + head +
		`<tr><td>000031</td><td></td><td>購入部品</td><td>ボルト</td><td>M8</td><td></td><td></td><td></td><td>4</td><td>発注済</td></tr></tbody></table>`
	writeBodyFile(t, 32, order)
	syncBody(t, 32, order)
	code, out := postRFQ(t, adminUser(), OrderLineStatusAPIHandler, map[string]any{"page_id": "000032", "row": 0, "value": OrderLineDelivered})
	if code != 200 {
		t.Fatalf("納品済にできません: %d %v", code, out)
	}
	if body := readPageBody(t, "000040"); !strings.Contains(body, "<td>ボルト</td><td>M8</td><td>4</td>") {
		t.Errorf("⚠ 2つ目の表の届いた行が在庫表に入っていません: %v\n%s", out, body)
	}
}
