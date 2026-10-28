package comm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestRecordRemovalIsAdminOnly は、届いた記録・送った記録を、管理者でない人が消せない・通信箱の外へ動かせないことを
// 固定します（2026-10-28・record_guard.go）。下書きでない向きの無いメモと、通信箱の中で動かすのは、いままでどおり。
//
// > 2026-10-28 利用者（届いた事実の範囲の記録も、書ける人は消せる件に）:「管理者だけ（勧め）」
func TestRecordRemovalIsAdminOnly(t *testing.T) {
	setupIntakeTest(t)
	for _, p := range []struct{ id, parent, body string }{
		{"000401", "000100", `<h1>受信の記録</h1><dl data-type="tags"><dt>` + DirectionTag + `</dt><dd>` + DirectionIn + `</dd></dl>`},
		{"000402", "000100", `<h1>手書きのメモ</h1>`},
		{"000403", "000100", `<h1>2026年</h1>`},
		{"000500", "000000", `<h1>ほかの置き場</h1>`},
	} {
		if err := page.WriteSidecar(p.id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: p.parent}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(p.id, p.body); err != nil {
			t.Fatal(err)
		}
	}
	alice := &auth.User{Username: "alice"}
	admin := &auth.User{Username: "root", IsAdmin: true}

	if why := guardRecordRemoval(alice, 401, -1); !strings.Contains(why, "管理者だけが消せます") {
		t.Errorf("⚠ 管理者でない人に受信の記録を消させます: %q", why)
	}
	if why := guardRecordRemoval(admin, 401, -1); why != "" {
		t.Errorf("⚠ 管理者にも消させません: %q", why)
	}
	if why := guardRecordRemoval(alice, 402, -1); why != "" {
		t.Errorf("⚠ 向きの無いメモまで断っています: %q", why)
	}
	if why := guardRecordRemoval(alice, 401, 403); why != "" {
		t.Errorf("⚠ 通信箱の中で動かすのを断っています: %q", why)
	}
	if why := guardRecordRemoval(alice, 401, 100); why != "" {
		t.Errorf("⚠ 通信箱の直下へ動かすのを断っています: %q", why)
	}

	// コアの口を通しても断る——ごみ箱へ移す口と、親を替える口。
	rr := httptest.NewRecorder()
	cms.DeletePageAPIHandler(rr, auth.WithUser(httptest.NewRequest("POST", "/api/delete-page?id=000401", nil), alice))
	if rr.Code != http.StatusForbidden {
		t.Errorf("⚠ 消す口が受信の記録を消しました（または別の理由で断った）: %d %s", rr.Code, rr.Body.String())
	}
	if _, _, err := cms.SetPageParent(alice, "000401", "000500"); err == nil || !strings.Contains(err.Error(), "管理者だけ") {
		t.Errorf("⚠ 受信の記録を通信箱の外へ動かしました: %v", err)
	}
	if _, _, err := cms.SetPageParent(alice, "000401", "000403"); err != nil {
		t.Errorf("⚠ 通信箱の中で動かせません: %v", err)
	}
}
