package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
)

// **品番から製造製品ページを特定し、受注明細の `弊社品番` を埋める**（2026-09-21）。
//
// ユーザー:「弊社品番を入れるために、品番から製造製品ページを特定する必要があります」
// 「**できれば機械的にやってほしいです**」。
//
// ⚠ **機械が黙って書く機能です。** 誤って別の製品に結ぶと**現場が別物を作ります**
// ——取り返しが利かない側なので、歯止めの番人を厚く置きます。

// withProductCodeTags は照合に使うタグ名を差し替え、試験の後で戻します。
func withProductCodeTags(t *testing.T, names ...string) {
	t.Helper()
	stagesMu.Lock()
	old := productCodeTags
	productCodeTags = names
	stagesMu.Unlock()
	t.Cleanup(func() {
		stagesMu.Lock()
		productCodeTags = old
		stagesMu.Unlock()
	})
}

// orderBody は受注明細を1つ持つ本文を組みます（`弊社品番` は空）。
func orderBody(rows ...[2]string) string {
	b := `<h1>受注 A-1</h1><table data-type="` + clientOrderItemsType + `">` +
		`<caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`
	for _, r := range rows {
		b += `<tr><td>` + r[0] + `</td><td>` + r[1] + `</td><td>ブラケット</td><td>100</td></tr>`
	}
	return b + `</tbody></table>`
}

// TestFillOurItemNoFillsMatchingRow は、**品番が一致した行が埋まる**ことを固定します。
func TestFillOurItemNoFillsMatchingRow(t *testing.T) {
	body := orderBody([2]string{"", "K120-01-211"}, [2]string{"", "K120-01-212"})

	got, n := fillOurItemNo(body, "K120-01-211", "000036")
	if n != 1 {
		t.Fatalf("埋めた行が %d です（1を期待）:\n%s", n, got)
	}
	if !strings.Contains(got, "<td>000036</td><td>K120-01-211</td>") {
		t.Errorf("一致した行が埋まっていません:\n%s", got)
	}
	// ⚠ **別の品番の行は触らない。**
	if strings.Contains(got, "<td>000036</td><td>K120-01-212</td>") {
		t.Errorf("⚠ 一致していない行まで埋めています:\n%s", got)
	}
}

// TestFillOurItemNoKeepsHumanValue は、⚠ **人が入れた値を上書きしない**ことを
// 固定します。
//
// ⚠ 機械の推測より人の判断が上、という線引きはこのプロジェクトで一貫しています。
// 上書きすると、**人が直した結び先が整理のたびに戻ります**——直したつもりが直らない、
// いちばん腹の立つ壊れ方です。
func TestFillOurItemNoKeepsHumanValue(t *testing.T) {
	body := orderBody([2]string{"000099", "K120-01-211"})

	got, n := fillOurItemNo(body, "K120-01-211", "000036")
	if n != 0 {
		t.Errorf("人が入れた値を書き換えています: %d行\n%s", n, got)
	}
	if !strings.Contains(got, "<td>000099</td>") {
		t.Errorf("人の値が消えています:\n%s", got)
	}
}

// TestFillOurItemNoFoldsCode は、**畳んだ一致で当たる**ことを固定します。
//
// `品番` は `code` 型なので、大小・全角・空白・ハイフンの種類（`_`・長音・ダッシュ）の
// 揺れを越えます。
//
// ⚠ **区切りの有無は畳みません**（`NormalizeCode` は空白を消すだけで、ハイフンを
// 補いません）。`K120 01 211` は `K12001211` になり、`K120-01-211` には**当たりません**。
// これは意図した線引きです——補うと `AB 12` と `AB-12` と `AB12` が全部1つになり、
// **別の品番どうしが衝突します**。
//
// ⚠ **実務上の限界として覚えておくこと**: 先方が発注書でハイフンを空白に置き換えて
// 書いてくると、その行は自動では埋まりません（人が結べば覚えます）。
func TestFillOurItemNoFoldsCode(t *testing.T) {
	for _, cell := range []string{
		"k120-01-211",     // 小文字
		"Ｋ１２０－０１－２１１",     // 全角
		" K120-01-211 ",   // 前後の空白
		"K120 - 01 - 211", // ハイフンの周りの空白
		"K120_01_211",     // アンダースコア（実データにある形）
	} {
		body := orderBody([2]string{"", cell})
		if _, n := fillOurItemNo(body, "K120-01-211", "000036"); n != 1 {
			t.Errorf("%q に当たっていません", cell)
		}
	}
	// ⚠ **区切りを落とした形には当たりません**（当ててはいけません）。
	for _, cell := range []string{"K120 01 211", "K12001211"} {
		body := orderBody([2]string{"", cell})
		if _, n := fillOurItemNo(body, "K120-01-211", "000036"); n != 0 {
			t.Errorf("⚠ 区切りの無い %q に当てています（別の品番と衝突します）", cell)
		}
	}
}

