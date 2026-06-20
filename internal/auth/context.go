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
