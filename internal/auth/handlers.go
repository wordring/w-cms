package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// loginSem はログイン処理（argon2id検証）の同時実行数を制限します。
// argon2idは1回あたり約64MiBを使うため、ピークメモリを抑える（認証認可設計.md 2.1節）。
var loginSem = make(chan struct{}, 4)

// loginPageHTML はログインフォームです。スタイルは /assets/login.css（認証不要で
// 配信される self のCSS）。かつてはインラインstyleの自己完結だったが、
// CSP strict 化（'unsafe-inline' 除去）に伴い外部化した。
const loginPageHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>w-cms ログイン</title>
<link rel="stylesheet" href="/assets/login.css">
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

// MeAPIHandler は現在のユーザー情報を返します（GET /api/me）。
// 匿名公開ページでも呼ばれるため OptionalAuth 配下に置き、未認証時は 401 ではなく
// {authenticated:false} を返す（フロントが匿名＝読み取り専用モードへ切り替えるため）。
func MeAPIHandler(w http.ResponseWriter, r *http.Request) {
	u := CurrentUser(r)
	w.Header().Set("Content-Type", "application/json")
	if u == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"authenticated": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      u.Username,
		"is_admin":      u.IsAdmin,
		"primary_group": u.PrimaryGroup,
		"groups":        u.Groups,
	})
}
