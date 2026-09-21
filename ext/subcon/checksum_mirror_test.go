package subcon

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 検算の結果が**受注ページに出る**ことを固定します（2026-09-20）。
//
// ⚠ **鏡は空振りしやすい**ところです——引き金が立たなければ、番人が「合っている」を
// 見ているつもりで**ただの静けさ**を見ます。だからここでは「⚠ が出ること」だけでなく
// **「合っているときは合っていると出ること」**も見ます。静けさと成功を区別します。

// orderPageWith は原本・タグつきの受注ページの本文を組みます。
func orderPageWith(sub, tax, total string, rows ...[]string) string {
	j := &orderJudgment{
		IsClientOrder: true, DocType: "order",
		OrderNo: "250715-304", Customer: "南北スポーツ機械",
		OrderDate: "2026-09-16", DueDate: "2026-10-15",
		Subtotal: sub, Tax: tax, Total: total,
		SourceTable: poTable(rows...),
		Items: []orderPDFItem{
			{ItemNo: "K120-1", ItemName: "ブラケット", Quantity: "100", Unit: "個", Price: "390"},
		},
	}
	return buildOrderPageHTML("000001", "pdf001", j)
}

// render は鏡を通した表示用のHTMLを返します。
func render(t *testing.T, body string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/000001", nil)
	req = auth.WithUser(req, &auth.User{Username: "root", IsAdmin: true})
	return cms.RenderComputedViews(req, 1, body)
}

// TestChecksumMirrorSaysNothingIsWrong は、**合っているときに合っていると出る**ことを
// 固定します。
//
// ⚠ **この番人がいちばん大事です。** 「⚠ が出ない」だけを見る試験は、鏡が一度も
// 走っていなくても通ります——静けさと成功が区別できません。
func TestChecksumMirrorSaysNothingIsWrong(t *testing.T) {
	body := orderPageWith("42000", "4200", "46200",
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
		[]string{"2", "カバー", "t1.6", "K120-2", "2", "セット", "1500", "3000"},
	)
	got := render(t, body)

	if !strings.Contains(got, "✓ 検算: 合っています") {
		// ⚠ **`✓` は飾りではありません。** 合格は薄い緑、疑わしいは薄い赤ですが、
		// この2色は赤緑色覚でいちばん近づきます（ΔE は正常視 24.4 に対し
		// 2型 11.1・1型 9.5＝「別の色」の目安 10 の境目）。**印の形**が最後の
		// 見分けです——落とすと、
		// 色を見分けられない人には2つの行が同じに見えます。
		t.Fatalf("⚠ 鏡が走っていないか、何も言っていません（`✓` も見ます）:\n%s", got)
	}
	if !strings.Contains(got, "明細2行") {
		t.Errorf("何行を検算したのか出ていません:\n%s", got)
	}
	if strings.Contains(got, "checksum-ng") {
		t.Errorf("合っているのに ⚠ を出しています:\n%s", got)
	}
}

// TestChecksumMirrorWarnsOnDroppedRow は、⚠ **落丁が画面に出る**ことを固定します。
func TestChecksumMirrorWarnsOnDroppedRow(t *testing.T) {
	// 紙は2行ぶん（小計42000）だが、1行しか読めなかった。
	body := orderPageWith("42000", "4200", "46200",
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
	)
	got := render(t, body)

	if !strings.Contains(got, "checksum-ng") {
		t.Fatalf("⚠ 落丁が画面に出ていません:\n%s", got)
	}
	if !strings.Contains(got, "抜けて") || !strings.Contains(got, "3000") {
		t.Errorf("差額と落丁の手がかりが出ていません:\n%s", got)
	}
}

