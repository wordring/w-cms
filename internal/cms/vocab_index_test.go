package cms

import (
	"database/sql"
	"testing"

	"w-cms/internal/database"
)

// vocabRow は検証用に vocab_index の1行を読み取った形です。
type vocabRow struct {
	dataType string
	blockNo  int
	blockID  string
	rowNo    int
	field    string
	value    string
	norm     sql.NullString
}

func queryVocabRows(t *testing.T, pageID int) []vocabRow {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT data_type, block_no, block_id, row_no, field, value, norm_value
		 FROM vocab_index WHERE page_id = ? ORDER BY data_type, block_no, row_no, field`, pageID)
	if err != nil {
		t.Fatalf("vocab_indexのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []vocabRow
	for rows.Next() {
		var r vocabRow
		if err := rows.Scan(&r.dataType, &r.blockNo, &r.blockID, &r.rowNo, &r.field, &r.value, &r.norm); err != nil {
			t.Fatalf("Scanエラー: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func findVocabRow(rows []vocabRow, rowNo int, field string) (vocabRow, bool) {
	for _, r := range rows {
		if r.rowNo == rowNo && r.field == field {
			return r, true
		}
	}
	return vocabRow{}, false
}

// TestVocabIndexTable は <table data-type> が縦持ちの汎用索引へ同期されることを
// 検証します（語彙モデル §5.1 の記録だけの形式: 鍵＝見出しテキスト・型＝推論）。
func TestVocabIndexTable(t *testing.T) {
	setupSaveTest(t)

	const id = "000030"
	body := `<h1>検査のページ</h1>` +
		`<table data-type="inspection-record" data-id="ab12">` +
		`<tr><th>品番</th><th>判定</th><th>検査日</th></tr>` +
		`<tr><td>SHAFT-01</td><td>合格</td><td>2026/8/10</td></tr>` +
		`<tr><td>SHAFT-02</td><td>不合格</td><td>2026-08-11</td></tr>` +
		`</table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 30)
	if len(rows) != 6 {
		t.Fatalf("索引の件数が期待と異なります: got %d want 6 (%+v)", len(rows), rows)
	}

	// 鍵は見出しテキスト、値は生テキスト
	r, ok := findVocabRow(rows, 0, "品番")
	if !ok || r.value != "SHAFT-01" {
		t.Errorf("行0の品番が索引されていません: %+v", rows)
	}
	if r.blockID != "ab12" {
		t.Errorf("block_id が保持されていません: %q", r.blockID)
	}

	// 検査日はレジストリ宣言（date）により正規化値が併記される
	r, ok = findVocabRow(rows, 0, "検査日")
	if !ok {
		t.Fatalf("行0の検査日が索引されていません: %+v", rows)
	}
	if r.value != "2026/8/10" {
		t.Errorf("生テキストが正本のまま入っていません: %q", r.value)
	}
	if !r.norm.Valid || r.norm.String != "2026-08-10" {
		t.Errorf("検査日の正規化値が併記されていません: %+v", r.norm)
	}

	// 判定（enum）は正規化しない
	r, _ = findVocabRow(rows, 1, "判定")
	if r.norm.Valid {
		t.Errorf("enum 列に正規化値が入っています: %+v", r.norm)
	}
}

// TestVocabIndexFieldAndTypeOverride は鍵と型の決定順序を検証します:
// 鍵＝data-field 優先、型＝th の data-type 明示 > 推論辞書。
func TestVocabIndexFieldAndTypeOverride(t *testing.T) {
	setupSaveTest(t)

	const id = "000031"
	body := `<table data-type="delivery-note">` + // レジストリ未定義の形式でも索引される
		`<tr><th data-field="item-id">品番</th><th>単価</th><th data-type="date">出荷</th></tr>` +
		`<tr><td>GEAR-9</td><td>¥8,000</td><td>2026年8月1日</td></tr>` +
		`</table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 31)

	// 鍵: data-field があればその値（見出しテキスト「品番」ではなく item-id）
	if _, ok := findVocabRow(rows, 0, "item-id"); !ok {
		t.Errorf("data-field の鍵が使われていません: %+v", rows)
	}
	if _, ok := findVocabRow(rows, 0, "品番"); ok {
		t.Errorf("data-field があるのに見出しテキストが鍵になっています: %+v", rows)
	}

	// 型: 「単価」は推論辞書で number → 正規化値 8000
	r, ok := findVocabRow(rows, 0, "単価")
	if !ok || !r.norm.Valid || r.norm.String != "8000" {
		t.Errorf("単価の正規化値が併記されていません: %+v", r)
	}

	// 型: th data-type="date" の明示（「出荷」は辞書に無い語）
	r, ok = findVocabRow(rows, 0, "出荷")
	if !ok || !r.norm.Valid || r.norm.String != "2026-08-01" {
		t.Errorf("th data-type の型明示が効いていません: %+v", r)
	}
}

// TestVocabIndexDL は <dl data-type> の索引を検証します
// （dt＝鍵・dd＝値・多値は複数 dd。未知の data-type も索引に載る）。
func TestVocabIndexDL(t *testing.T) {
	setupSaveTest(t)

	const id = "000032"
	body := `<dl data-type="tags">` +
		`<dt>希望納期</dt><dd>2026-07-10</dd>` +
		`<dt>納入場所</dt><dd>本社工場</dd><dd>第二倉庫</dd>` +
		`</dl>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 32)
	if len(rows) != 3 {
		t.Fatalf("dl の索引件数が期待と異なります: got %d want 3 (%+v)", len(rows), rows)
	}

	// 「納期」を含む語だが辞書は完全一致なので「希望納期」は text… ではなく
	// 値そのものの確認だけ行う（推論辞書の適用範囲は語の完全一致）
	r := rows[0]
	if r.field != "希望納期" || r.value != "2026-07-10" {
		t.Errorf("dl の鍵と値が期待と異なります: %+v", r)
	}

	// 多値: 同じ鍵で2件
	count := 0
	for _, r := range rows {
		if r.field == "納入場所" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("複数 dd の多値が索引されていません: %+v", rows)
	}
}

