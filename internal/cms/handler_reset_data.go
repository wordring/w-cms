package cms

// データの初期化の口（2026-09-16）。正本は reset_data.go の冒頭。
//
//	POST /api/admin/reset  {"confirm": "初期化"}
//
// **admin だけ**・**合言葉が要る**・**控えを取ってから**消します。

import (
	"encoding/json"
	"net/http"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ResetDataAPIHandler は POST /api/admin/reset です。
func ResetDataAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// **認可を先に**（ページを全部消す操作なので、いちばん重い関門）。
	if !page.RequireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	// ⚠ **合言葉はサーバーでも見ます。** 画面だけで守ると、`/api/admin/reset` を
	// 直に叩けば素通りします——**歯止めは押す側ではなく受ける側に置く**。
	if strings.TrimSpace(req.Confirm) != ResetConfirmWord {
		JSONFail(w, http.StatusBadRequest,
			"合言葉が違います（「"+ResetConfirmWord+"」と入力してください）")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil { // RequireAdmin を通っていれば起きませんが、念のため
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	sum, err := ResetData(user.Username)
	if err != nil {
		// **途中で失敗しても、控えは残っています。** どこまで進んだかを添えて返すので、
		// 画面は「控えはここにある」と言えます。
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
			"summary": sum,
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "summary": sum})
}
