package editlock

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// readPageHTML はページの本文HTMLをファイルから読み込みます。
func readPageHTML(id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RequireEditLock は、エディタ内の変更操作が現在の編集ロック保持者から来ていることを要求します。
// 本文保存・権限変更・親付け替え・将来のリソース操作（画像/PDF等）を、本文編集と同じロックで
// 直列化するための共通ゲートです。トークンは `X-Lock-Token` ヘッダ（無ければ `token` クエリ）で受けます。
//
// 検証規約は SaveAPIHandler と同一です（`Locks.Validate`）:
//   - ロックが無ければ許可（無競合。フロント未対応でも従来どおり動く）。
//   - 自分が保持者でトークン一致なら許可。
//   - 他者が保持中／トークン失効なら 409 Conflict を書いて false を返す。
// 許可なら true。呼び出し側は権限チェック（page.RequirePageWrite 等）の後にこれを通します。
func RequireEditLock(w http.ResponseWriter, r *http.Request, idStr string) bool {
	u := auth.CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return false
	}
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	token := r.Header.Get("X-Lock-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if !Locks.Validate(pageID, u.Username, token) {
		http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
		return false
	}
	return true
}

// LockAPIHandler は編集ロックの取得を処理します。
// POST /api/lock?id=&token= 。対象ページの write 権限を要求します。
// 取得成功時は最新HTMLも返し、クライアントが古い版で上書きしないよう再ロードさせます。
func LockAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 本文の読み込み（readPageHTML）のパスに使うためゼロ詰め6桁へ正規化する。
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}

	// token は再取得（同一エディタ）の検証用。新規取得時は空。
	res := Locks.TryAcquire(idInt, user.Username, r.URL.Query().Get("token"))
	w.Header().Set("Content-Type", "application/json")
	if !res.Acquired {
		w.WriteHeader(http.StatusLocked)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                  false,
			"holder":              res.Holder,
			"same_user":           res.SameUser,
			"grace_remaining_sec": int(res.GraceRemaining.Seconds()),
		})
		return
	}
	html, _ := readPageHTML(id) // 取得成功 → 最新HTMLを同梱（読めなければ空）
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"token": res.Token,
		"html":  html,
	})
}

// LockEventsAPIHandler はロック状態の変化を SSE（text/event-stream）でプッシュします。
// GET /api/lock-events?id=&role=holder|waiter&token= 。対象ページの write 権限を要求します。
// 接続中＝presence とみなし、保持者の切断は即明け渡し、待機者の切断は猶予キャンセルに使います。
func LockEventsAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "ストリーミング非対応", http.StatusInternalServerError)
		return
	}

	role := r.URL.Query().Get("role")
	if role != "holder" {
		role = "waiter"
	}
	token := r.URL.Query().Get("token")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // リバースプロキシのバッファリング抑止

	s := Locks.subscribe(idInt, role, user.Username, token)
	defer Locks.unsubscribe(idInt, s)

	w.Write([]byte(": connected\n\n")) // 初期コメント（接続確立）
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case ev, open := <-s.ch:
			if !open {
				return
			}
			b, _ := json.Marshal(ev)
			w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		case <-keepalive.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// UnlockAPIHandler は保持者本人によるロックの明示解放です。
// POST /api/unlock?id=&token= 。タブ離脱時の navigator.sendBeacon からも呼ばれます。
func UnlockAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	idInt, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	Locks.Release(idInt, user.Username, r.URL.Query().Get("token"))
	w.WriteHeader(http.StatusNoContent)
}

// LockForceAPIHandler は admin による強制解除です（保持者が落ちてスタックした場合の救済）。
// POST /api/lock/force?id= 。
func LockForceAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !page.RequireAdmin(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	Locks.ForceRelease(idInt)
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "lock.force", id)
	}
	w.WriteHeader(http.StatusNoContent)
}
