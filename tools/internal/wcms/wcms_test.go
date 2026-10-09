package wcms

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestLoginAndDo は、ログインがフォーム形式で名前とパスワードを送り、口を呼ぶときに Origin（CSRF の守り）と
// 編集ロックの札を付けることを確かめます（道具3本の写しを寄せた口の番人・2026-10-09）。
func TestLoginAndDo(t *testing.T) {
	var gotLogin, gotOrigin, gotToken, gotCookie string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			r.ParseForm()
			gotLogin = r.Form.Get("username") + "/" + r.Form.Get("password") + " " + r.Header.Get("Content-Type")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusFound)
		case "/api/x":
			gotOrigin, gotToken = r.Header.Get("Origin"), r.Header.Get("X-Lock-Token")
			if c, err := r.Cookie("session"); err == nil {
				gotCookie = c.Value
			}
		}
	}))
	defer srv.Close()
	t.Setenv("WCMS_BASE", srv.URL+"/")
	t.Setenv("WCMS_USER", "u1")
	t.Setenv("WCMS_PASS", "p1")
	c, err := Login(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if gotLogin != "u1/p1 application/x-www-form-urlencoded" {
		t.Errorf("ログインで送ったもの = %q", gotLogin)
	}
	res, err := c.Do("POST", "/api/x", strings.NewReader("{}"), "application/json", "tok")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if gotOrigin != srv.URL || gotToken != "tok" || gotCookie != "s1" {
		t.Errorf("Origin=%q 札=%q クッキー=%q（Origin は末尾の / を落とした基の URL・札とログインのクッキーが付くはず）", gotOrigin, gotToken, gotCookie)
	}
	if _, err := c.Do("GET", "/api/x", nil, "", ""); err != nil || gotToken != "" {
		t.Errorf("札が空なのに付けています: %q %v", gotToken, err)
	}
}

// TestLoginRefused は、ログインの戻り先に error が付いていたら断ることを確かめます。
func TestLoginRefused(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/login?error=1")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	t.Setenv("WCMS_BASE", srv.URL)
	if _, err := Login(time.Minute); err == nil {
		t.Error("ログインに失敗したのに係を返しています")
	}
}
