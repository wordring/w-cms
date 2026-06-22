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
func setupPermsDB(t *testing.T, id string, p PageMeta) {
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
	setupPermsDB(t, "000005", PageMeta{Owner: "alice", Group: "", Mode: "300"})

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

// postPermsTok は X-Lock-Token ヘッダ付きで POST するヘルパ（編集ロック検証の確認用）。
func postPermsTok(handler http.HandlerFunc, path, body, token string, u *auth.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	if u != nil {
		req = auth.WithUser(req, u)
	}
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

// TestEditLockVerification は、エディタ内の変更操作（chmod/chown/set-parent）が
// 編集ロック（RequireEditLock）で直列化され、他者保持中は 409 になることを確認する。
func TestEditLockVerification(t *testing.T) {
	setupPermsDB(t, "000005", PageMeta{Owner: "alice", Group: "", Mode: "300"})
	pageLocks.ForceRelease(5)
	t.Cleanup(func() { pageLocks.ForceRelease(5) })

	// 別の編集者 bob がロックを保持している状況をつくる。
	if !pageLocks.TryAcquire(5, "bob", "").Acquired {
		t.Fatal("bob のロック取得に失敗")
	}

	// 所有者 alice でも、ロック未保持なら chmod は 409（他者が編集中）。
	rr := postPermsTok(PagePermsHandler, "/api/page-perms?id=000005", `{"mode":"320"}`, "", &auth.User{Username: "alice"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("他者ロック中の chmod が 409 になりません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if GetPerms(5).Mode != "300" {
		t.Errorf("409 なのに mode が変わっています: %s", GetPerms(5).Mode)
	}

	// admin の chown も他者ロック中は 409。
	rr = postPermsTok(PageChownHandler, "/api/page-chown?id=000005", `{"owner":"carol"}`, "", &auth.User{Username: "root", IsAdmin: true})
	if rr.Code != http.StatusConflict {
		t.Errorf("他者ロック中の chown が 409 になりません: code=%d", rr.Code)
	}

	// set-parent も他者ロック中は 409。
	rr = postPermsTok(SetParentAPIHandler, "/api/set-parent?id=000005&parent=", "", "", &auth.User{Username: "alice"})
	if rr.Code != http.StatusConflict {
		t.Errorf("他者ロック中の set-parent が 409 になりません: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// alice が編集権を取り直し、正しいトークンを X-Lock-Token で提示すれば chmod 成功。
	pageLocks.ForceRelease(5)
	a := pageLocks.TryAcquire(5, "alice", "")
	if !a.Acquired {
		t.Fatal("alice のロック取得に失敗")
	}
	rr = postPermsTok(PagePermsHandler, "/api/page-perms?id=000005", `{"mode":"320"}`, a.Token, &auth.User{Username: "alice"})
	if rr.Code != http.StatusOK {
		t.Fatalf("保持者 alice の chmod が失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if GetPerms(5).Mode != "320" {
		t.Errorf("mode が更新されていません: %s", GetPerms(5).Mode)
	}
}

func TestPageChownHandler(t *testing.T) {
	setupPermsDB(t, "000005", PageMeta{Owner: "alice", Group: "", Mode: "300"})

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
