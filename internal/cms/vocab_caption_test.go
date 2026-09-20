package cms

import "testing"

// **表は自分の `<caption>` で形式を名乗れます**（2026-09-20 ユーザー:「見た儘が全ての
// 原則から言って、data-type より表の見出し（caption）で検索できるのが筋では？」）。
//
// ⚠ **`data-type` へ寄ったのは意図した判断ではなく、副作用でした。** 受注ページは
// もともと節の機能見出しで宣言していましたが、2026-09-18 に「1文書1ページ」で節を
// やめたとき見える印が消え、`data-type` が肩代わりしていました。

// TestCaptionDeclaresVocabType は、**caption の言葉で形式が決まる**ことを固定します。
func TestCaptionDeclaresVocabType(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>検査</h1>` +
		`<table><caption>検査記録</caption><tbody>` +
		`<tr><th>品番</th><th>判定</th></tr>` +
		`<tr><td>A1</td><td>合格</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000081", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 81)
	if len(rows) == 0 {
		t.Fatalf("caption で宣言した表が索引に入っていません")
	}
	for _, r := range rows {
		if r.dataType != "inspection-record" {
			t.Errorf("caption から形式が解決していません: %+v", r)
		}
	}
	// enum の宣言も効いている（＝**定義ごと**引けている。名前だけの一致ではない）。
	if r, ok := findVocabRow(rows, 0, "判定"); !ok || !r.norm.Valid || r.norm.String != "合格" {
		t.Errorf("列の宣言が効いていません: %+v", r)
	}
}

// TestDataTypeBeatsCaption は、**属性が caption に勝つ**ことを固定します。
//
// ⚠ **既存の本文を1枚も壊さないための線**です。`data-type` の本文と caption の本文は
// しばらく併存します——新しく書くものは caption、`data-type` は古い本文のための互換。
func TestDataTypeBeatsCaption(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>混在</h1>` +
		`<table data-type="inspection-record"><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>判定</th></tr>` +
		`<tr><td>A1</td><td>合格</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000082", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	for _, r := range queryVocabRows(t, 82) {
		if r.dataType != "inspection-record" {
			t.Errorf("属性が caption に負けています: %+v", r)
		}
	}
}

// TestUnregisteredCaptionIsInert は、**登録されていない caption はただの表題**である
// ことを固定します（節の機能見出しと同じ規則）。
//
// ⚠ これが効かないと、**普通の文章の表に名前を付けただけでDBに入ります**。
// 表題は誰でも付けるものなので、オプトインの原則（素の文書は索引しない）が崩れます。
func TestUnregisteredCaptionIsInert(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>メモ</h1>` +
		`<table><caption>今週やったこと</caption><tbody>` +
		`<tr><th>日</th><th>内容</th></tr>` +
		`<tr><td>2026-09-20</td><td>段取り</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000083", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if rows := queryVocabRows(t, 83); len(rows) != 0 {
		t.Errorf("登録されていない caption の表が索引に入っています: %+v", rows)
	}
}

// TestNestedTableCaptionDoesNotLeak は、**入れ子の表の caption を外側が名乗らない**
// ことを固定します（`functionHeading` が入れ子の節の見出しを拾わないのと同じ理由）。
func TestNestedTableCaptionDoesNotLeak(t *testing.T) {
	setupSaveTest(t)

	// ⚠ **外側にもデータ行を持たせます。** 見出し1行だけの表は `syncVocabTable` が
	// 「索引に載せる値が無い」として落とすので、**外側が型を名乗っても何も起きず、
	// 守りを外しても通る試験**になります（最初そう書いて変異試験で気づきました）。
	body := `<h1>入れ子</h1>` +
		`<table><tbody>` +
		`<tr><th>外側の列</th></tr>` +
		`<tr><td>外側の値</td></tr>` +
		`<tr><td>` +
		`<table><caption>検査記録</caption><tbody>` +
		`<tr><th>品番</th></tr><tr><td>A1</td></tr>` +
		`</tbody></table>` +
		`</td></tr></tbody></table>`
	if err := SyncIndex("000084", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 外側は無名の表なので索引されない。内側だけが `検査記録` として載る。
	for _, r := range queryVocabRows(t, 84) {
		if r.field == "外側の列" {
			t.Errorf("外側の表が内側の caption を名乗っています: %+v", r)
		}
	}
}
