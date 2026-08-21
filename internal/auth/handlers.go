package auth

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
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
    <input type="hidden" name="next" value="%next%">
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
	// 戻り先を持ち回る。匿名の404画面から来た人を、ログインしたその足で
	// 目的のページへ返すため（要件定義書 §2.1 の「404統一」と対になる決定）。
	next := safeNextPath(r.URL.Query().Get("next"))
	body := strings.Replace(loginPageHTML, "%next%", html.EscapeString(next), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(strings.Replace(body, "%s", errMsg, 1)))
}

// safeNextPath はログイン後の戻り先を検証します。**同一サイト内の絶対パスだけ**を
// 通し、それ以外はトップ（"/"）へ倒します。
//
// 戻り先は外から与えられるので、素通しすると**開かれたリダイレクト**になります
// （`https://evil.example/` へ飛ばして偽のログイン画面を見せる、など）。
// `//host` はプロトコル相対URL、`/\host` はブラウザによって同じ扱いになるため
// どちらも弾きます。制御文字はヘッダ分割を防ぐために弾きます。
func safeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") {
		return "/"
	}
	if strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/"
	}
	for _, c := range next {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	return next
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
	// 失敗しても戻り先は保つ（打ち間違いのたびに目的地を見失わないため）。
	next := safeNextPath(r.FormValue("next"))
	if err != nil {
		http.Redirect(w, r, "/login?error=1&next="+url.QueryEscape(next), http.StatusFound)
		return
	}

	token, err := CreateSession(user.Username)
	if err != nil {
		http.Error(w, "セッションの作成に失敗しました", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token)
	// 元のページへ戻す。匿名の404から来た人がログインしたその足で目的地へ着く
	// ——これが無いと、社員どうしのアドレス共有が行き止まりになる。
	http.Redirect(w, r, next, http.StatusFound)
}

// LogoutAPIHandler はセッションを破棄します（POST /api/logout）。
//
// POST 限定です。CSRFProtect は GET を検証しない（middleware.go）ので、GET でも
// 破棄できると、本文へ <img src="/api/logout"> を1つ保存するだけで、そのページを
// 開いた全ログインユーザーを無音で追い出せます（同一オリジンなので SameSite も
// CSP も止めない）。フロントは元から POST で呼んでいます。
func LogoutAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
