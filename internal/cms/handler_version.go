package cms

// 版管理（リビジョン／リバート）のAPI。設計は version.go の冒頭と
// [docs/【考察】アンドゥ・リドゥ.md] §4・§5。
//
// 認可の考え方は「**版は本文そのもの**」の一言に尽きます——一覧と取得は本文と同じ
// read、書き戻しは本文と同じ write ＋編集ロック。ここを緩めると、版が本文の
// 認可を迂回する裏口になります。

import (
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
	id, _, ok := queryPageID(w, r)
	if !ok {
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
	WriteJSON(w, list)
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
	id, idInt, ok := queryPageID(w, r)
	if !ok {
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
	// 本文を読む経路はすべて同じ扱い（サニタイズ二層目＋計算ビューの事前描画。
	// docs/本文サニタイズ設計.md §4）——ここだけ素通しだと、版を開いた鏡の中身が空になる。
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(RenderComputedViews(r, idInt, Sanitize(string(body)))))
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
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	id, idInt, ok := normalizedPageID(w, req.PageID)
	if !ok {
		return
	}
	// 本文を書き換える操作なので、保存とまったく同じ守りを掛ける。
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	author := auth.UsernameOf(r) // RequirePageWrite が通っていれば空ではない
	if err := RevertToVersion(id, req.Version, author); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 監査記録: どの版へ戻したかが分からないと後から追えない。
	auth.AuditRequest(r, "revert", id+" -> "+req.Version)

	body, err := ReadVersion(id, req.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteJSON(w, map[string]any{
		"success": true,
		"page_id": id,
		"version": req.Version,
		// エディタが載せ替えられるよう、戻した本文を返す（/api/load と同じく
		// 計算ビューの中身を埋めて——載せ替え直後に鏡が空にならないように）。
		"html": RenderComputedViews(r, idInt, Sanitize(string(body))),
	})
}
