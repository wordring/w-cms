package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// CSRFProtect は GET/HEAD/OPTIONS を検証しない（middleware.go）ので、
// 状態を変える経路は自分でメソッドを絞る必要があります。ログアウトが GET で
// 動くと、本文へ <img src="/api/logout"> を1つ保存するだけで、そのページを
// 開いた全ログインユーザーを無音で追い出せます（保存型CSRF）。

// TestLogoutRejectsGET はログアウトが GET で起きないことを固定します。
func TestLogoutRejectsGET(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/logout", nil)
	rr := httptest.NewRecorder()
	LogoutAPIHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET の /api/logout が 405 になりません: code=%d", rr.Code)
	}
	// セッションCookieの削除も起きていないこと（副作用ゼロで弾く）。
	if len(rr.Result().Cookies()) != 0 {
		t.Errorf("405 なのに Cookie が操作されました: %v", rr.Result().Cookies())
	}
}
