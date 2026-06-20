package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// loginSem はログイン処理（argon2id検証）の同時実行数を制限します。
// argon2idは1回あたり約64MiBを使うため、ピークメモリを抑える（認証認可設計.md 2.1節）。
var loginSem = make(chan struct{}, 4)

// loginPageHTML は依存のない自己完結のログインフォームです。
const loginPageHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>w-cms ログイン</title>
<style>
  body { font-family: sans-serif; background:#f5f5f5; display:flex; min-height:100vh; align-items:center; justify-content:center; margin:0; }
  .card { background:#fff; padding:2rem; border-radius:8px; box-shadow:0 2px 8px rgba(0,0,0,.1); width:320px; }
  h1 { font-size:1.2rem; margin:0 0 1rem; }
  label { display:block; font-size:.85rem; margin:.6rem 0 .2rem; color:#444; }
  input { width:100%; padding:.5rem; box-sizing:border-box; border:1px solid #ccc; border-radius:4px; }
  button { width:100%; margin-top:1.2rem; padding:.6rem; background:#2563eb; color:#fff; border:0; border-radius:4px; cursor:pointer; font-size:1rem; }
  .error { color:#b91c1c; font-size:.85rem; margin-top:.8rem; }
</style>
</head>
<body>
  <form class="card" method="POST" action="/api/login">
    <h1>w-cms ログイン</h1>
    <label for="username">ユーザー名</label>
    <input id="username" name="username" autocomplete="username" autofocus required>
    <label for="password">パスワード</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">ログイン</button>
    %s
  </form>
</body>
</html>`

// LoginPageHandler はログインフォームを表示します（GET /login、認証不要）。
func LoginPageHandler(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if r.URL.Query().Get("error") != "" {
		errMsg = `<div class="error">ユーザー名またはパスワードが違います。</div>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(strings.Replace(loginPageHTML, "%s", errMsg, 1)))
}

// LoginAPIHandler は認証してセッションを発行します（POST /api/login、認証不要）。
func LoginAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	// argon2idの同時実行を制限してメモリのピークを抑える
	loginSem <- struct{}{}
	user, err := Authenticate(username, password)
	<-loginSem
	if err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}

	token, err := CreateSession(user.Username)
	if err != nil {
		http.Error(w, "セッションの作成に失敗しました", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

// LogoutAPIHandler はセッションを破棄します（POST /api/logout）。
func LogoutAPIHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		DeleteSession(c.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// MeAPIHandler は現在のユーザー情報を返します（GET /api/me、要認証）。
func MeAPIHandler(w http.ResponseWriter, r *http.Request) {
	u := CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"username":      u.Username,
		"is_admin":      u.IsAdmin,
		"primary_group": u.PrimaryGroup,
		"groups":        u.Groups,
	})
}
