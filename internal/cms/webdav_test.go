package cms

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// davRequest は WebDAV の要求を1つ投げます（合言葉は任意）。
func davRequest(t *testing.T, method, path, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.Header.Set("Authorization", "Basic "+
			base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	rr := httptest.NewRecorder()
	DavHandler(rr, req)
	return rr
}

// setupDavTest は添付を1つ持つページを作り、WebDAV を開けた状態にします。
//
// **認証DBも用意します**——WebDAV は Cookie ではなく HTTP Basic を通るので、
// 利用者を注入する（auth.WithUser）他のテストと違い、**本物の照合が走ります**。
func setupDavTest(t *testing.T, pageID string, meta page.PageMeta) string {
	t.Helper()
	setupUploadTest(t, pageID, meta)
	t.Setenv("WCMS_DAV", "1")

	if err := database.InitAuthDB(); err != nil {
		t.Fatalf("認証DBを用意できません: %v", err)
	}
	t.Cleanup(func() { database.AuthDB.Close() })
	if err := auth.CreateUser("alice", "pw", false, ""); err != nil {
		t.Fatalf("利用者を作れません: %v", err)
	}
	if err := auth.CreateUser("bob", "pw", false, ""); err != nil {
		t.Fatalf("利用者を作れません: %v", err)
	}

	dir := page.AttachmentDir(pageID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("添付フォルダを作れません: %v", err)
	}
	name := "a1b2.dxf"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("0\nSECTION\n"), 0o644); err != nil {
		t.Fatalf("添付を作れません: %v", err)
	}
	return name
}

// TestDavClosedByDefault は、**環境変数を置くまで口が開かない**ことを固定します。
//
// 平文HTTPでの Basic 認証は要求のたびに合言葉を流すので、「気づかないうちに開いて
// いた」が起きない形にしてあります（2026-09-07）。
func TestDavClosedByDefault(t *testing.T) {
	setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})
	t.Setenv("WCMS_DAV", "")

	rr := davRequest(t, "GET", "/dav/000001/a1b2.dxf", "alice", "pw")
	if rr.Code != http.StatusNotFound {
		t.Errorf("既定で開いています: %d", rr.Code)
	}
}

// TestDavRequiresAuth は、合言葉が無ければ 401 と認証の要求を返すことを固定します。
// エクスプローラはこの返事を見て合言葉を尋ねます——無いと黙って失敗します。
func TestDavRequiresAuth(t *testing.T) {
	setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})

	rr := davRequest(t, "PROPFIND", "/dav/000001/", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("認証を求めていません: %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Errorf("認証の要求が返っていません: %q", rr.Header().Get("WWW-Authenticate"))
	}
}

// TestDavRejectsWrites は、**書き込む要求を全部断る**ことを固定します（読み取り専用の期間）。
//
// 書き込みを許す前に決めることが2つ残っています（添付の版・同時編集）。ここが緩むと、
// **決める前に上書きが起きます**——CADファイルは戻せません。
func TestDavRejectsWrites(t *testing.T) {
	setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})

	for _, m := range []string{"PUT", "DELETE", "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK"} {
		rr := davRequest(t, m, "/dav/000001/a1b2.dxf", "alice", "pw")
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s を断っていません: %d", m, rr.Code)
		}
	}
}

// TestDavRootListsNothing は、**入口そのものが何も出さない**ことを固定します。
// ページの一覧を出すと、読めないページの存在を数えられる形になります。
func TestDavRootListsNothing(t *testing.T) {
	setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})

	for _, p := range []string{"/dav/", "/dav/notanid/"} {
		rr := davRequest(t, "PROPFIND", p, "alice", "pw")
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s が何かを返しています: %d", p, rr.Code)
		}
	}
}

// TestDavHidesUnreadablePage は、**読めないページを「無い」と同じ顔で返す**ことを
// 固定します（匿名の404統一と同じ規律）。403 を返すと、そこにページが在ることが漏れます。
func TestDavHidesUnreadablePage(t *testing.T) {
	setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "300"})

	rr := davRequest(t, "GET", "/dav/000001/a1b2.dxf", "bob", "pw")
	if rr.Code != http.StatusNotFound {
		t.Errorf("読めないページの存在が漏れています: %d", rr.Code)
	}
}

// TestDavServesFileAndListing は、**読める人には実際に配る**ことを固定します。
// 断る側だけを固めると、「全部断っているから通る」テストになってしまいます。
func TestDavServesFileAndListing(t *testing.T) {
	name := setupDavTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})

	// ファイルそのもの
	rr := davRequest(t, "GET", "/dav/000001/"+name, "alice", "pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("添付を配れていません: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SECTION") {
		t.Errorf("中身が違います: %q", rr.Body.String())
	}

	// フォルダの一覧（エクスプローラが最初に投げるのがこれ）
	rr = davRequest(t, "PROPFIND", "/dav/000001/", "alice", "pw")
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("一覧を返していません: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), name) {
		t.Errorf("一覧に添付が出ていません: %s", rr.Body.String())
	}
}
