package comm

// ─────────────────────────────────────────────────────────────────────────
// 「この記録への返信」を引く（2026-09-03）
//
// ユーザー:「返信の本体は送信箱にあるのはどうでしょう？そして、返信元のメールから
// ものぞき見できるのが良いかと」——**のぞき見は所有ではなく参照**です。
// 返信の本体は送信箱に立ち、こちらは逆引きで見えるだけ。返信元の本文は
// 一切変わりません（通信記録は届いたときのまま不変に保つ）。
//
// 逆引きの鍵は記録に書かれた `親ページID` タグ（値＝親の記録のページID・2026-10-09——それまでは w-cms が送った控えに
// だけ書く `返信元`）。出すのは**こちらが送った**返信（`向き`＝送信）——Outlook から送って取り込んだ返信も入る。
// 相手からの返信も含めた前後は `/api/thread`。索引を1回引くだけで、専用のテーブルも、返信元側への書き込みも要りません。
// ─────────────────────────────────────────────────────────────────────────

import (
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

// RepliesTo は pageID を親とする記録のうち、こちらが送ったもの（向き＝送信）を返します。
func RepliesTo(user *auth.User, pageID string) ([]ReplyRef, error) {
	ids, err := cms.PagesByTag(database.DB, ParentPageTag, pageID)
	if err != nil {
		return nil, err
	}
	out := make([]ReplyRef, 0, len(ids))
	for _, idInt := range ids {
		// **読めない相手には見せない**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, idInt) || cms.PageTagValue(database.DB, idInt, DirectionTag) != DirectionOut {
			continue
		}
		r := ReplyRef{PageID: page.FormatID(idInt), Title: cms.PageTitleByID(idInt),
			SentAt: cms.PageTagValue(database.DB, idInt, SentAtTag)}
		database.DB.QueryRow(
			// **畳んだ値がアドレス**（生の値は `名前 <アドレス>`）。2026-09-13 に1人1タグへ。
			`SELECT COALESCE(norm_value, value) FROM page_tags
			  WHERE page_id = ? AND name = ? LIMIT 1`,
			idInt, ToTag).Scan(&r.To)
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
	cms.WriteJSON(w, map[string]any{"success": true, "replies": replies})
}