// TestVocabIndexIgnoresPlainTables は data-type の無い素の table / dl が索引されない
// （オプトインの規約）ことと、洗い替えの冪等性を検証します。
func TestVocabIndexIgnoresPlainTables(t *testing.T) {
	setupSaveTest(t)

	const id = "000033"
	body := `<table><tr><th>普通の表</th></tr><tr><td>値</td></tr></table>` +
		`<dl><dt>用語</dt><dd>説明</dd></dl>` +
		`<table data-type="inspection-record"><tr><th>品番</th></tr><tr><td>X-1</td></tr></table>`

	for i := 0; i < 3; i++ { // 再同期でも件数が増えない（DELETE→INSERTの洗い替え）
		if err := SyncIndex(id, body); err != nil {
			t.Fatalf("%d回目の同期でエラー: %v", i+1, err)
		}
	}

	rows := queryVocabRows(t, 33)
	if len(rows) != 1 {
		t.Fatalf("素の table / dl が索引されているか、再同期で重複しています: got %d want 1 (%+v)", len(rows), rows)
	}
	if rows[0].dataType != "inspection-record" || rows[0].value != "X-1" {
		t.Errorf("索引の内容が期待と異なります: %+v", rows[0])
	}
}

// TestVocabIndexHeaderOnlyTable は見出し行だけの表（挿入直後の骨格に相当）が
// エラーにならず、索引にも何も載らないことを検証します。
func TestVocabIndexHeaderOnlyTable(t *testing.T) {
	setupSaveTest(t)

	const id = "000034"
	body := `<table data-type="inspection-record"><tr><th>品番</th><th>判定</th></tr></table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if rows := queryVocabRows(t, 34); len(rows) != 0 {
		t.Errorf("見出しだけの表から値が索引されています: %+v", rows)
	}
}

// TestVocabIndexMultipleBlocks は同一形式のブロックが複数あるとき block_no で
// 区別されることを検証します。
func TestVocabIndexMultipleBlocks(t *testing.T) {
	setupSaveTest(t)

	const id = "000035"
	body := `<table data-type="inspection-record"><tr><th>品番</th></tr><tr><td>A</td></tr></table>` +
		`<table data-type="inspection-record"><tr><th>品番</th></tr><tr><td>B</td></tr></table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 35)
	if len(rows) != 2 {
		t.Fatalf("索引の件数が期待と異なります: got %d want 2", len(rows))
	}
	if rows[0].blockNo == rows[1].blockNo {
		t.Errorf("block_no でブロックが区別されていません: %+v", rows)
	}
}

// TestSaveReportsUnknownVocabTypes は保存応答に未知の data-type の告知が
// 載ることを検証します（拒否ではなく告知——本文はそのまま保存される）。
func TestSaveReportsUnknownVocabTypes(t *testing.T) {
	setupSaveTest(t)

	body := `<table data-type="mystery-type"><tr><th>x</th></tr><tr><td>1</td></tr></table>` +
		`<table data-type="inspection-record"><tr><th>品番</th></tr><tr><td>A</td></tr></table>`
	got := postSave(t, "000036", body)

	types, ok := got["unknown_types"].([]interface{})
	if !ok || len(types) != 1 || types[0] != "mystery-type" {
		t.Errorf("unknown_types の告知が期待と異なります: %v", got["unknown_types"])
	}

	// 告知はするが保存は通る（未知の data-type も索引に載る）
	rows := queryVocabRows(t, 36)
	found := false
	for _, r := range rows {
		if r.dataType == "mystery-type" {
			found = true
		}
	}
	if !found {
		t.Error("未知の data-type が索引に載っていません")
	}
}
