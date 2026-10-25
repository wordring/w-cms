package auth

import (
	"context"
	"net/http"
)

type ctxKey int

const userCtxKey ctxKey = 0

// WithUser はリクエストのコンテキストに認証済みユーザーを格納した新しいリクエストを返します。
func WithUser(r *http.Request, u *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
}

// CurrentUser は認証ミドルウェアが格納したユーザーを返します。未認証なら nil。
func CurrentUser(r *http.Request) *User {
	u, _ := r.Context().Value(userCtxKey).(*User)
	return u
}

// FreshUser は、長く開いている要求（SSE など）の利用者を、いまの状態で引き直します（2026-10-26）。
//
// CurrentUser は要求を受けたときの写しなので、あとからログアウトした・セッションが切れた・管理者から外した・
// グループから外した・無効にした、が見えません。Cookie のセッションが今も有効で同じ利用者を指すかを確かめ
// （寿命は延ばさない）、利用者を DB から読み直します。有効でなければ nil。
// Cookie の無い要求（中で組んだ要求・試験）は、利用者の読み直しだけをします。
func FreshUser(r *http.Request) *User {
	u := CurrentUser(r)
	if u == nil {
		return nil
	}
	name := u.Username
	if c, err := r.Cookie(sessionCookieName); err == nil {
		got, ok := ResolveSession(c.Value)
		if !ok || got != name {
			return nil
		}
	}
	fresh, err := LookupUser(name)
	if err != nil {
		return nil
	}
	return fresh
}
