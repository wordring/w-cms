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
