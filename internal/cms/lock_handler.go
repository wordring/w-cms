package cms

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"w-cms/internal/auth"
)

// readPageHTML はページの本文HTMLをファイルから読み込みます。
func readPageHTML(id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(GetPageDir(id), id+".html"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LockAPIHandler は編集ロックの取得を処理します。
// POST /api/lock?id=&token= 。対象ページの write 権限を要求します。
// 取得成功時は最新HTMLも返し、クライアントが古い版で上書きしないよう再ロードさせます。
func LockAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if !RequirePageWrite(w, r, id) {
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
	res := pageLocks.TryAcquire(idInt, user.Username, r.URL.Query().Get("token"))
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
	if !RequirePageWrite(w, r, id) {
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

	s := pageLocks.subscribe(idInt, role, user.Username, token)
	defer pageLocks.unsubscribe(idInt, s)

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
	pageLocks.Release(idInt, user.Username, r.URL.Query().Get("token"))
	w.WriteHeader(http.StatusNoContent)
}

// LockForceAPIHandler は admin による強制解除です（保持者が落ちてスタックした場合の救済）。
// POST /api/lock/force?id= 。
func LockForceAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !RequireAdmin(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	pageLocks.ForceRelease(idInt)
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "lock.force", id)
	}
	w.WriteHeader(http.StatusNoContent)
}
