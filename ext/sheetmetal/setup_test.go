package sheetmetal

// 拡張パッケージのテスト土台。
//
// `internal/cms` のテストヘルパはパッケージ境界を越えないので、必要な分だけ
// ここに持ちます（コアへ「テストのためだけの公開関数」を足さないための割り切り）。
// 中身は upload_test.go の setupUploadTest と同じで、**ページを1枚だけ用意して
// 索引まで通す**ところまで。

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// setupExtTest は一時ディレクトリ＋インメモリDBを用意し、ページを1枚作ります。
func setupExtTest(t *testing.T, id string, p page.PageMeta) {
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
	if err := page.WriteSidecar(id, p); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}
	if err := cms.SyncIndex(id, "<h1>添付テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}
