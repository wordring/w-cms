package mail

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// TestMailImportRejectsHugeBody は、POST /api/mail/import が大きすぎる本体を 413 で断ることを固定します
// （2026-10-19・【考察】文書の食い違い §3.3——それまでは上限なしで読んでいた）。読み込みには進まない。
func TestMailImportRejectsHugeBody(t *testing.T) {
	body := `{"folder":"` + strings.Repeat("x", 100<<10) + `"}`
	req := httptest.NewRequest("POST", "/api/mail/import", strings.NewReader(body))
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	MailImportAPIHandler(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("大きすぎる本体を断っていません: %d %s", rr.Code, rr.Body.String())
	}
}
