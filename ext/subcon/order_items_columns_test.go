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
		// ⚠ **並びは実物の発注書に寄せています**（`No. / 品名 / サイズ / 図面番号 /
		// 数量 / 単位 / 単価 / 金額`）——**数量のすぐ隣が単位**。
		{"弊社品番", cms.ColRef},  // ⚠ 先頭。加工中に使う番号
		{"品番", cms.ColCode},    // 顧客の言葉（実データでは図面番号が入る）
		{"品名", cms.ColText},
		{"数量", cms.ColNumber},
		{"単位", cms.ColEnum},    // ⚠ 落とすと数量の意味が変わる（個／セット）
		{"単価", cms.ColNumber},
		{"備考", cms.ColText},    // ⚠ 既存4表と同じく `状態` の手前。先方の `サイズ` もここ
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
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th>` +
		`<th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>` +
		`<tr><td>000047</td><td>P103-227-6</td><td>ブラケット</td><td>100</td>` +
		`<td>セット</td><td>390</td><td>材質変更</td><td>未着手</td></tr>` +
		`</tbody></table>`
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}

	for _, c := range []struct{ field, want string }{
		{"弊社品番", "000047"},
		// ⚠ **`セット` が残ること**——ここが落ちると、数量100が100個なのか
		// 100セットなのか分からなくなります（まれにしか出ないので、
		// **セットの行だけが黙って間違います**）。
		{"単位", "セット"},
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

// TestOrderPageDueDateIsPageTag は、**納期がページのタグになる**ことを固定します。
//
// ⚠ **行ではありません**（2026-09-20 ユーザー:「納期は、各行ではなく、**表とは別に
// 発注書の最初の方にあります**」）。書面のヘッダにあるものはページのタグになる、
// という **1文書＝1ページ**の規則どおりです。
//
// ⚠ **両方に置いてはいけません。** 同じことを2か所に書くと、どちらが正なのか
// 分からなくなり、片方だけ直したときに静かに食い違います。
func TestOrderPageDueDateIsPageTag(t *testing.T) {
	j := &orderJudgment{
		IsClientOrder: true, DocType: "order", OrderNo: "PO-1",
		DueDate: "2026-10-15",
		Items:   []orderPDFItem{{ItemNo: "A-1", ItemName: "ブラケット", Quantity: "100", Unit: "個"}},
	}
	body := buildOrderPageHTML("000001", "pdf001", j)

	if !strings.Contains(body, "<dt>"+DueDateTag+"</dt><dd>2026-10-15</dd>") {
		t.Errorf("納期がページのタグに出ていません:\n%s", body)
	}
	// **明細の列にはしない。**
	if strings.Contains(body, "<th>納期</th>") {
		t.Errorf("納期が明細の列にも出ています（2か所になります）:\n%s", body)
	}
}

// TestOrderPageRowCarriesUnit は、**単位が行を通る**ことを固定します。
//
// ⚠ 落とすと `数量: 100` が100個なのか100セットなのか分からなくなります。
// **まれにしか出ないので、セットの行だけが黙って間違います。**
func TestOrderPageRowCarriesUnit(t *testing.T) {
	j := &orderJudgment{
		IsClientOrder: true, DocType: "order", OrderNo: "PO-1",
		Items: []orderPDFItem{{ItemNo: "A-1", ItemName: "ブラケット", Quantity: "2", Unit: "セット"}},
	}
	body := buildOrderPageHTML("000001", "pdf001", j)
	if !strings.Contains(body, "<td>セット</td>") {
		t.Errorf("単位が落ちています:\n%s", body)
	}
	// ⚠ **数量の隣にあること**——見比べる人の目が滑らない並び（実物の発注書も同じ）。
	iQty := strings.Index(body, "<td>2</td>")
	iUnit := strings.Index(body, "<td>セット</td>")
	if iQty < 0 || iUnit < 0 || iUnit < iQty {
		t.Errorf("単位が数量の隣にありません:\n%s", body)
	}
}

// TestOrderPageKeepsNonDateDueDate は、**日付でない納期も残る**ことを固定します。
//
// ⚠ **実データの1通目が「最短納期」でした**（2026-09-20 ユーザー）。プロンプトが
// `YYYY-MM-DD 形式で`とだけ頼んでいたころ、Gemini は**空で返して**いました——
// 日付にできないので。**書いてあるのに何も残らない**状態です。
//
// **取り込みは情報を捨てない**（D-3）。`納期` は辞書で `date` なので、日付として
// 読めない値は**畳んだ値が付かないだけ**で、生の値は正本として残ります
// （語彙モデル §5.1「解釈できない値は併記しない。拒否もしない」）。
func TestOrderPageKeepsNonDateDueDate(t *testing.T) {
	for _, v := range []string{"最短納期", "至急", "都度指示"} {
		j := &orderJudgment{IsClientOrder: true, DocType: "order", OrderNo: "PO-1", DueDate: v}
		body := buildOrderPageHTML("000001", "pdf001", j)
		if !strings.Contains(body, "<dt>"+DueDateTag+"</dt><dd>"+v+"</dd>") {
			t.Errorf("日付でない納期 %q が落ちています:\n%s", v, body)
		}
	}
}
