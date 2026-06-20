package cms

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// setupPermsDB は data/master を一時ディレクトリにし、cms.db を用意して
// 指定ページのサイドカー＋page_perms を作ります。
func setupPermsDB(t *testing.T, id string, p PagePerms) {
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
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	if err := WriteSidecar(id, p); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>権限管理テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

func postPerms(handler http.HandlerFunc, path, body string, u *auth.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestPagePermsHandler_chmod(t *testing.T) {
	setupPermsDB(t, "000005", PagePerms{Owner: "alice", Group: "", Mode: "300"})

	// 所有者 alice が chmod 300 -> 320
	rr := postPerms(PagePermsHandler, "/api/page-perms?id=000005", `{"mode":"320"}`, &auth.User{Username: "alice"})
	if rr.Code != http.StatusOK {
		t.Fatalf("所有者のchmodが失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := GetPerms(5).Mode; got != "320" {
		t.Errorf("modeが更新されていません: %s", got)
	}

	// 部外者 carol は 403
	rr = postPerms(PagePermsHandler, "/api/page-perms?id=000005", `{"mode":"333"}`, &auth.User{Username: "carol"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("部外者のchmodが拒否されません: code=%d", rr.Code)
	}

	// 不正なmodeは400
	rr = postPerms(PagePermsHandler, "/api/page-perms?id=000005", `{"mode":"99"}`, &auth.User{Username: "alice"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("不正なmodeが拒否されません: code=%d", rr.Code)
	}
}

func TestPageChownHandler(t *testing.T) {
	setupPermsDB(t, "000005", PagePerms{Owner: "alice", Group: "", Mode: "300"})

	// admin が chown alice -> bob
	rr := postPerms(PageChownHandler, "/api/page-chown?id=000005", `{"owner":"bob"}`, &auth.User{Username: "root", IsAdmin: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("adminのchownが失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := GetPerms(5).Owner; got != "bob" {
		t.Errorf("ownerが更新されていません: %s", got)
	}

	// 非admin（元owner alice でも）chownは403
	rr = postPerms(PageChownHandler, "/api/page-chown?id=000005", `{"owner":"carol"}`, &auth.User{Username: "alice"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("非adminのchownが拒否されません: code=%d", rr.Code)
	}
}
