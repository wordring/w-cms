package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// 認証イベント（ログインの成功・失敗・ログアウト）の監査記録を固定します。
//
// 動機はユーザーの言葉「誰が事務所の外から入ってきたか知る必要があると思います」
// （要件定義書 §2.3）。誰が・いつ、に加えて **どこから** が要るので、接続元も残します。

// auditOf は監査ログを新しい順に取り、action が一致する最初の1件を返します。
func auditOf(t *testing.T, action string) (AuditEntry, bool) {
	t.Helper()
	entries, err := RecentAudit(50)
	if err != nil {
		t.Fatalf("RecentAuditエラー: %v", err)
	}
	for _, e := range entries {
		if e.Action == action {
			return e, true
		}
	}
	return AuditEntry{}, false
}

// postLogin は /api/login をフォームで叩きます。from は接続元（RemoteAddr）。
func postLogin(t *testing.T, username, password, from string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if from != "" {
		req.RemoteAddr = from
	}
	rr := httptest.NewRecorder()
	LoginAPIHandler(rr, req)
	return rr
}

// TestLoginSuccessIsAudited はログイン成功が記録されることを検証します。
func TestLoginSuccessIsAudited(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("alice", "秘密のことば", false, ""); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}

	if rr := postLogin(t, "alice", "秘密のことば", "203.0.113.9:51000"); rr.Code != 302 {
		t.Fatalf("ログインに失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}

	e, ok := auditOf(t, "login")
	if !ok {
		t.Fatal("ログイン成功が監査記録に残っていません")
	}
	if e.Username != "alice" {
		t.Errorf("記録された利用者が違います: %q", e.Username)
	}
	if !strings.Contains(e.Target, "203.0.113.9") {
		t.Errorf("接続元が記録されていません: %q", e.Target)
	}
}

// TestLoginFailureIsAudited はログイン失敗が記録されることを検証します。
// 失敗のほうが重要です（総当たりや、辞めた人の試行が見えるのはここだけ）。
func TestLoginFailureIsAudited(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("alice", "秘密のことば", false, ""); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}

	postLogin(t, "alice", "ちがうことば", "203.0.113.9:51000")

	e, ok := auditOf(t, "login.fail")
	if !ok {
		t.Fatal("ログイン失敗が監査記録に残っていません")
	}
	if e.Username != "alice" {
		t.Errorf("試行された利用者名が残っていません: %q", e.Username)
	}
	if !strings.Contains(e.Target, "203.0.113.9") {
		t.Errorf("接続元が記録されていません: %q", e.Target)
	}
	// 失敗の記録にパスワードが混ざらないこと（記録そのものが漏洩経路になる）。
	if strings.Contains(e.Target, "ちがうことば") {
		t.Error("入力されたパスワードが監査記録に混入しています")
	}
}

// TestLogoutIsAudited はログアウトが記録されることを検証します。
func TestLogoutIsAudited(t *testing.T) {
	setupAuthDB(t)
	token, err := CreateSession("alice")
	if err != nil {
		t.Fatalf("CreateSessionエラー: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	req.RemoteAddr = "203.0.113.9:51000"
	rr := httptest.NewRecorder()
	LogoutAPIHandler(rr, req)
	if rr.Code != 302 {
		t.Fatalf("ログアウトに失敗: code=%d", rr.Code)
	}

	e, ok := auditOf(t, "logout")
	if !ok {
		t.Fatal("ログアウトが監査記録に残っていません")
	}
	if e.Username != "alice" {
		t.Errorf("記録された利用者が違います: %q", e.Username)
	}
}

// TestClientIPTrustsProxyOnlyFromLocal は、接続元の判定規則を固定します。
//
// 本番はリバースプロキシの背後で動く（デプロイ・運用ガイド §6.2）ため
// RemoteAddr は常にプロキシになり、そのままでは「外から来た人」が分かりません。
// かといって X-Forwarded-For を無条件に信じると、**記録される接続元を利用者が
// 自由に詐称できる**ので、プロキシ（ループバック/私設アドレス）から来たときだけ
// 採用し、末尾＝直近のプロキシが足した値を使います。
func TestClientIPTrustsProxyOnlyFromLocal(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"プロキシ経由なら XFF を採る", "127.0.0.1:8080", "198.51.100.7", "198.51.100.7"},
		{"多段でも末尾（直近のプロキシが足した値）", "127.0.0.1:8080", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		{"直結なら XFF は無視する", "203.0.113.9:51000", "198.51.100.7", "203.0.113.9"},
		{"XFF が無ければ RemoteAddr", "127.0.0.1:8080", "", "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/login", nil)
			req.RemoteAddr = c.remoteAddr
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := clientIP(req); got != c.want {
				t.Errorf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}