// TestFillOurItemNoFindsTableByCaption は、**caption で名乗る表にも効く**ことを
// 固定します。
//
// ⚠ 形式の宣言は `data-type` から見える文字へ移る途中で併存します（§2.4）。
// 片方しか見ないと、**caption へ移した日に黙って埋まらなくなります**——エラーは
// 出ないので、気づくのは「いつまでも空欄だ」と誰かが思ったときです。
func TestFillOurItemNoFindsTableByCaption(t *testing.T) {
	body := `<h1>受注 A-1</h1><table><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th></tr>` +
		`<tr><td></td><td>K120-01-211</td></tr></tbody></table>`

	if _, n := fillOurItemNo(body, "K120-01-211", "000036"); n != 1 {
		t.Errorf("⚠ caption で名乗る表に効いていません:\n%s", body)
	}
}

// TestFillOurItemNoNeedsBothColumns は、**列が揃わなければ触らない**ことを固定します。
func TestFillOurItemNoNeedsBothColumns(t *testing.T) {
	body := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><tbody>` +
		`<tr><th>品番</th><th>品名</th></tr>` +
		`<tr><td>K120-01-211</td><td>ブラケット</td></tr></tbody></table>`

	got, n := fillOurItemNo(body, "K120-01-211", "000036")
	if n != 0 || got != body {
		t.Errorf("弊社品番の列が無いのに書き換えています:\n%s", got)
	}
}

// TestFillOurItemNoLeavesOtherTablesAlone は、⚠ **原本の写しを触らない**ことを
// 固定します。
//
// ⚠ 原本は**先方が書いたまま**が値です。機械が1文字でも足すと、OCR を見比べる
// という原本の唯一の役目が壊れます。
func TestFillOurItemNoLeavesOtherTablesAlone(t *testing.T) {
	body := `<h1>受注</h1><table><caption>` + sourceTableCaption + `</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th></tr>` +
		`<tr><td></td><td>K120-01-211</td></tr></tbody></table>`

	got, n := fillOurItemNo(body, "K120-01-211", "000036")
	if n != 0 || got != body {
		t.Errorf("⚠ 原本の写しを書き換えています:\n%s", got)
	}
}

// TestProductCodeTagsAreDeclared は、**照合するタグ名が設定から来る**ことを
// 固定します。
//
// ⚠ コードに名前を直書きすると、`部品番号` で発注してくる客先が現れた日に
// **Goを書き直さないと結べません**（「運用者が語彙を足せる」要件・2026-08-26）。
func TestProductCodeTagsAreDeclared(t *testing.T) {
	withProductCodeTags(t) // 空にする
	if got := ProductPagesByCode("K120-01-211"); len(got) != 0 {
		t.Errorf("タグ名が1つも無いのに引いています: %v", got)
	}
}

// TestDrawingPageHasItemNoSlot は、**図面ブロックに `品番` の欄が出る**ことを
// 固定します。
//
// ⚠ **空欄で出すのが肝**です。図面の無い製品ではここが唯一の手掛かりになり、
// **書く場所が見えていれば人が埋めます**。⚠ そして**解析は値を推測しません**
// ——「何が品番か」は取引先ごとの取り決めで、図面には書かれていないからです。
func TestDrawingPageHasItemNoSlot(t *testing.T) {
	j := &orderJudgment{DocType: "drawing", DrawingNo: "K120-01-211", DrawingName: "ブラケット"}
	body := buildProductPageHTML("000001", "pdf001", j, nil)

	if !strings.Contains(body, "<dt>"+ItemNoTag+"</dt>") {
		t.Fatalf("品番の欄がありません:\n%s", body)
	}
	// ⚠ **図面番号を写していないこと。** 写すと、同じ文字列が2つのタグに載り、
	// 片方を直すともう片方が古くなります。
	at := strings.Index(body, "<dt>"+ItemNoTag+"</dt>")
	if strings.Contains(body[at:at+60], "K120-01-211") {
		t.Errorf("⚠ 図面番号を品番へ写しています（推測はしない約束です）:\n%s", body[at:at+60])
	}
	// 索引にも空欄として載る形（`<dd><br/></dd>`）であること。
	if !strings.Contains(body[at:at+40], "<dd>") {
		t.Errorf("欄の形が違います:\n%s", body[at:at+40])
	}
}

// TestOrderItemColumnLabelsMatchConstants は、⚠ **定数と宣言がずれていない**ことを
// 固定します。
//
// ⚠ 本文の書き換えも索引も**見出しの表示文字**で引くので、`vocab.go` の `Label` を
// 改名して定数を直し忘れると、**エラーにならず黙って埋まらなくなります**。
func TestOrderItemColumnLabelsMatchConstants(t *testing.T) {
	def, ok := cms.VocabDefByType(clientOrderItemsType)
	if !ok {
		t.Fatal("受注明細の宣言がありません")
	}
	found := map[string]bool{}
	for _, c := range def.Columns {
		found[c.Label] = true
	}
	for _, want := range []string{OurItemNoTag, ItemNoTag} {
		if !found[want] {
			t.Errorf("⚠ 定数 %q に当たる列が宣言にありません（黙って埋まらなくなります）", want)
		}
	}
}
