package auth

import (
	"errors"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"os"
	"net/url"
	"strings"
)

// loginSem はログイン処理（argon2id検証）の同時実行数を制限します。
// argon2idは1回あたり約64MiBを使うため、ピークメモリを抑える（認証認可設計.md 2.1節）。
var loginSem = make(chan struct{}, 4)

// ログインの欄の長さの上限です（利用者名は管理画面で作る名前より十分に長く・パスワードは貼り付けた長い文も入る長さ）。
const (
	maxLoginNameBytes = 256
	maxPasswordBytes  = 1024
)

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
	switch r.URL.Query().Get("error") {
	case "":
	case "wait":
		// 続けて失敗したあとの待ち（バックオフ・ロックアウト）——パスワードは確かめていない（2026-10-01）。
		// 「違います」と言うと、正しく打ち直した人が打ち間違いだと思い込むので、待つように言う。
		errMsg = `<div class="error">続けて失敗したので、しばらく待ってからもう一度お試しください。</div>`
	default:
		errMsg = `<div class="error">ユーザー名またはパスワードが違います。</div>`
	}
	// 戻り先を持ち回る。匿名の404画面から来た人を、ログインしたその足で
	// 目的のページへ返すため（要件定義書 §2.1 の「404統一」と対になる決定）。
	next := safeNextPath(r.URL.Query().Get("next"))
	// 先に知らせを入れてから戻り先を入れる（戻り先に「%s」が入っていると、知らせが value の中へ入って欄が壊れた・2026-10-22 の見直し）。
	body := strings.Replace(loginPageHTML, "%s", errMsg, 1)
	body = strings.Replace(body, "%next%", html.EscapeString(next), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(body))
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
	// 長すぎる名前・パスワードは、確かめずに断る（2026-10-26。名前は失敗の記録と監査に残るので、長さを抑える）。
	if len(username) > maxLoginNameBytes || len(password) > maxPasswordBytes {
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}

	// argon2idの同時実行を制限してメモリのピークを抑える
	loginSem <- struct{}{}
	user, err := Authenticate(username, password)
	<-loginSem
	// 失敗しても戻り先は保つ（打ち間違いのたびに目的地を見失わないため）。
	next := safeNextPath(r.FormValue("next"))
	if err != nil {
		// 失敗も記録する。総当たりや、辞めた人の試行が見えるのはここだけ。
		// 入力されたパスワードは記録しない（記録そのものが漏洩経路になる）。
		Audit(username, "login.fail", clientIP(r))
		code := "1"
		if errors.Is(err, ErrLockedOut) {
			code = "wait"
		}
		http.Redirect(w, r, "/login?error="+code+"&next="+url.QueryEscape(next), http.StatusFound)
		return
	}

	token, err := CreateSession(user.Username)
	if err != nil {
		http.Error(w, "セッションの作成に失敗しました", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token)
	// 誰が・いつ・どこから入ってきたかを残す（要件定義書 §2.3）。
	Audit(user.Username, "login", clientIP(r))
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
		// 誰のセッションを閉じたのかは、破棄する前にしか分からない。
		if username, ok := ResolveSession(c.Value); ok {
			Audit(username, "logout", clientIP(r))
		}
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
	// 誰であるかの答えは絶対にキャッシュさせない。この口は OptionalAuth 配下で
	// RequireAuth の一律 no-store が掛からないため、ここで自前で付ける
	// （共用パソコンで前の人の名前が出る、という形で表に出る）。
	w.Header().Set("Cache-Control", "no-store")
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

// SessionAPIHandler は、このセッションが失効するまでの残り時間を返します（/api/session・要認証）。
// 画面が失効の数分前に告知するための口です（【要求】権限 §1・2026-10-14）。
//
//   - GET は**寿命を延ばしません**（画面が裏で残りを見るだけ——isOperation）。
//   - POST は告知の「続ける」です。利用者の操作なので、RequireAuth を通るときに寿命が延び、
//     延びたあとの残りを返します。
//
// 失効していれば RequireAuth が 401 を返すので、ここへは来ません。
func SessionAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	remaining, ok := SessionRemaining(c.Value)
	if !ok {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"expires_in":  int(remaining.Seconds()),          // 失効までの秒
		"idle_ttl":    int(SessionIdleTTL.Seconds()),     // 最後の操作から失効までの秒（8時間）
		"warn_before": int(SessionWarnBefore.Seconds()), // 失効の何秒前に告知するか（5分）
	})
}

// clientIP はリクエストの接続元アドレスを返します（監査記録用）。
//
// 本番はリバースプロキシの背後で動く（[【ガイド】デプロイ・運用.md] §6.2）ため、
// RemoteAddr は常にプロキシ自身になり「誰が事務所の外から入ってきたか」が分かりません。
// そこで X-Forwarded-For を見ますが、**無条件に信じると接続元を利用者が詐称できる**
// （記録が意味を失う）ので、リクエスト自体がループバック／私設アドレスから来たとき
// ＝前段にプロキシが居るときだけ採用します。
// 値は末尾を採ります——直近のプロキシが足した1つで、利用者が先頭へ何を書いても
// その後ろに真の接続元が積まれるためです。
func clientIP(r *http.Request) string {
	host := remoteHost(r)
	if !isTrustedProxy(host) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return host
	}
	return last
}

// IsFromTrustedProxy は、そのリクエストが前段のプロキシ経由で来たかを返します。
//
// `X-Forwarded-*` を採るかどうかの判定はこの1関数に集約します——監査記録の接続元
// （clientIP）と公開ビューの基底URL（scheme / host）で規則がずれると、片方だけ
// 詐称できる穴になるためです。
func IsFromTrustedProxy(r *http.Request) bool {
	return isTrustedProxy(remoteHost(r))
}

// remoteHost は接続元アドレスからポートを落としたホスト部です。
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isTrustedProxy は、そのアドレスを「前段のプロキシ」とみなすかを返します。
//
// 信じるのは、ループバックと、環境変数 WCMS_TRUSTED_PROXIES に書いたアドレスの範囲（CIDR をカンマで並べる。
// 例: VPS からのトンネルの相手 10.8.0.1/32）だけです。
// ⚠ 2026-10-22 まで、プライベートのアドレスを全部信じていた——職場の LAN のパソコンやスマホは直に来るので、
// だれでも X-Forwarded-For で監査の接続元を偽れた（見直しで見つけた）。
func isTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, n := range trustedProxyNets() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// trustedProxyNets は WCMS_TRUSTED_PROXIES の範囲です（読めない書き方は飛ばす）。
func trustedProxyNets() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range strings.Split(os.Getenv("WCMS_TRUSTED_PROXIES"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}
