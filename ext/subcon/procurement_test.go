package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 受注ページの手配状況を、加工製品ごとに集める（2026-09-21）。
//
// ユーザー:「**受注明細の一行は加工製品一種類**で、加工製品一種類に必要な材料など
// 購入品は複数あります。…その表の列の一つとして、**発注書番号と発注書ページへの
// リンク**が必要になると思います」。
//
// ⚠ **スコープに頼っていないこと**がいちばん大事です——発注書は `発注／年／月` に
// あり、受注ページからは**2段**なので、`RelatedPages`（深さ1）では**黙って0件**に
// なります。ここでは**参照でつながっていない発注書**を当てて、それを固定します。

// seedProcurement は 受注ページ(30) / 加工製品(31) / 発注書(32) を用意します。
//
// ⚠ **発注書は受注ページとどこにも繋がっていません**（`発注／年／月` に置かれる想定）。
func seedProcurement(t *testing.T, orderOwner, orderMode string, orderPublic bool) {
	t.Helper()
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 31, 0, "ブラケット", "root", "302", true)
	addPage(t, 32, 0, "発注 みなと商店", orderOwner, orderMode, orderPublic)
	addPage(t, 30, 0, "受注", "root", "302", true)

	syncBody(t, 31, `<h1>ブラケット</h1>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>2</td></tr>`+
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td></tr>`+
		`</tbody></table>`)
	// 発注書——⚠ **弊社品番で加工製品を指します**（受注ページは指していません）。
	syncBody(t, 32, `<h1>発注 みなと商店</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>みなと商店</dd>`+
		`<dt>`+OrderedAtTag+`</dt><dd>2026-09-21</dd></dl>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単価</th></tr>`+
		`<tr><td>000031</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>800</td></tr>`+
		`</tbody></table>`)
	syncBody(t, 30, `<h1>受注</h1>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>3</td></tr>`+
		`</tbody></table>`)
}

// TestProcurementFindsOrdersOutsideScope は、⚠ **参照でつながっていない発注書でも
// 引ける**ことを固定します。
//
// ⚠ **これがこの機能の心臓です。** 発注書は `発注／年／月` に置かれ、受注ページとは
// 直接つながりません。`RelatedPages`（深さ1）に頼ると**黙って0件**になります。
func TestProcurementFindsOrdersOutsideScope(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)

	list, err := ProcurementByProduct(&auth.User{Username: "root", IsAdmin: true}, 30)
	if err != nil {
		t.Fatalf("ProcurementByProductエラー: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("加工製品が %d 件です（1件のはず）: %#v", len(list), list)
	}
	p := list[0]
	if p.PageID != 31 || p.Qty != 3 {
		t.Fatalf("受注明細の読み取りが違います: page=%d qty=%d why=%q", p.PageID, p.Qty, p.Why)
	}
	if len(p.Items) != 2 {
		t.Fatalf("購入品が %d 件です（2件のはず）: %#v", len(p.Items), p.Items)
	}
	// 1台あたり2 × 受注3 = 6 必要、発注済6 → 残0
	var plate *ProcurementItem
	for i := range p.Items {
		if strings.Contains(p.Items[i].Name, "t3.2") {
			plate = &p.Items[i]
		}
	}
	if plate == nil {
		t.Fatalf("材料が見つかりません: %#v", p.Items)
	}
	if plate.Required != 6 || plate.Ordered != 6 || plate.Remaining != 0 {
		t.Errorf("必要6・発注済6・残0 のはずです: %#v", *plate)
	}
	if len(plate.Orders) != 1 || plate.Orders[0].PageID != 32 {
		t.Fatalf("⚠ 発注書に結べていません（スコープに頼っていませんか）: %#v", plate.Orders)
	}
	// ⚠ **手配していないほうは黙らない**（残1・発注書なし）。
	for _, it := range p.Items {
		if strings.Contains(it.Name, "t4.5") && len(it.Orders) != 0 {
			t.Errorf("⚠ 無関係な発注に結んでいます: %#v", it)
		}
	}
}

// TestProcurementHidesUnreadableOrders は、⚠ **読めない発注書は混ぜない**ことを
// 固定します。
//
// ⚠ **これは変異試験が見つけた穴です**（2026-09-21）——可視判定を外しても、
// どの試験も落ちませんでした。「守りを外しても通る試験は、守りではなく別の何かを
// 測っています」。
func TestProcurementHidesUnreadableOrders(t *testing.T) {
	setupMaterialsPermsTest(t)
	// 発注書は alice 専有（mallory は読めない）。
	seedProcurement(t, "alice", "300", false)

	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(32).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は発注書を読めてはいけません")
	}
	list, err := ProcurementByProduct(mallory, 30)
	if err != nil {
		t.Fatalf("ProcurementByProductエラー: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("加工製品が %d 件です: %#v", len(list), list)
	}
	for _, it := range list[0].Items {
		if len(it.Orders) != 0 {
			t.Fatalf("⚠ 読めない発注書が出ています: %#v", it.Orders)
		}
		if it.Ordered != 0 {
			t.Errorf("⚠ 読めない発注書の数量が積まれています: %#v", it)
		}
	}
}

// TestProcurementSaysWhenProductIsUnknown は、⚠ **どの加工製品か分からないことを
// 黙らない**ことを固定します（空欄だと「要る物が無い」に見えます）。
func TestProcurementSaysWhenProductIsUnknown(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, "受注", "root", "302", true)
	syncBody(t, 40, `<h1>受注</h1>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td></td><td>謎-1</td><td>正体不明</td><td>3</td></tr>`+
		`</tbody></table>`)

	list, err := ProcurementByProduct(&auth.User{Username: "root", IsAdmin: true}, 40)
	if err != nil {
		t.Fatalf("ProcurementByProductエラー: %v", err)
	}
	if len(list) != 1 || !strings.Contains(list[0].Why, "どの加工製品か分かりません") {
		t.Fatalf("理由を言っていません: %#v", list)
	}
}
