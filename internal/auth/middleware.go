package auth

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const sessionCookieName = "wcms_session"

// secureCookies はCookieに Secure 属性を付けるかを返します。
// 本番（HTTPS、リバースプロキシTLS終端）では true。ローカルHTTP検証時は
// 環境変数 WCMS_SECURE_COOKIES=0 で無効化できます。既定は true。
func secureCookies() bool {
	switch strings.ToLower(os.Getenv("WCMS_SECURE_COOKIES")) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionAbsoluteTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// RequireAuth は、セッションCookieから認証済みユーザーを解決して後続ハンドラに渡します。
// 未認証の場合、API（/api/）は 401、それ以外は /login へリダイレクトします。
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var user *User
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if username, ok := ResolveSession(c.Value); ok {
				if u, e := LookupUser(username); e == nil {
					user = u
				}
			}
		}
		// **認証が要る経路の応答は、絶対にキャッシュさせない**（要件定義書 §4.4）。
		// 公開ページをキャッシュ可能にした以上（2026-08-26）、この切り分けが前提条件です。
		// Cache-Control が無いと、ブラウザや中間キャッシュがヒューリスティックに保存して
		// よいことになり、共用パソコンで前の人の応答が出うる状態が残ります。
		// ハンドラごとに付けるとどれか1本が漏れるので、**入口で一律に**付けます
		// （401 で止まる場合も含めて必ず付く）。
		w.Header().Set("Cache-Control", "no-store")

		if user == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "認証が必要です", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusFound)
			}
			return
		}
		next.ServeHTTP(w, WithUser(r, user))
	})
}

// OptionalAuth は、セッションがあればユーザーを解決して後続ハンドラに渡し、無ければ
// 「未認証のまま」後続へ通します（ブロックしない）。匿名でも閲覧しうる公開ページ系の
// ルート（ページ本文・添付配信・/api/me）に使い、認可は各ハンドラが
// RequirePageReadOrPublic 等で個別に判定します（認証認可設計.md 10.5）。
func OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if username, ok := ResolveSession(c.Value); ok {
				if u, e := LookupUser(username); e == nil {
					next.ServeHTTP(w, WithUser(r, u))
					return
				}
			}
		}
		next.ServeHTTP(w, r) // 匿名（コンテキストにユーザー無し）
	})
}

// cspPolicy は全レスポンスへ付与する Content-Security-Policy（strict 版）です。
//
// 2026-08-19 の移行第4段（Web Components 全廃）で 'unsafe-inline' を外しました。
// インラインの script/style はリポジトリ全体でゼロです:
//   - FOUC 防止スクリプトは /assets/boot.js へ外部化（head の同期読み込み。
//     当初の per-request nonce 案より単純で、静的配信とも両立するため）
//   - templates/*.html の style=（75個）と web-components.js の生成 style=（8行）は
//     ファイルごと撤去（計算ビューはサーバー事前描画 view_render.go へ）
//   - ログイン画面の <style> は /assets/login.css へ外部化
// on*= 属性ハンドラも従来どおりゼロ。経緯は docs/本文サニタイズ設計.md §6。
//
// アプリは外部リソース（CDN・web fonts・data:/blob: 等）を一切使わず、PDFは
// 同一オリジンの <embed>（きれいなURL /<ページID>/<生成ID>.pdf）のため、
// default-src 'self' と object-src 'self' でインライン以外は何も壊れません。
const cspPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"object-src 'self'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'self'"

// CSPProtect は全レスポンスへ Content-Security-Policy ヘッダを付与します。
// 既存の CSRFProtect と同じミドルウェア形（func(http.Handler) http.Handler）で、
// main.go のハンドラチェーンの最外周に置いて全ルートへ適用します。
func CSPProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", cspPolicy)
		next.ServeHTTP(w, r)
	})
}

// CSRFProtect は状態変更系（GET/HEAD/OPTIONS以外）のリクエストに対し、
// Origin（無ければ Referer）のホストがリクエストホストと一致することを要求します。
// SameSite=Lax Cookie と併用したCSRF対策（認証認可設計.md 2.3節）。
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "CSRF検証に失敗しました（オリジン不一致）", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin は Origin または Referer のホストがリクエストホストと一致するかを返します。
// どちらのヘッダも無い場合は安全側に倒して false（拒否）。
func sameOrigin(r *http.Request) bool {
	for _, h := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if h == "" {
			continue
		}
		if u, err := url.Parse(h); err == nil && u.Host == r.Host {
			return true
		}
		return false
	}
	return false
}
