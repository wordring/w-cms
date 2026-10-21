package toho

import (
	"strings"
	"testing"
)

// 部材発注書ページの本文を組む（2026-09-21）。§7b の「足りない1つ」。
//
// ⚠ **`弊社品番` が書かれること**がいちばん大事です——無いと**どの加工製品のぶんか**を
// 誰も辿れず、手配状況の鏡が**黙って未手配のまま**になります。

func TestBuildOurOrderWritesOurItemNo(t *testing.T) {
	body := testOurOrder("000138", "みなと商店", "2026-09-21", "2026-09-30", "定尺で可", "",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄", Shape: "FB",
			Size: "t4.5*75*1090", Quantity: "2", Cost: "860"}})

	for _, want := range []string{
		"<dt>" + PurchaseNoTag + "</dt><dd>000138</dd>", // ⚠ 番号はページ番号そのもの
		"<dt>" + SupplierTag + "</dt><dd>みなと商店</dd>",
		"<dt>" + PurchasedAtTag + "</dt><dd>2026-09-21</dd>",
		"<dt>" + DueDateTag + "</dt><dd>2026-09-30</dd>",
		"<td>000080</td>", // ⚠ 弊社品番
		"<td>t4.5*75*1090</td>",
		"<td>未発注</td>", // ⚠ 紙はできたが、まだ出していない（order_status.go）
		"<caption>部材発注明細表</caption>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("本文に %q がありません:\n%s", want, body)
		}
	}
}

// TestBuildOurOrderDropsRedundantItemName は、⚠ **材料の行に `品名` を書かない**ことを
// 固定します。
//
// 材料に単独の名前はなく、**材質・形状・寸法の3つで決まります**
// ——`鉄 FB t4.5*75*1090` と書くと**同じことが紙の上で2回**言われ、直すときに食い違います。
// ⚠ **購入部品は逆**で、**品名が同一性そのもの**なので残します。
func TestBuildOurOrderDropsRedundantItemName(t *testing.T) {
	mat := testOurOrder("000138", "みなと商店", "2026-09-21", "", "", "",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄", Shape: "FB",
			Size: "t4.5*75*1090", ItemName: "鉄 FB t4.5*75*1090", Quantity: "2"}})
	if strings.Contains(mat, "鉄 FB t4.5*75*1090</td><td>鉄</td>") {
		t.Errorf("⚠ 材料の行に品名（材質・形状・寸法の焼き直し）が入っています:\n%s", mat)
	}

	// 購入部品（材質・形状・寸法が空）は品名が残ること。
	bought := testOurOrder("000139", "かなめ商会", "2026-09-21", "", "", "",
		[]ourOrderLine{{ProductID: "000080", ItemName: "カラー FAC-V21-D25-L15", Quantity: "4"}})
	if !strings.Contains(bought, "カラー FAC-V21-D25-L15") {
		t.Errorf("⚠ 購入部品の品名まで落としています（同一性が消えます）:\n%s", bought)
	}
}

// TestBuildOurOrderHeaderComesFromDeclaration は、⚠ **見出しを手書きしない**ことを
// 固定します。
//
// ⚠ 手書きに戻すと、**列を足した日に足した列がどこからも読めません**——エラーは出ず、
// 索引は見出しの表示文字で引くので**静かにずれます**。
func TestBuildOurOrderHeaderComesFromDeclaration(t *testing.T) {
	body := testOurOrder("000138", "みなと商店", "2026-09-21", "", "", "",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄"}})
	for _, c := range columnsOf(ourOrderItemsType) {
		if !strings.Contains(body, "<th>"+c.Label+"</th>") {
			t.Errorf("宣言の列 %q が見出しにありません:\n%s", c.Label, body)
		}
	}
	// 値の数も列の数と揃うこと（ずれると1つずつ隣に入ります）。
	row := body[strings.LastIndex(body, "<tr>"):]
	if n := strings.Count(row, "<td>"); n != len(columnsOf(ourOrderItemsType)) {
		t.Errorf("値が %d 個です（列は %d）:\n%s", n, len(columnsOf(ourOrderItemsType)), row)
	}
}

// TestBuildOurOrderKeepsZeroButNotUnknown は、単価の「0」と「空」を分けて運ぶことを固定します（2026-10-21）。
//
//   - ⚠ **人が書いた `0` は `0` のまま**部材発注書に書く（「0円とは別がありがたい」——2026-09-22 利用者）。
//     それまでは `0` を空に落としていて、自社への発注で「0円」と書けませんでした。
//   - 空は空のまま（「まだ分からない」）。値のある単価も、そのまま。
//   - ⚠ **参考単価が無いことを `0` にしないのは送り元**（`unorderedCostValue`）——紙の `0` は「0円で発注した」と読まれます。
func TestBuildOurOrderKeepsZeroButNotUnknown(t *testing.T) {
	for _, tc := range []struct{ cost, want string }{{"0", "<td>0</td>"}, {"860", "<td>860</td>"}} {
		body := testOurOrder("000139", "みなと商店", "2026-09-21", "", "", "",
			[]ourOrderLine{{ProductID: "000080", Material: "鉄", Size: "t4.5", Quantity: "6", Cost: tc.cost}})
		if !strings.Contains(body, tc.want) {
			t.Errorf("⚠ 人が書いた単価 %q が部材発注書に残っていません:\n%s", tc.cost, body)
		}
	}
	if got := unorderedCostValue(UnorderedItem{Cost: 0}); got != "" {
		t.Errorf("⚠ 参考単価の無い部材に %q を送ります（空を送ること——0 は「0円で発注した」と読まれる）", got)
	}
	if got := unorderedCostValue(UnorderedItem{Cost: 860}); got != "860" {
		t.Errorf("参考単価 860 が %q になりました", got)
	}
}
