package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFreshUserFollowsSessionAndAccount は、FreshUser が、つないだあとのログアウト・利用者の無効化・管理者から外すことを
// 見ることを固定します（2026-10-26——SSE は受けたときの写しで流し続けていた）。
func TestFreshUserFollowsSessionAndAccount(t *testing.T) {
	setupAuthDB(t)
	token := newSessionUser(t, "carol")
	req := httptest.NewRequest("GET", "/api/page-events?id=1", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	req = WithUser(req, &User{Username: "carol", IsAdmin: true}) // 受けたときは管理者だった
	if u := FreshUser(req); u == nil || u.IsAdmin {
		t.Fatalf("いまの状態を引き直していません: %+v", u)
	}
	// 別の人のセッションの Cookie では、その利用者として認めない。
	other := newSessionUser(t, "dave")
	mixed := httptest.NewRequest("GET", "/", nil)
	mixed.AddCookie(&http.Cookie{Name: sessionCookieName, Value: other})
	if u := FreshUser(WithUser(mixed, &User{Username: "carol"})); u != nil {
		t.Errorf("別の人のセッションで %q を認めました", u.Username)
	}
	DeleteSession(token) // ログアウト
	if u := FreshUser(req); u != nil {
		t.Errorf("ログアウトしたあとも %q を返しました", u.Username)
	}
}
