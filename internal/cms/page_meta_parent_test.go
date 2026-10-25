package cms

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestPageMetaHidesUnreadableParentTitle は、/api/page-meta が、読めない親ページの題を返さないことを固定します
// （2026-10-19・設計総点検 §3.1 の残り・【考察】文書の食い違い §3.3）。読める親なら題を返す。
func TestPageMetaHidesUnreadableParentTitle(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000050", `<h1>秘密の親</h1>`, page.PageMeta{Owner: "bob", Mode: "300"})
	newPage(t, "000051", `<h1>見える子</h1>`, page.PageMeta{Owner: "alice", Mode: "302", ParentID: "000050"})
	newPage(t, "000052", `<h1>見える親</h1>`, page.PageMeta{Owner: "alice", Mode: "302"})
	newPage(t, "000053", `<h1>見える子2</h1>`, page.PageMeta{Owner: "alice", Mode: "302", ParentID: "000052"})
	meta := func(id string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/page-meta?id="+id, nil)
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		PageMetaAPIHandler(rr, req)
		if rr.Code != 200 {
			t.Fatalf("%s: %d %s", id, rr.Code, rr.Body.String())
		}
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return out
	}
	if got := meta("000051"); got["parent_title"] != "" || got["parent_id"] != "000050" {
		t.Errorf("読めない親の題を返しています: %+v", got)
	}
	if got := meta("000053"); got["parent_title"] != "見える親" {
		t.Errorf("読める親の題が返りません: %+v", got)
	}
}

// TestPageMetaHidesCreatorFromAnonymous は、/api/page-meta が、ログインしていない人（公開のページを見に来た社外の人）に
// 作った人のログイン名を返さないことを固定します（2026-10-26）。ログインしている人には返す。
func TestPageMetaHidesCreatorFromAnonymous(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000060", `<h1>公開のページ</h1>`, page.PageMeta{Owner: "alice", Mode: "302", Public: true})
	if _, err := database.DB.Exec(`UPDATE pages SET created_by = 'alice' WHERE id = 60`); err != nil {
		t.Fatal(err)
	}
	if !page.EffectivePublic(60) {
		t.Skip("公開の道が作れません（試験の下ごしらえ）")
	}
	meta := func(u *auth.User) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/page-meta?id=000060", nil)
		if u != nil {
			req = auth.WithUser(req, u)
		}
		rr := httptest.NewRecorder()
		PageMetaAPIHandler(rr, req)
		if rr.Code != 200 {
			t.Fatalf("%d %s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return out
	}
	if got := meta(nil); got["created_by"] != "" {
		t.Errorf("⚠ ログインしていない人に作った人 %v を返しました", got["created_by"])
	}
	if got := meta(&auth.User{Username: "bob"}); got["created_by"] != "alice" {
		t.Errorf("ログインしている人に作った人が返りません: %+v", got)
	}
}
