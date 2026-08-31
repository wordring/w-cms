package cms

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// 監査記録の取りこぼしを埋めたことを固定します（要件定義書 §2.3 の未達分）。
//
// ページの新規作成・添付の保存と上書き・索引の全再構築は、どれも「誰かが何かを
// 変えた」出来事なのに記録に残っていませんでした。とくに添付は**リビジョンも
// ゴミ箱も無く上書きが復元できない**ので、記録が唯一の手掛かりになります。

// setupAuditDB は監査ログの書き込み先（auth.db 相当）を用意します。
// auth.Audit は database.AuthDB が nil なら黙って捨てるため、これが無いと
// 「記録されている」ことをテストできません。
func setupAuditDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("認証DB接続エラー: %v", err)
	}
	if err := database.CreateAuthTables(db); err != nil {
		t.Fatalf("認証テーブル作成エラー: %v", err)
	}
	prev := database.AuthDB
	database.AuthDB = db
	t.Cleanup(func() { database.AuthDB = prev; db.Close() })
}

// auditEntries は監査ログを新しい順に返します。
func auditEntries(t *testing.T) []auth.AuditEntry {
	t.Helper()
	entries, err := auth.RecentAudit(50)
	if err != nil {
		t.Fatalf("RecentAuditエラー: %v", err)
	}
	return entries
}

// findAudit は action が一致する最初の1件（＝最新）を返します。
func findAudit(t *testing.T, action string) (auth.AuditEntry, bool) {
	t.Helper()
	for _, e := range auditEntries(t) {
		if e.Action == action {
			return e, true
		}
	}
	return auth.AuditEntry{}, false
}

// TestNewPageIsAudited はページの新規作成が記録されることを検証します。
func TestNewPageIsAudited(t *testing.T) {
	setupTemplateAPITest(t)
	setupAuditDB(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	rr := newPageWithTemplate(t, TopPageID, "", &auth.User{Username: "alice"})
	if rr.Code != 302 {
		t.Fatalf("新規作成に失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}

	e, ok := findAudit(t, "new-page")
	if !ok {
		t.Fatal("ページの新規作成が監査記録に残っていません")
	}
	if e.Username != "alice" {
		t.Errorf("作成者が違います: %q", e.Username)
	}
	// どのページが増えたのか分からない記録は、後から追えないので意味が薄い。
	if !strings.Contains(e.Target, "000001") {
		t.Errorf("作成されたページIDが記録されていません: %q", e.Target)
	}
}

// TestAttachmentUploadsAreAuditedAndNeverDestroy は添付の保存が記録され、
// **同じ元名の再アップロードが既存の添付を壊さない**ことを検証します。
// 保存名はサーバー生成（2026-08-31）なので、かつての「同名上書き＝不可逆の
// データ破壊」は設計ごと消えた——2回目は別のファイルとして増える。
func TestAttachmentUploadsAreAuditedAndNeverDestroy(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	setupAuditDB(t)
	alice := &auth.User{Username: "alice"}

	rr1 := postUpload(t, id, "発注書.pdf", []byte("%PDF-1.4\n初版"), alice)
	if rr1.Code != 200 {
		t.Fatalf("1回目のアップロードに失敗: code=%d body=%s", rr1.Code, rr1.Body.String())
	}
	name1 := savedNameOf(t, rr1)
	e, ok := findAudit(t, "attach")
	if !ok {
		t.Fatal("添付の保存が監査記録に残っていません")
	}
	if !strings.Contains(e.Target, name1) || !strings.Contains(e.Target, id) {
		t.Errorf("ページIDと保存名が記録されていません: %q", e.Target)
	}

	rr2 := postUpload(t, id, "発注書.pdf", []byte("%PDF-1.4\n差替"), alice)
	if rr2.Code != 200 {
		t.Fatalf("2回目のアップロードに失敗: code=%d body=%s", rr2.Code, rr2.Body.String())
	}
	name2 := savedNameOf(t, rr2)
	if name1 == name2 {
		t.Fatal("同じ保存名が再利用されました（既存の添付が上書きされうる）")
	}
	// 初版が無傷で残っていること——これが生成名の利得。
	got, err := os.ReadFile(filepath.Join(page.AttachmentDir(id), name1))
	if err != nil || !strings.Contains(string(got), "初版") {
		t.Errorf("1回目の添付が壊れています: %v %q", err, got)
	}
}

// TestRebuildIsAudited は索引の全再構築が記録されることを検証します。
// 派生索引を作り直す操作は、途中で失敗すると集計が静かに欠けるため、
// 「いつ・誰が回したか」が要ります。
func TestRebuildIsAudited(t *testing.T) {
	setupTemplateAPITest(t)
	setupAuditDB(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	req := httptest.NewRequest("POST", "/api/rebuild-db", nil)
	req = auth.WithUser(req, &auth.User{Username: "root", IsAdmin: true})
	rr := httptest.NewRecorder()
	RebuildDBAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("再構築に失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}

	e, ok := findAudit(t, "rebuild-db")
	if !ok {
		t.Fatal("索引の全再構築が監査記録に残っていません")
	}
	if e.Username != "root" {
		t.Errorf("実行者が違います: %q", e.Username)
	}
	// 何ページ取り込めたかは、欠けに後から気づくための手掛かり。
	if !strings.Contains(e.Target, "1") {
		t.Errorf("取り込み件数が記録されていません: %q", e.Target)
	}
}
