package cms

// ─────────────────────────────────────────────────────────────────────────
// 「この記録への返信」を引く（2026-09-03）
//
// ユーザー:「返信の本体は送信箱にあるのはどうでしょう？そして、返信元のメールから
// ものぞき見できるのが良いかと」——**のぞき見は所有ではなく参照**です。
// 返信の本体は送信箱に立ち、こちらは逆引きで見えるだけ。返信元の本文は
// 一切変わりません（通信記録は届いたときのまま不変に保つ）。
//
// 逆引きの鍵は送信記録に書かれた `返信元` タグ（値＝返信元のページID）。
// 索引を1回引くだけで、専用のテーブルも、返信元側への書き込みも要りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ReplyRef は「この記録への返信」1件です。
type ReplyRef struct {
	PageID string `json:"page_id"`
	Title  string `json:"title"`
	SentAt string `json:"sent_at"`
	To     string `json:"to"`
}

// RepliesTo は pageID を返信元とする記録を、送った順に返します。
func RepliesTo(user *auth.User, pageID string) ([]ReplyRef, error) {
	ids, err := PagesByTag(database.DB, ReplySourceTag, pageID)
	if err != nil {
		return nil, err
	}
	out := make([]ReplyRef, 0, len(ids))
	for _, idInt := range ids {
		// **読めない相手には見せない**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, idInt) {
			continue
		}
		r := ReplyRef{PageID: fmt.Sprintf("%0*d", page.IDLength, idInt)}
		database.DB.QueryRow(`SELECT COALESCE(title,'') FROM pages WHERE id = ?`, idInt).Scan(&r.Title)
		database.DB.QueryRow(
			`SELECT value FROM vocab_index WHERE page_id = ? AND field = '送信日時' LIMIT 1`,
			idInt).Scan(&r.SentAt)
		database.DB.QueryRow(
			`SELECT value FROM vocab_index WHERE page_id = ? AND field = '宛先アドレス' LIMIT 1`,
			idInt).Scan(&r.To)
		out = append(out, r)
	}
	return out, nil
}

// RepliesAPIHandler は GET /api/replies?page_id=X です。
func RepliesAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pageID, ok := page.NormalizeID(r.URL.Query().Get("page_id"))
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	user := auth.CurrentUser(r)
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		// 読めない相手には「無い」と同じ顔を見せる（匿名の404統一と同じ規律）。
		JSONFail(w, http.StatusNotFound, "ページが見つかりません")
		return
	}
	replies, err := RepliesTo(user, pageID)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "返信を引けません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "replies": replies})
}
