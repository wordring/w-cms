package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLocalSocketActsAsTheConfiguredUser は、業務サーバーの中の入口（local_socket.go）が、
//   - Cookie 無しでも決めた利用者として通し、POST に Origin が無くても通す（ブラウザが開けない入口）
//   - 届いた Cookie（別の人のセッション）を見ない
//   - 決めた利用者を無効にしたら、ログインしていないのと同じ（401）
//   - 入口を通らない要求は、いままでどおり（Cookie 無しは 401・Origin の無い POST は 403）
//
// ことを固定します（2026-10-26）。
func TestLocalSocketActsAsTheConfiguredUser(t *testing.T) {
	setupAuthDB(t)
	newSessionUser(t, "claude")
	other := newSessionUser(t, "dave")
	var seen, fresh string
	inner := CSRFProtect(RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = CurrentUser(r).Username
		fresh = ""
		if u := FreshUser(r); u != nil { // 長く開いている口（ページの更新の通知など）が引き直す利用者
			fresh = u.Username
		}
	})))
	sock := LocalSocket(inner, "claude")
	do := func(h http.Handler, method string, cookie string) int {
		t.Helper()
		seen = ""
		req := httptest.NewRequest(method, "/api/sync", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := do(sock, "POST", ""); code != 200 || seen != "claude" {
		t.Fatalf("⚠ 入口から来た POST を決めた利用者として通していません: %d %q", code, seen)
	}
	if code := do(sock, "GET", other); code != 200 || seen != "claude" || fresh != "claude" {
		t.Errorf("⚠ 入口へ届いた別の人の Cookie に引きずられました（%d・利用者 %q・引き直し %q）", code, seen, fresh)
	}
	if code := do(inner, "GET", ""); code != http.StatusUnauthorized {
		t.Errorf("⚠ 入口を通らない Cookie 無しの要求を通しました: %d", code)
	}
	if code := do(inner, "POST", other); code != http.StatusForbidden {
		t.Errorf("⚠ 入口を通らない Origin の無い POST を通しました: %d", code)
	}
	if err := SetDisabled("claude", true); err != nil {
		t.Fatal(err)
	}
	if code := do(sock, "GET", ""); code != http.StatusUnauthorized {
		t.Errorf("⚠ 無効にした利用者として入口を通しました: %d %q", code, seen)
	}
}
