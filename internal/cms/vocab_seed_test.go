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
