package cms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
)

// 状態を変える経路が GET で動くと、CSRF 防御が丸ごと素通りされます。
// CSRFProtect は GET/HEAD/OPTIONS を検証せず（middleware.go）、Cookie は
// SameSite=Lax なのでトップレベルGETナビゲーションでは送られるためです。
//
// さらに重いのが**保存型**の変種です。サニタイザは相対URLの <img src> を通すので、
// 本文へ <img src="/api/new-page?parent=000001"> を保存しておくと、そのページを
// 開いた全ログインユーザーのブラウザが、クリックも見た目の変化も無しにGETを撃ちます。
// 同一オリジンなので SameSite も CSP も止めません。止められるのはメソッド制限だけ。

// TestNewPageRejectsGET は、ページ作成が GET で起きないことを固定します。
func TestNewPageRejectsGET(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/new-page?parent=000000", nil)
	req = auth.WithUser(req, &auth.User{Username: "mallory"})
	rr := httptest.NewRecorder()
	NewPageAPIHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET の /api/new-page が 405 になりません: code=%d body=%s", rr.Code, rr.Body.String())
	}
}
