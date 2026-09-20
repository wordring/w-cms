package cms

// ─────────────────────────────────────────────────────────────────────────
// JSONで答える読み取り口の、共通の関門（2026-09-14）
//
// **同じ16行が7箇所に写されていました**——`Content-Type` → メソッド確認 →
// `NormalizeID` → `CurrentUser` → `Atoi` → `CanView` → 404。しかも**2箇所は
// メソッド確認が抜けていました**（`handler_replies`・`handler_thread`）。
// 写した回数だけ抜ける機会があった、ということです。
//
// **`RequirePageRead` とは別物です。** あちらは `text/plain` で断る汎用の関門で、
// ここが要るのは**JSONで答える口**のため——`text/plain` が返ると、受ける側は
// `res.json()` に失敗して理由を落とします（2026-09-14 に実際に起きていました）。
//
// **「読めない」と「無い」を区別させません。** どちらも 404 です——匿名に対する
// 404統一と同じ規律で、権限の無いページの存在が漏れないようにするためです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// queryPageID は `?id=` を、ゼロ詰め6桁と数値の両方で返します
// （`text/plain` で断る口の共通の入口）。不正なら 400 を書いて ok=false。
//
// **IDはハンドラの入口で6桁へ畳みます**（2026-09-14）。数値しか使わない口でも
// 畳んでおくのは、`page.GetPageDir(id)` / `page.AttachmentDir(id)` が**文字列を取る**
// ので、あとで1行足した人が `"1"` を渡すと `data/1/1.html` を探しに行くためです。
// **同じ7行が7つのハンドラに写されていました**（2026-09-21 に寄せた）。
func queryPageID(w http.ResponseWriter, r *http.Request) (id string, idInt int, ok bool) {
	return normalizedPageID(w, r.URL.Query().Get("id"))
}

// normalizedPageID は raw をゼロ詰め6桁と数値へ畳みます（不正なら 400 を書いて ok=false）。
func normalizedPageID(w http.ResponseWriter, raw string) (id string, idInt int, ok bool) {
	id, okID := page.NormalizeID(raw)
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return "", 0, false
	}
	return id, mustAtoi(id), true
}

// GateJSONPost は、JSONで答える**書き込み口**の入口です: `Content-Type` → POST の確認 →
// 利用者。断ったときは応答を書き終えていて ok=false。
//
// **同じ8行が拡張の6つの口に写されていました**（2026-09-21 に寄せた）。`/api/` は
// `RequireAuth` の内側なので利用者が nil になることは普通ありませんが、口ごとに
// 確かめる形はそのまま残します（ミドルウェアの入れ子は「黙って壊れる層」なので）。
func GateJSONPost(w http.ResponseWriter, r *http.Request) (user *auth.User, ok bool) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return nil, false
	}
	user = auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return nil, false
	}
	return user, true
}

// WriteJSON は JSON の応答を書きます（成功の応答の定型）。
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// GateJSONPageRead は、JSONで答える読み取り口の入口をまとめて通します。
//
//	pageID, idInt, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
//	if !ok {
//		return // 応答は書き終えている
//	}
//
// 通したときだけ `ok` が真で、`pageID` は**ゼロ詰め6桁**（サイドカーのパスに
// そのまま使える形）、`idInt` はその数値です。
//
// ⚠ **`user` は nil でありえます**（匿名で読める公開ページ）。呼ぶ側が利用者を
// 名指しで使うなら、そこで確かめること。
func GateJSONPageRead(w http.ResponseWriter, r *http.Request, rawID string) (pageID string, idInt int, user *auth.User, ok bool) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return "", 0, nil, false
	}
	pageID, okID := page.NormalizeID(rawID)
	if !okID {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return "", 0, nil, false
	}
	user = auth.CurrentUser(r)
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		// 読めない相手には「無い」と同じ顔を見せる（匿名の404統一と同じ規律）。
		JSONFail(w, http.StatusNotFound, "ページが見つかりません")
		return "", 0, nil, false
	}
	return pageID, idInt, user, true
}
