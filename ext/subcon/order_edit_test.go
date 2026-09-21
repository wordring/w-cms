package subcon

import (
	"strings"
	"testing"
)

// **受注残表から受注明細を書き換える**（2026-09-21）。
//
// ⚠ **計算ビューで初めての「書ける鏡」**です。押した先が**別のページの本文**になる
// ので、取り違えると**別の受注を壊します**。ここで固定するのは**3つの照合**です:
//
//	① 行番号     … どの行か
//	② その行の品番 … ⚠ **行が入れ替わっていないか**（番号は動くが品番は動かない）
//	③ いまの値   … ⚠ **誰かが先に直していないか**（compare-and-swap）
//
// **3つとも通らないと1文字も書きません。**

// editBody は受注明細を2行持つ本文を組みます。
func editBody() string {
	return `<h1>受注</h1><table data-type="` + clientOrderItemsType + `">` +
		`<caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>出荷済み</th>` +
		`<th>備考</th><th>状態</th><th>材料発注</th></tr>` +
		`<tr><td></td><td>A-1</td><td>甲</td><td>100</td><td></td><td></td><td>未着手</td><td></td></tr>` +
		`<tr><td></td><td>A-2</td><td>乙</td><td>50</td><td>10</td><td>急ぎ</td><td>加工中</td><td></td></tr>` +
		`</tbody></table>`
}

// TestSetOrderItemCellWrites は、**当たった行のそのセルだけ**が変わることを固定します。
func TestSetOrderItemCellWrites(t *testing.T) {
	got, err := setOrderItemCell(editBody(), 1, "A-2", "出荷済み", "10", "30")
	if err != nil {
		t.Fatalf("書けません: %v", err)
	}
	if !strings.Contains(got, "<td>30</td>") {
		t.Errorf("書き換わっていません:\n%s", got)
	}
	// ⚠ **他の行・他のセルは触らない。**
	if !strings.Contains(got, "<td>100</td>") || !strings.Contains(got, "<td>急ぎ</td>") {
		t.Errorf("⚠ 関係のないセルまで変わっています:\n%s", got)
	}
	if strings.Contains(got, "<td>10</td>") {
		t.Errorf("古い値が残っています:\n%s", got)
	}
}

// TestSetOrderItemCellClearsToEmpty は、**空へ戻せる**ことを固定します
// （⚠ チェックを外す・出荷済みを消すのに要ります）。
func TestSetOrderItemCellClearsToEmpty(t *testing.T) {
	body := strings.Replace(editBody(),
		`<td>加工中</td><td></td>`, `<td>加工中</td><td>`+MarkDone+`</td>`, 1)
	got, err := setOrderItemCell(body, 1, "A-2", "材料発注", MarkDone, "")
	if err != nil {
		t.Fatalf("空に戻せません: %v", err)
	}
	// ⚠ **セルの形で見ます。** `strings.Contains(got, MarkDone)` だと、見出しの
	// **`出荷済み`** に当たって毎回「残っている」と言います（最初そう書いて誤検出
	// しました）。**部分一致は、短い語ほど嘘をつきます。**
	if strings.Contains(got, "<td>"+MarkDone+"</td>") {
		t.Errorf("印が残っています:\n%s", got)
	}
}

// TestSetOrderItemCellChecksItemNo は、⚠ **行がずれていたら書かない**ことを
// 固定します。
//
// ⚠ **行番号だけで書くと、間に行が挿されたときに隣の行を書き換えます。**
// 番号は動きますが `品番` は動きません——その非対称を照合に使います。
func TestSetOrderItemCellChecksItemNo(t *testing.T) {
	// 0行目は A-1 なのに、A-2 のつもりで書こうとする。
	_, err := setOrderItemCell(editBody(), 0, "A-2", "出荷済み", "", "30")
	if err == nil {
		t.Fatal("⚠ 品番が違う行に書いています（隣の受注を壊します）")
	}
}

// TestSetOrderItemCellChecksOldValue は、⚠ **誰かが先に直していたら書かない**ことを
// 固定します（compare-and-swap）。
//
// ⚠ 黙って上書きすると、**二人が同じ表を見ているとき、後から押したほうが勝ちます**
// ——しかも負けたほうは気づきません。
func TestSetOrderItemCellChecksOldValue(t *testing.T) {
	_, err := setOrderItemCell(editBody(), 1, "A-2", "出荷済み", "0", "30")
	if err == nil {
		t.Fatal("⚠ いまの値を確かめずに上書きしています")
	}
	if !strings.Contains(err.Error(), "読み込み直して") {
		t.Errorf("直し方が書かれていません: %v", err)
	}
}

// TestSetOrderItemCellRefusesUnknownRow は、**無い行を指したら断る**ことを固定します。
func TestSetOrderItemCellRefusesUnknownRow(t *testing.T) {
	for _, row := range []int{-1, 2, 99} {
		if _, err := setOrderItemCell(editBody(), row, "A-1", "備考", "", "x"); err == nil {
			t.Errorf("行 %d を書いています", row)
		}
	}
}

// TestEditableFieldsExcludeKeys は、⚠ **鍵の列と計算の列を書かせない**ことを
// 固定します。
//
// ⚠ `品番` を残表から書き換えられると、**次の書き込みが照合に失敗するか、
// 隣の行に当たります**（鍵が動くため）。`残` は `数量 - 出荷済み` の計算なので
// そもそも本文にありません。
func TestEditableFieldsExcludeKeys(t *testing.T) {
	for _, f := range []string{"弊社品番", "品番", "品名", "残", "受注"} {
		if editableOrderFields[f] {
			t.Errorf("⚠ %q を残表から書けるようにしています", f)
		}
	}
	// 毎日押すものは書けること（⚠ ここが空だと、そもそも機能していません）。
	for _, f := range []string{"出荷済み", "状態", "備考", "材料発注", "納品書発行", "請求書発行"} {
		if !editableOrderFields[f] {
			t.Errorf("%q が書けません（毎日押す列です）", f)
		}
	}
}

// TestSetOrderItemCellFindsTableByCaption は、**caption で名乗る表にも効く**ことを
// 固定します（§2.4 の移行中は両方の書き方が併存します）。
func TestSetOrderItemCellFindsTableByCaption(t *testing.T) {
	body := strings.Replace(editBody(),
		`<table data-type="`+clientOrderItemsType+`">`, `<table>`, 1)
	if _, err := setOrderItemCell(body, 0, "A-1", "備考", "", "x"); err != nil {
		t.Errorf("⚠ caption で名乗る表に効いていません: %v", err)
	}
}
