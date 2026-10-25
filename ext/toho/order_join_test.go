package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// 相乗り（2026-10-21・order_join.go）——別の製造の発注部材表の行を、まだ送っていない部材発注書へ足す。
// 同じ部材の行は数を足して内訳・案件・弊社品番・製造を書き足し、違う行はその部材の行のすぐ下に足す。
// 仕入先の違う部材発注書・送った部材発注書には足さない。
func TestJoinOurOrderMergesAndRefuses(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	for _, id := range []int{991, 992} {
		addPage(t, id, -1, "製造"+page.FormatID(id), "root", "302", true)
		syncBody(t, id, `<h1>製造</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)
	}
	fb := func(product, order, qty, note string) map[string]any {
		return map[string]any{"product_id": product, "for_order": order, "kind": "材料", "material": "鉄", "shape": "FB", "size": "t4.5*75*1090", "quantity": qty, "note": note}
	}
	code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "みなと商店", "draft_page": "000991", "draft_index": "1",
		"lines": []map[string]any{fb("000031", "000030", "2", ""), {"product_id": "000031", "for_order": "000030", "kind": "購入部品", "item_name": "ボルト", "quantity": "8"}},
	})
	if code != 200 {
		t.Fatalf("部材発注書を作れません: %d %v", code, out)
	}
	po := out["page_id"].(string)

	// 仕入先が違えば断る。
	if code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "ひかりレーザー", "draft_page": "000992", "draft_index": "1", "join": po, "lines": []map[string]any{fb("000032", "000035", "3", "")},
	}); code != 409 || !strings.Contains(out["message"].(string), "仕入先") {
		t.Errorf("⚠ 仕入先の違う部材発注書に足せます: %d %v", code, out)
	}

	code, out = postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "みなと商店", "draft_page": "000992", "draft_index": "1", "join": po,
		"lines": []map[string]any{fb("000032", "000035", "3", ""), fb("000032", "000035", "1", "定尺で")},
	})
	if code != 200 || out["joined"] != true || out["summed"].(float64) != 1 || out["added"].(float64) != 1 {
		t.Fatalf("⚠ 相乗りできません（数を足した1行・新しい1行のはず）: %d %v", code, out)
	}
	body := readPageBody(t, po)
	for _, want := range []string{
		"<td>000031、000032</td><td>000030、000035</td><td>000991、000992</td><td>000030/000031:2、000035/000032:3</td>",
		">5</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 数を足した行に %q がありません:\n%s", want, body)
		}
	}
	// 備考の違う行は、同じ部材の行のすぐ下（ボルトの行より上）。
	if a, b := strings.Index(body, "定尺で"), strings.Index(body, "ボルト"); a < 0 || b < 0 || a > b {
		t.Errorf("⚠ 備考の違う行が、同じ部材の行のすぐ下にありません:\n%s", body)
	}
	// 2つめの製造の「この製造の書類」にも並ぶ。
	found := false
	for _, d := range mfgDocs(adminUser(), 992) {
		found = found || page.FormatID(d.PageID) == po
	}
	if !found {
		t.Errorf("⚠ 相乗りした製造 000992 の「この製造の書類」に部材発注書 %s がありません", po)
	}
	// 送った部材発注書には足さない。
	if err := cms.RewriteBody(po, "root", func(cur string) string { out, _ := setOrderLineStatus(cur, 0, OrderLineSent); return out }); err != nil {
		t.Fatal(err)
	}
	if code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "みなと商店", "draft_page": "000992", "draft_index": "1", "join": po, "lines": []map[string]any{fb("000032", "000035", "3", "")},
	}); code != 409 || !strings.Contains(out["message"].(string), "送ってある") {
		t.Errorf("⚠ 送った部材発注書に足せます: %d %v", code, out)
	}
}

// TestOurOrderNewRefusesRepeatByDraftSig は、⚠ **同じ発注部材表から2枚目の部材発注書を作らず、消すのは作るのに使った表だけ**
// であることを固定します（2026-10-26——応答が届かず押し直すと、2枚目を作り、番号のずれたほかの業者の表を消した）。
func TestOurOrderNewRefusesRepeatByDraftSig(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 993, -1, "製造000993", "root", "302", true)
	lineA := ourOrderLine{ProductID: "000031", Kind: "購入部品", ItemName: "ボルトA", Quantity: "8"}
	lineB := ourOrderLine{ProductID: "000032", Kind: "購入部品", ItemName: "ナットB", Quantity: "4"}
	body := `<h1>製造</h1>` + tableOfLinesHTML(OrderDraftType, []ourOrderLine{lineA}, true) + tableOfLinesHTML(OrderDraftType, []ourOrderLine{lineB}, true)
	seedBody(t, "000993", body)
	nodes, _ := htmldoc.ParseFragment(body)
	tables := tablesOfType(nodes, OrderDraftType)
	if len(tables) != 2 {
		t.Fatalf("前提: 発注部材表が %d 枚", len(tables))
	}
	sigA := tableSig(tables[0])
	req := map[string]any{
		"supplier": "みなと商店", "draft_page": "000993", "draft_index": "1", "draft_sig": sigA,
		"lines": []map[string]any{{"product_id": "000031", "kind": "購入部品", "item_name": "ボルトA", "quantity": "8"}},
	}
	if code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, req); code != 200 {
		t.Fatalf("部材発注書を作れません: %d %v", code, out)
	}
	if code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, req); code != 409 {
		t.Errorf("⚠ 同じ表からもう1枚作りました: %d %v", code, out)
	}
	after := readPageBody(t, "000993")
	if strings.Contains(after, "ボルトA") || !strings.Contains(after, "ナットB") {
		t.Errorf("⚠ 消す表を取り違えました（ボルトA の表だけ消えるはず）:\n%s", after)
	}
}
