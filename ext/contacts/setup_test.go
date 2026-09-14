package contacts

// 拡張パッケージのテスト土台。
//
// `internal/cms` のテストヘルパはパッケージ境界を越えないので、必要な分だけ
// ここに持ちます（コアへ「テストのためだけの公開関数」を足さないための割り切り。
// `ext/sheetmetal/setup_test.go` と同じ流儀です）。
//
// ⚠ **ファイルDBを使います。** アドレス帳は `visibleChildren`・`page.CanView` を
// 通るので、`:memory:` では動きません——接続ごとに別のDBになり、行を読みながら
// 中で別のクエリを投げると**空の別DBに当たって絞り込みが静かに全部落ちます**
// （2026-09-03 に本番コードで踏んだ罠）。`ext/sheetmetal` の `setupExtTest` が
// インメモリなのは、あちらがその経路を通らないからです。

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// setupTemplateAPITest は一時ディレクトリとファイルDBを用意します。
// **名前はコア側の同名ヘルパから引き継ぎました**（移設した試験がそのまま動くように）。
func setupTemplateAPITest(t *testing.T) {
	t.Helper()
	origWd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	dsn := filepath.ToSlash(filepath.Join(dir, "t.db")) +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
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
}

// newPage は正本（HTML＋サイドカー）を書いて索引まで通します。
func newPage(t *testing.T, id, body string, meta page.PageMeta) {
	t.Helper()
	dir := page.GetPageDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("ページディレクトリ作成エラー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".html"), []byte(body), 0644); err != nil {
		t.Fatalf("本文書き込みエラー: %v", err)
	}
	if err := page.WriteSidecar(id, meta); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}
