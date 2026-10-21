package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
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
