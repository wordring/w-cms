package comm

// 通信のテスト土台（2026-09-15 にコアから移した試験のため）。
//
// `internal/cms` のテストヘルパはパッケージ境界を越えないので、必要な分だけ
// ここに持ちます（コアへ「テストのためだけの公開関数」を足さないための割り切り。
// `ext/comm/contacts/setup_test.go`・`ext/subcon/setup_test.go` と同じ流儀です）。
// **名前はコア側の同名ヘルパから引き継ぎました**（移設した試験がそのまま動くように）。

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// TestMain はリポジトリの設定を読みます。**型は名前で決まる**ので、読まないと
// `差出人`（email 型）の畳んだ値がアドレスにならず、取り込みの試験が落ちます。
// 場所は `runtime.Caller` で解決します——試験は一時ディレクトリへ移るため。
func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "settings.json")
	if err := cms.LoadSettingsFrom(path); err != nil {
		panic("テストの設定を読み込めません: " + err.Error())
	}
	os.Exit(m.Run())
}

// setupSaveTest は一時ディレクトリとインメモリDBを用意します（コアの同名ヘルパと同じ）。
// ⚠ `visibleChildren`・`page.CanView` を通る試験は setupIntakeTest（ファイルDB）を使うこと。
func setupSaveTest(t *testing.T) *sql.DB {
	t.Helper()
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := cms.ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	return db
}

// queryTags はページのタグを `名前=値` の並びで返します（コアの同名ヘルパと同じ）。
func queryTags(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT name, value FROM page_tags
		 WHERE page_id = ? ORDER BY name, value`, pageID)
	if err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f, v string
		rows.Scan(&f, &v)
		out = append(out, f+"="+v)
	}
	return out
}
