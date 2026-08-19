package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCSPPolicyIsStrict は CSP が strict 版（'unsafe-inline' なし）であることを
// 固定します。移行第4段（2026-08-19）で 'unsafe-inline' を外した——インラインの
// script/style を新たに持ち込むとこのテストが落ちる前に実画面が壊れるはずだが、
// 方針の後戻り（ヘッダの緩和）をここで検出する。
func TestCSPPolicyIsStrict(t *testing.T) {
	h := CSPProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	got := rr.Header().Get("Content-Security-Policy")
	if got == "" {
		t.Fatal("Content-Security-Policy ヘッダがありません")
	}
	if strings.Contains(got, "unsafe-inline") {
		t.Errorf("CSP に 'unsafe-inline' が残っています: %s", got)
	}
	for _, want := range []string{
		"default-src 'self'", "script-src 'self'", "style-src 'self'",
		"object-src 'self'", "base-uri 'self'", "frame-ancestors 'self'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CSP に %q がありません: %s", want, got)
		}
	}
}
