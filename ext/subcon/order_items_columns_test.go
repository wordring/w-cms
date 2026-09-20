package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 受注明細の列（2026-09-20）。
//
// ユーザー:「加工中に番号を使うので、受注の表に**弊社品番として製造製品のページIDを
// 入れたい**です」「ついでに備考欄もお願いします」。納期は同日の決定
// （[docs/【考察】発注書から受注明細へ.md] §2.3）で、あとから足すと本文の作り直しが
// 2回になるので一緒に入れました。
//
// ⚠ **`弊社品番` と `品番` は別の列です。** `品番` は**顧客の言葉**（この客先では
// 図面番号が入る）、`弊社品番` は**弊社の識別**（ページ番号）。同じ列に混ぜると、
// 顧客の品番で問い合わせが来たときに引けません。

// TestOrderItemColumns は、**列の宣言**を固定します。
func TestOrderItemColumns(t *testing.T) {
	def, ok := cms.VocabDefByType("client-order-items")
	if !ok {
		t.Fatal("受注明細の形式が登録されていません")
	}
	want := []struct {
		label string
		typ   cms.ColumnType
	}{
		{"弊社品番", cms.ColRef},   // ⚠ 先頭。加工中に使う番号
		{"品番", cms.ColCode},     // 顧客の言葉（実データでは図面番号が入る）
		{"品名", cms.ColText},
		{"単価", cms.ColNumber},
		{"数量", cms.ColNumber},
		{"納期", cms.ColDate},
		{"備考", cms.ColText},     // ⚠ 既存4表と同じく `状態` の手前
		{"状態", cms.ColEnum},
	}
	if len(def.Columns) != len(want) {
		t.Fatalf("列の数が違います: %d（%d を期待）", len(def.Columns), len(want))
	}
	for i, w := range want {
		got := def.Columns[i]
		if got.Label != w.label {
			t.Errorf("%d列目が %q です（%q を期待）", i, got.Label, w.label)
		}
		if got.Type != w.typ {
			t.Errorf("%q の型が %q です（%q を期待）", got.Label, got.Type, w.typ)
		}
	}
}

// TestOrderPageHeaderMatchesDeclaration は、**本文の見出しが宣言と揃っている**ことを
// 固定します。
//
// ⚠ **これが割れると、足した列がどこからも読めません。** 索引は見出しの表示文字で
// 引くので、宣言だけ増やして本文の `<th>` が古いままだと**エラーも出ずに欠けます**。
func TestOrderPageHeaderMatchesDeclaration(t *testing.T) {
	j := &orderJudgment{IsClientOrder: true, DocType: "order", OrderNo: "PO-1"}
	body := buildOrderPageHTML("000001", "pdf001", j)

	for _, c := range clientOrderItemColumns() {
		if !strings.Contains(body, "<th>"+c.Label+"</th>") {
			t.Errorf("見出しに %q がありません:\n%s", c.Label, body)
		}
	}
}

// TestOrderPageRowMatchesHeaderWidth は、**明細の行が見出しと同じ列数**であることを
// 固定します。
//
// ⚠ 列を足したとき `<td>` を足し忘れると、**値が1つずつ横にずれます**——品名の欄に
// 単価が入るような壊れ方で、しかも索引は見出しの順で鍵を付けるので**黙って別の列に
// 入ります**。
func TestOrderPageRowMatchesHeaderWidth(t *testing.T) {
	j := &orderJudgment{
		IsClientOrder: true, DocType: "order", OrderNo: "PO-1",
		Items: []orderPDFItem{{ItemNo: "A-1", ItemName: "ブラケット", Price: "390", Quantity: "100"}},
	}
	body := buildOrderPageHTML("000001", "pdf001", j)

	head := strings.Count(body, "<th>")
	rows := strings.SplitN(body, "</tr>", 3)
	if len(rows) < 3 {
		t.Fatalf("明細の行がありません:\n%s", body)
	}
	cells := strings.Count(rows[1], "<td>")
	if cells != head {
		t.Errorf("行の列数が見出しと違います: 見出し %d / 行 %d\n%s", head, cells, body)
	}
}

// TestOrderItemsIndexNewColumns は、足した列が**索引に載る**ことを固定します。
//
// ⚠ 宣言しただけでは足りません——本文に見出しと値が並んで初めて索引に入ります。
func TestOrderItemsIndexNewColumns(t *testing.T) {
	const id = "000081"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>単価</th>` +
		`<th>数量</th><th>納期</th><th>備考</th><th>状態</th></tr>` +
		`<tr><td>000047</td><td>P103-227-6</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td>2026-10-15</td><td>材質変更</td><td>未着手</td></tr>` +
		`</tbody></table>`
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}

	for _, c := range []struct{ field, want string }{
		{"弊社品番", "000047"},
		{"納期", "2026-10-15"},
		{"備考", "材質変更"},
	} {
		var got string
		err := database.DB.QueryRow(
			`SELECT value FROM vocab_index WHERE page_id = ? AND data_type = ? AND field = ?`,
			81, "client-order-items", c.field).Scan(&got)
		if err != nil || got != c.want {
			t.Errorf("%s が索引に入っていません: %q（%v）", c.field, got, err)
		}
	}
}
