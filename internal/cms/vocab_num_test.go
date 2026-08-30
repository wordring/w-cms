package cms

import (
	"regexp"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// number 型の値が数値の格納クラス（norm_num REAL）に入ることのテスト
// （【一覧】日付形式と数詞.md §5・2026-08-30 決定）。
//
// norm_value は TEXT なので "8000" < "900" になる——数の大小・範囲で絞る列を
// TEXT のまま比較するのが唯一の「必ず間違う」形で、これを封じるのが norm_num。

// TestVocabIndexNormNum は、数値の比較が文字列比較に負けないことを検証します。
func TestVocabIndexNormNum(t *testing.T) {
	setupUploadTest(t, "000040", page.PageMeta{Owner: "alice", Mode: "330"})

	body := `<table data-type="inspection-record">
		<tr><th>品番</th><th data-type="number">工数</th></tr>
		<tr><td>A</td><td>8000</td></tr>
		<tr><td>B</td><td>900</td></tr>
		<tr><td>C</td><td>¥1,200</td></tr>
	</table>`
	if err := SyncIndex("000040", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 文字列比較なら "8000" < "900" だが、norm_num は数値なので 900 < 1200 < 8000。
	rows, err := database.DB.Query(
		`SELECT value FROM vocab_index WHERE field = '工数' ORDER BY norm_num`)
	if err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		got = append(got, v)
	}
	want := []string{"900", "¥1,200", "8000"}
	if len(got) != len(want) {
		t.Fatalf("行数が違います: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("norm_num の順序が数値になっていません: got %v want %v", got, want)
			break
		}
	}

	// 数値でない列（品番=text）は norm_num が NULL のままであること。
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE field = '品番' AND norm_num IS NOT NULL`).Scan(&n); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if n != 0 {
		t.Errorf("text 列に norm_num が入っています: %d 行", n)
	}
}

// TestSyncFallbackUpdatedAtIsISO は、サイドカーが無いページの updated_at フォールバックが
// T 区切りの ISO 8601（RFC3339）で入ることを検証します（要件定義書 §3・2026-08-30 決定）。
// かつては SQLite の CURRENT_TIMESTAMP（空白区切り）で、サイドカー由来と表記が割れていた。
func TestSyncFallbackUpdatedAtIsISO(t *testing.T) {
	setupUploadTest(t, "000041", page.PageMeta{Owner: "alice", Mode: "330"})

	// サイドカーを書かずに同期する（別ID）→ フォールバック経路に入る。
	if err := SyncIndex("000042", "<h1>孤児</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	var got string
	if err := database.DB.QueryRow(
		`SELECT updated_at FROM pages WHERE id = 42`).Scan(&got); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	iso := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	if !iso.MatchString(got) {
		t.Errorf("updated_at が T 区切りの ISO ではありません: %q", got)
	}
}
