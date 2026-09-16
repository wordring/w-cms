package cms

// 拡張が要る置き場の口（2026-09-16）。正本は required_pages.go の冒頭。
//
//	GET  /api/admin/pages … いま何が要り、どれが在るか
//	POST /api/admin/pages … 足りないものを作る（冪等）
//
// **admin だけ**です。トップ直下にページを作る操作なので、既存の規律
// （「`/api/new-page` の親がトップならadmin」）と同じ線に合わせます。

import (
	"encoding/json"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// RequiredPagesAPIHandler は GET/POST /api/admin/pages です。
func RequiredPagesAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// **認可を先に見ます**（`PagePermsHandler` と同じ順序）。読むほうも admin です
	// ——「どの置き場が欠けているか」は構成の情報で、利用者に見せる必要がありません。
	if !page.RequireAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"pages":   RequiredPageStatuses(),
		})
	case http.MethodPost:
		user := auth.CurrentUser(r)
		if user == nil { // RequireAdmin を通っていれば起きませんが、念のため
			JSONFail(w, http.StatusForbidden, "ログインが必要です")
			return
		}
		created, err := CreateMissingRequiredPages(user.Username)
		if err != nil {
			// **途中まで作ったものは残します**。何ができたかを添えて返すので、
			// 画面は「どこまで進んで、どれで止まったか」を出せます。
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"message": err.Error(),
				"created": created,
				"pages":   RequiredPageStatuses(),
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"created": created,
			"pages":   RequiredPageStatuses(),
		})
	default:
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
