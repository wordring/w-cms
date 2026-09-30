package mail

// メールの拡張のテスト土台（2026-09-30・送る欄と下書きの試験のため）。
//
// `ext/comm/setup_test.go` と同じ流儀です（パッケージ境界を越えられないので、要る分だけここに持つ）。
// ⚠ **ファイルDBを使います**——`page.CanView` を通るので `:memory:` では絞り込みが静かに全部落ちます。

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/ext/comm"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestMain はリポジトリの設定を読みます（**タグの型は名前で決まる**——`下書きの元` は参照）。
func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "settings.json")
	if err := cms.LoadSettingsFrom(path); err != nil {
		panic("テストの設定を読み込めません: " + err.Error())
	}
	os.Exit(m.Run())
}

// setupMailTest は一時ディレクトリ・ファイルDB・トップ・通信箱・通信記録のテンプレートを用意します。
func setupMailTest(t *testing.T) {
	t.Helper()
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	db, err := sql.Open("sqlite", "test.db")
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
	putPage(t, "000000", "", "alice", "330", "<h1>トップ</h1>")
	putPage(t, "000100", "000000", "alice", "330", "<h1>"+comm.MailBoxTitle+"</h1>")
	putPage(t, "000950", "000000", "alice", "330", "<h1>"+cms.TemplateRootTitle+"</h1>")
	putPage(t, "000951", "000950", "alice", "330", "<h1>通信</h1>")
	putPage(t, "000954", "000951", "alice", "330", testSentTemplate)
}

// putPage は正本（HTML＋サイドカー）を書いて索引まで通します。
func putPage(t *testing.T, id, parent, owner, mode, body string) {
	t.Helper()
	if err := os.MkdirAll(page.GetPageDir(id), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page.BodyPath(id), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := page.WriteSidecar(id, page.PageMeta{Owner: owner, Mode: mode, ParentID: parent}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatal(err)
	}
}

// putFile はページの添付（files/）を1つ置きます。
func putFile(t *testing.T, id, name string, content []byte) {
	t.Helper()
	dir := page.AttachmentDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0644); err != nil {
		t.Fatal(err)
	}
}
