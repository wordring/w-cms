package cms

import (
	"testing"

	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// テスト用: 汎用索引を読むヘルパ
//
// ⚠ 種を蒔く側のヘルパ（seedVocab 等）は 2026-09-21 に消しました——呼び手がゼロでした。
// 同じ経路を通る）。列型はレジストリ宣言 → 語→型推論の順で解決します。
// ─────────────────────────────────────────────────────────────────────────


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
