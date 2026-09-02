package cms

import (
	"database/sql"
)

// ─────────────────────────────────────────────────────────────────────────
// 索引を「読む」側の入口（③計算プラグインが業務データを引く経路）
//
// 書き込みは vocab_index.go、読み出しはここ。D-1（硬いドメイン表を全廃して
// 汎用索引へ一本化・docs/アーキテクチャとDBスキーマ.md §9）で、③計算は
// 専用テーブルではなく**この層を通して** vocab_index を読みます。
//
// **鍵の変換がこの層の仕事です。** 索引の `field` に入っているのは本文の
// 見出しの表示文字（`品番`・`数量`）で、③計算が使いたいのは機械キー
// （`item-id`・`quantity`）です。対応表は①語彙レジストリの Label ↔ Field が
// 唯一の正本なので、ここで一度だけ引き当てます——集計コードへ日本語の
// 魔法文字列を直書きすると、見出しを改名したときに告知する側と読む側がずれます
// （設計総点検⑤で一度踏んだ轍）。
//
// 宣言の無い列（自由語のタグなど）は表示文字のまま鍵になります。
// ─────────────────────────────────────────────────────────────────────────

// VocabRow は索引に載った1行分の値です。表なら1データ行、`dl` ならブロック全体
// （名前：値の組すべて）が1つの VocabRow になります。
//
// Values の鍵は、レジストリが Field を宣言していれば機械キー、無ければ
// 見出しの表示文字です。
type VocabRow struct {
	PageID  int
	BlockNo int    // 同じ形式のブロックの文書順連番
	BlockID string // 本文のブロックID（data-id）。無ければ空
	RowNo   int
	Values  map[string]string
	nums    map[string]float64 // norm_num が入った列だけ
}

// Num は列を数値として読みます。索引が数として解釈できていればその値を、
// できていなければ生テキストからの変換（vocabNumber と同じ規則）を返します。
func (r VocabRow) Num(key string) int {
	if f, ok := r.nums[key]; ok {
		return int(f)
	}
	return vocabNumber(r.Values[key])
}

// vocabRows は1ページ分の指定形式を、行ごとの値の組へ畳み直して返します。
// 並びは文書順（block_no, row_no）です。
//
// `dl`（名前：値）は行ごとに row_no が振られますが、**1ブロックが1件**なので
// block_no でまとめます。表は row_no ごとに1件です。この違いは索引の書き手
// （syncVocabTable / syncVocabDL）の作りに由来します。
func vocabRows(db ReadOnlyDB, pageID int, dataType string, perBlock bool) ([]VocabRow, error) {
	def, _ := VocabDefByType(dataType) // 未定義でもゼロ値で続行（表示文字が鍵になる）

	rows, err := db.Query(`
		SELECT block_no, block_id, row_no, field, value, norm_num
		FROM vocab_index
		WHERE page_id = ? AND data_type = ?
		ORDER BY block_no, row_no
	`, pageID, dataType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type key struct{ block, row int }
	index := map[key]int{}
	var out []VocabRow

	for rows.Next() {
		var blockNo, rowNo int
		var blockID, field, value string
		var normNum sql.NullFloat64
		if err := rows.Scan(&blockNo, &blockID, &rowNo, &field, &value, &normNum); err != nil {
			return nil, err
		}

		k := key{blockNo, rowNo}
		if perBlock {
			k.row = 0 // dl は1ブロックで1件
		}
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, VocabRow{
				PageID: pageID, BlockNo: blockNo, BlockID: blockID, RowNo: k.row,
				Values: map[string]string{}, nums: map[string]float64{},
			})
		}

		// 見出しの表示文字 → 機械キー（宣言があるときだけ）
		name := field
		if col, ok := def.columnFor(field); ok && col.Field != "" {
			name = col.Field
		}
		out[i].Values[name] = value
		if normNum.Valid {
			out[i].nums[name] = normNum.Float64
		}
	}
	return out, rows.Err()
}

// vocabTableRowsOf は表形式（1データ行＝1件）を読みます。
func vocabTableRowsOf(db ReadOnlyDB, pageID int, dataType string) ([]VocabRow, error) {
	return vocabRows(db, pageID, dataType, false)
}

// vocabBlocksOf は名前：値形式（1ブロック＝1件）を読みます。
func vocabBlocksOf(db ReadOnlyDB, pageID int, dataType string) ([]VocabRow, error) {
	return vocabRows(db, pageID, dataType, true)
}

// pagesByTag は「可変タグ `name` の値が `value` のページ」を返します。
//
// 硬いドメイン表を廃したことで、**ページ横断の突き合わせはこの逆引きになります**。
// 部材定義が部品番号でつながるように、鍵が形式の外（ページ全体のタグ）にある
// 形式で使います。索引 idx_vocab_index_field_value が効きます。
//
// 生テキスト（value）に対して引きます——正規化値ではなく生が正本だからです
// （docs/アーキテクチャとDBスキーマ.md §9.1）。
func pagesByTag(db ReadOnlyDB, name, value string) ([]int, error) {
	if name == "" || value == "" {
		return nil, nil // 空の鍵で全ページを引き当てない
	}
	rows, err := db.Query(`
		SELECT DISTINCT page_id FROM vocab_index
		WHERE data_type = 'tags' AND field = ? AND value = ?
		ORDER BY page_id
	`, name, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
