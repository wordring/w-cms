package cms

import "testing"

// 節の見出しと `<caption>` の**両方**が同じ形式を名乗る表が、
// ⚠ **二重に索引されない**ことを固定します（2026-09-21 に実データで踏んだ）。
//
// ⚠ **エラーは出ません。** 手配数も原価も**すべて倍**になり、画面は平然と嘘を出します。
// caption で形式を宣言できるようにした 2026-09-20 から在った穴で、実データに caption 付きの
// 表が無かったので誰も踏んでいませんでした。
func TestCaptionedTableInMatchingSectionIsIndexedOnce(t *testing.T) {
	db := newTestFileDB(t)

	RegisterVocab(VocabDef{
		Type: "dup-check", DisplayName: "二重検査", Category: "試験",
		Element: "table",
		Columns: []VocabColumn{
			{Field: "name", Label: "品名", Type: ColText},
			{Field: "qty", Label: "個数", Type: ColNumber},
		},
	})

	const id = "000501"
	body := `<h1>二重検査</h1>` +
		`<section><h2>二重検査</h2>` +
		`<table><caption>二重検査</caption><tbody>` +
		`<tr><th>品名</th><th>個数</th></tr>` +
		`<tr><td>ブラケット</td><td>3</td></tr>` +
		`</tbody></table></section>`
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var blocks, rows int
	err := db.QueryRow(
		`SELECT COUNT(DISTINCT block_no), COUNT(*) FROM vocab_index
		  WHERE page_id = 501 AND data_type = 'dup-check'`).Scan(&blocks, &rows)
	if err != nil {
		t.Fatalf("集計エラー: %v", err)
	}
	if blocks != 1 {
		t.Errorf("⚠ 同じ表が %d ブロックとして入っています（1のはず）——集計がすべて倍になります", blocks)
	}
	// 1行 × 2列 = 2件だけ。
	if rows != 2 {
		t.Errorf("⚠ 索引の件数が %d です（2のはず・1行×2列）", rows)
	}
}
