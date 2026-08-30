package cms

import (
	"testing"

	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// テスト用: 汎用索引へ直接種を蒔くヘルパ
//
// 硬いドメイン表を廃した（D-1）ので、③計算のテストは vocab_index に種を蒔きます。
// **鍵は本文の見出しの表示文字**（`品番`・`数量`）です——索引に入るのがそれだから
// で、機械キー（item-id）で蒔くと本物と違うものを試すことになります。
//
// 書き込みは本番と同じ insertVocabEntry を通します（正規化・norm_num の付与まで
// 同じ経路を通る）。列型はレジストリ宣言 → 語→型推論の順で解決します。
// ─────────────────────────────────────────────────────────────────────────

// seedVocabTable は表形式の索引行を蒔きます（rows の1要素＝表の1データ行）。
func seedVocabTable(t *testing.T, pageID int, dataType string, blockNo int, rows ...map[string]string) {
	t.Helper()
	seedVocab(t, pageID, dataType, blockNo, rows, false)
}

// seedVocabBlock は名前：値形式（dl）の索引行を蒔きます。1ブロック＝1件。
func seedVocabBlock(t *testing.T, pageID int, dataType string, blockNo int, fields map[string]string) {
	t.Helper()
	seedVocab(t, pageID, dataType, blockNo, []map[string]string{fields}, true)
}

// seedPageTag はページ横断メタ（可変タグ）を1対蒔きます。
func seedPageTag(t *testing.T, pageID int, name, value string) {
	t.Helper()
	seedVocabBlock(t, pageID, "tags", 0, map[string]string{name: value})
}

func seedVocab(t *testing.T, pageID int, dataType string, blockNo int, rows []map[string]string, asDL bool) {
	t.Helper()
	def, _ := VocabDefByType(dataType)
	tx, err := database.DB.Begin()
	if err != nil {
		t.Fatalf("索引の種まき（Begin）エラー: %v", err)
	}
	rowNo := 0
	for i, row := range rows {
		if !asDL {
			rowNo = i
		}
		for label, value := range row {
			typ := InferColumnType(label)
			if col, ok := def.columnFor(label); ok {
				typ = col.Type
			}
			if err := insertVocabEntry(tx, pageID, dataType, blockNo, "", rowNo, label, typ, value); err != nil {
				tx.Rollback()
				t.Fatalf("索引の種まきエラー: %v", err)
			}
			if asDL {
				rowNo++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("索引の種まき（Commit）エラー: %v", err)
	}
}

// ── 索引を読むテスト用ヘルパ ─────────────────────────────────────────────

// countVocabBlocks は指定形式のブロック数を数えます（業務文書ブロックの
// ヘッダなら「発注書が何件あるか」に当たります）。
func countVocabBlocks(t *testing.T, pageID int, dataType string) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(DISTINCT block_no) FROM vocab_index WHERE page_id = ? AND data_type = ?`,
		pageID, dataType).Scan(&n); err != nil {
		t.Fatalf("索引（%s）を数えられません: %v", dataType, err)
	}
	return n
}

// countVocabDataRows は表形式のデータ行数を数えます（ブロックをまたいで合計）。
func countVocabDataRows(t *testing.T, pageID int, dataType string) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM (SELECT DISTINCT block_no, row_no FROM vocab_index
		 WHERE page_id = ? AND data_type = ?)`, pageID, dataType).Scan(&n); err != nil {
		t.Fatalf("索引（%s）の行を数えられません: %v", dataType, err)
	}
	return n
}

// vocabValuesOf は指定形式・指定見出しの値を文書順で返します。
func vocabValuesOf(t *testing.T, pageID int, dataType, field string) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT value FROM vocab_index WHERE page_id = ? AND data_type = ? AND field = ?
		 ORDER BY block_no, row_no`, pageID, dataType, field)
	if err != nil {
		t.Fatalf("索引（%s.%s）のクエリエラー: %v", dataType, field, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("索引の読み取りエラー: %v", err)
		}
		out = append(out, v)
	}
	return out
}

// vocabValueOf は値を1つだけ返します（無ければ空文字）。
func vocabValueOf(t *testing.T, pageID int, dataType, field string) string {
	t.Helper()
	if vs := vocabValuesOf(t, pageID, dataType, field); len(vs) > 0 {
		return vs[0]
	}
	return ""
}