// TestChecksumMirrorSitsInTheTable は、⚠ **足元の行が表の中の `<tfoot>` である**
// ことを固定します。
//
// ⚠ `<p>` や `<div>` を表の中へ足すと、**HTMLパーサが表の外（手前）へ追い出します**
// ——明細の上に文字が飛び出す、気づきにくい壊れ方になります。
func TestChecksumMirrorSitsInTheTable(t *testing.T) {
	got := render(t, orderPageWith("42000", "4200", "46200",
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
		[]string{"2", "カバー", "t1.6", "K120-2", "2", "セット", "1500", "3000"},
	))

	at := strings.Index(got, "order-checksum")
	if at < 0 {
		t.Fatalf("足元の行がありません:\n%s", got)
	}
	// ⚠ **弊社の表の中**にあること（原本の `</details>` より後ろ）。
	if end := strings.Index(got, "</details>"); end > at {
		t.Errorf("⚠ 検算の行が原本の畳みの中に入っています（開くまで読まれません）:\n%s", got)
	}
	if !strings.Contains(got[:at], "<tfoot") {
		t.Errorf("⚠ `tfoot` の外に出ています（表の手前へ追い出されています）:\n%s", got)
	}
	// 横いっぱいに伸びていること（列数ぶん）。
	if !strings.Contains(got, `colspan="10"`) {
		t.Errorf("列数ぶん伸びていません（10列を期待）:\n%s", got)
	}
}

// TestChecksumMirrorStaysSilentWithoutMaterial は、**材料が無ければ黙る**ことを
// 固定します。
//
// ⚠ 手で作った受注ページに毎回「検算できません」と出ると、ただの雑音です。
func TestChecksumMirrorStaysSilentWithoutMaterial(t *testing.T) {
	body := `<h1>受注</h1><dl data-type="tags"><dt>発注書番号</dt><dd>A-1</dd></dl>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>数量</th></tr>` +
		`<tr><td></td><td>K1</td><td>3</td></tr></tbody></table>`

	if got := render(t, body); strings.Contains(got, "order-checksum") {
		t.Errorf("材料が無いのに検算の行を出しています:\n%s", got)
	}
}

// TestChecksumMirrorIsNotSaved は、⚠ **検算の行が本文に残らない**ことを固定します。
//
// ⚠ 焼き込むと、人が数字を直しても ⚠ が残ります（逆に、直した結果が間違っていても
// ⚠ が出ません）。**鏡は本文を変えない**——これが自動で消える仕掛けの土台です。
func TestChecksumMirrorIsNotSaved(t *testing.T) {
	body := orderPageWith("99999", "0", "99999",
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
	)
	if strings.Contains(body, "order-checksum") || strings.Contains(body, "<tfoot") {
		t.Errorf("⚠ 検算の行が本文に焼き込まれています:\n%s", body)
	}
	// 鏡を2回通しても増えない（前回のクロームを落としている）。
	once := render(t, body)
	twice := render(t, once)
	if strings.Count(twice, "order-checksum") != strings.Count(once, "order-checksum") {
		t.Errorf("鏡を通すたびに行が増えています:\n%s", twice)
	}
}

// TestChecksumMirrorReadsTagsNotHeaders は、**小計をページのタグから読む**ことを
// 固定します。
//
// ⚠ 素の `dl`（業務ブロックのヘッダ）に同じ名前があっても読みません——
// 「タグと表だけがDBに入る」の線引きと揃えます。
func TestChecksumMirrorReadsTagsNotHeaders(t *testing.T) {
	body := `<h1>受注</h1>` +
		`<dl><dt>小計</dt><dd>1</dd></dl>` + // ⚠ 素の dl。読んではいけない
		`<table><caption>` + sourceTableCaption + `</caption><tbody>` +
		`<tr><th>品名</th><th>数量</th><th>単価</th><th>金額</th></tr>` +
		`<tr><td>ブラケット</td><td>100</td><td>390</td><td>39000</td></tr>` +
		`</tbody></table>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>数量</th></tr><tr><td>K1</td><td>100</td></tr></tbody></table>`

	got := render(t, body)
	if strings.Contains(got, "小計は 1 です") {
		t.Errorf("⚠ 素の dl をページのタグとして読んでいます:\n%s", got)
	}
	// 小計のタグが無いので、行の検算だけが通って「合っています」になる。
	if !strings.Contains(got, "検算: 合っています") {
		t.Errorf("行の検算が働いていません:\n%s", got)
	}
}
