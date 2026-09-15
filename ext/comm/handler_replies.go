package comm

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
	"net/http"
	"w-cms/internal/cms"

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
	ids, err := cms.PagesByTag(database.DB, ReplySourceTag, pageID)
	if err != nil {
		return nil, err
	}
	out := make([]ReplyRef, 0, len(ids))
	for _, idInt := range ids {
		// **読めない相手には見せない**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, idInt) {
			continue
		}
		r := ReplyRef{PageID: page.FormatID(idInt), Title: cms.PageTitleByID(idInt)}
		database.DB.QueryRow(
			`SELECT value FROM page_tags WHERE page_id = ? AND name = '送信日時' LIMIT 1`,
			idInt).Scan(&r.SentAt)
		database.DB.QueryRow(
			// **畳んだ値がアドレス**（生の値は `名前 <アドレス>`）。2026-09-13 に1人1タグへ。
			`SELECT COALESCE(norm_value, value) FROM page_tags
			  WHERE page_id = ? AND name = '宛先' LIMIT 1`,
			idInt).Scan(&r.To)
		out = append(out, r)
	}
	return out, nil
}

// RepliesAPIHandler は GET /api/replies?page_id=X です。
func RepliesAPIHandler(w http.ResponseWriter, r *http.Request) {
	// **メソッド確認もここに入りました**——写していたころは、この2つの口だけ
	// 抜けていました（GET専用のつもりで書いて、書き忘れに誰も気づかない形）。
	pageID, _, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	replies, err := RepliesTo(user, pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "返信を引けません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "replies": replies})
}
