package cms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
)

// TestGateJSONGet は、ログインが要る読み取り口の入口が GET 以外を 405・匿名を 403 で断り、ログインした GET だけを
// 通すことを確かめます（4つの口の写しを寄せた口の番人・2026-10-09）。
func TestGateJSONGet(t *testing.T) {
	try := func(method string, u *auth.User) (int, bool) {
		req := httptest.NewRequest(method, "/api/x", nil)
		if u != nil {
			req = auth.WithUser(req, u)
		}
		rr := httptest.NewRecorder()
		_, ok := GateJSONGet(rr, req)
		if rr.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: Content-Type = %q", method, rr.Header().Get("Content-Type"))
		}
		return rr.Code, ok
	}
	alice := &auth.User{Username: "alice"}
	if code, ok := try("POST", alice); ok || code != http.StatusMethodNotAllowed {
		t.Errorf("POST を通しています: %d %v", code, ok)
	}
	if code, ok := try("GET", nil); ok || code != http.StatusForbidden {
		t.Errorf("匿名を通しています: %d %v", code, ok)
	}
	if _, ok := try("GET", alice); !ok {
		t.Error("ログインした GET を断っています")
	}
}
