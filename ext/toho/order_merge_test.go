package toho

import (
	"strings"
	"testing"
)

// 発注部材表の行をまとめる（2026-10-21・order_merge.go）——備考まで同じなら1行に足して内訳を書き、違えば上下に並べる。
func TestMergeOrderLinesSumsSameAndKeepsDifferentAdjacent(t *testing.T) {
	plate := func(order, product, qty, note string) ourOrderLine {
		return ourOrderLine{ProductID: product, ForOrder: order, Kind: "材料", Material: "鉄", Shape: "板", Size: "t3.2*100*200",
			Quantity: qty, Note: note, Mfg: "000991", Vendor: "わかば鋼業"}
	}
	in := []ourOrderLine{
		plate("000040", "000041", "4", ""),
		{ProductID: "000041", ForOrder: "000040", Kind: "購入部品", ItemName: "ボルト", Quantity: "8", Mfg: "000991"},
		plate("000042", "000043", "6", ""),
		plate("000042", "000043", "2", "定尺で"),
		plate("000040", "000041", "1", ""), // 同じ案件・同じ加工製品は内訳の中で足す
	}
	got := mergeOrderLines(in)
	if len(got) != 3 {
		t.Fatalf("行が %d です（3 のはず）: %#v", len(got), got)
	}
	m := got[0]
	if m.Quantity != "11" || m.Breakdown != "000040/000041:5、000042/000043:6" || m.ForOrder != "000040、000042" || m.ProductID != "000041、000043" {
		t.Errorf("⚠ まとめた行が違います: 数量 %s・内訳 %s・案件 %s・弊社品番 %s", m.Quantity, m.Breakdown, m.ForOrder, m.ProductID)
	}
	if got[1].Note != "定尺で" || got[1].Breakdown != "" || got[1].ForOrder != "000042" || got[1].Quantity != "2" {
		t.Errorf("⚠ 備考の違う行が、まとめた行のすぐ下にありません: %#v", got[1])
	}
	if got[2].ItemName != "ボルト" {
		t.Errorf("⚠ 種類の違う行が、材料のあとにありません: %#v", got[2])
	}

	groups := splitByVendor(got)
	if len(groups) != 2 || groups[0][0].Vendor != "わかば鋼業" || len(groups[0]) != 2 || groups[1][0].Vendor != "" {
		t.Errorf("⚠ 推奨業者ごとに分かれていません（推奨業者の無い組は最後）: %#v", groups)
	}
}

// 内訳の読み書き——読めない内訳は「まとめていない行」として読む（呼び手が案件と弊社品番の列を見る）。
func TestParseBreakdown(t *testing.T) {
	es, ok := parseBreakdown("000040/000041:5、 /000043:2")
	if !ok || len(es) != 2 || es[0] != (breakdownEntry{"000040", "000041", 5}) || es[1] != (breakdownEntry{"", "000043", 2}) {
		t.Errorf("内訳を読めません: %v %#v", ok, es)
	}
	if got := formatBreakdown(es); got != "000040/000041:5、/000043:2" {
		t.Errorf("内訳の書き方が違います: %s", got)
	}
	for _, bad := range []string{"", "000040:5", "000040/abc:5", "000040/000041:x"} {
		if _, ok := parseBreakdown(bad); ok {
			t.Errorf("⚠ 読めない内訳 %q を読めたことにしています", bad)
		}
	}
}

// 帳簿は、内訳のある行を案件ごとに当てる（2026-10-21）。⚠ 内訳を読まなければ、案件の列「000040、000042」は読めず、
// 6枚は案件の無い手当てとして納期の早い順に当たる（A 0・B 4）——内訳どおりなら A に1枚・B に5枚（A 3・B 1）。
func TestLedgerSplitsMergedRowByBreakdown(t *testing.T) {
	seedLedger(t)
	setMergedPurchase(t, 50, "6", "000040/000041:1、000042/000041:5")
	if got := remainingByOrder(t); got[40] != 3 || got[42] != 1 {
		t.Errorf("⚠ 内訳どおりに当たっていません: %v（A 3・B 1 のはず）", got)
	}
	// 人が数量を増やした（定尺に丸めた）ぶんは在庫——納期の早い A に回る。
	seedLedger(t)
	setMergedPurchase(t, 50, "8", "000040/000041:1、000042/000041:5")
	if got := remainingByOrder(t); got[40] != 1 || got[42] != 1 {
		t.Errorf("⚠ 増やした2枚が在庫として回っていません: %v（A 1・B 1 のはず）", got)
	}
}

// setMergedPurchase は部材発注書ページ id に、板を qty 枚・内訳 breakdown の行（まとめた行）を書きます。
func setMergedPurchase(t *testing.T, id int, qty, breakdown string) {
	t.Helper()
	addPage(t, id, 0, "発注 わかば鋼業", "root", "302", true)
	cells := []string{"000041", "000040、000042", breakdown, "鉄", "板", "t3.2*100*200", qty}
	syncBody(t, id, `<h1>発注 わかば鋼業</h1>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>案件</th><th>内訳</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th></tr>`+
		`<tr><td>`+strings.Join(cells, `</td><td>`)+`</td></tr>`+
		`</tbody></table>`)
}
