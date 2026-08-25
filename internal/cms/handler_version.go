package cms

// 版管理（リビジョン／リバート）のAPI。設計は version.go の冒頭と
// [docs/【考察】アンドゥ・リドゥ.md] §4・§5。
//
// 認可の考え方は「**版は本文そのもの**」の一言に尽きます——一覧と取得は本文と同じ
// read、書き戻しは本文と同じ write ＋編集ロック。ここを緩めると、版が本文の
// 認可を迂回する裏口になります。

import (
	"encoding/json"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// VersionsAPIHandler は版の一覧を新しい順で返します（GET /api/versions?id=）。
func VersionsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := page.NormalizeID(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageRead(w, r, id) {
		return
	}
	list, err := ListVersions(id)
	if err != nil {
		http.Error(w, "版の一覧を取得できませんでした", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// VersionAPIHandler は指定した版の本文を返します（GET /api/version?id=&v=）。
//
// 本文APIと同じく `text/plain` ＋ `nosniff` で返します——このURLを直接ブラウザで
// 開いてもHTMLとして実行されないようにするためです（多層防御）。**描画時と同じく
// サニタイズを通す**のも同じ理由で、当時の許可リストで通った記述を素通しにしません。
func VersionAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := page.NormalizeID(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageRead(w, r, id) {
		return
	}
	body, err := ReadVersion(id, r.URL.Query().Get("v"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(Sanitize(string(body))))
}

// RevertAPIHandler は選んだ版を現在の本文として書き戻します（POST /api/revert）。
func RevertAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PageID  string `json:"page_id"`
		Version string `json:"version"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	id, ok := page.NormalizeID(req.PageID)
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	// 本文を書き換える操作なので、保存とまったく同じ守りを掛ける。
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	u := auth.CurrentUser(r)
	author := page.DefaultOwner
	if u != nil {
		author = u.Username
	}
	if err := RevertToVersion(id, req.Version, author); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if u != nil {
		// 監査記録: どの版へ戻したかが分からないと後から追えない。
		auth.Audit(u.Username, "revert", id+" -> "+req.Version)
	}

	body, err := ReadVersion(id, req.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"page_id": id,
		"version": req.Version,
		// エディタが載せ替えられるよう、戻した本文をそのまま返す。
		"html": Sanitize(string(body)),
	})
}
