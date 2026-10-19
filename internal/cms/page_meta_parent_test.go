package cms

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
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
