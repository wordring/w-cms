package cms

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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

// LockAPIHandler は編集ロックの取得（および待機者の再試行ポーリング）を処理します。
// POST /api/lock?id= 。対象ページの write 権限を要求します。
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

	res := pageLocks.TryAcquire(idInt, user.Username)
	w.Header().Set("Content-Type", "application/json")
	if !res.Acquired {
		// 他者が編集中 → 423 Locked。保持者名と明け渡しまでの残り秒を返す。
		w.WriteHeader(http.StatusLocked)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":                  false,
			"holder":              res.Holder,
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

// LockStatusAPIHandler は保持者の死活更新と「待機者あり」通知のための状態を返します。
// GET /api/lock-status?id=&token= 。対象ページの write 権限を要求します。
func LockStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	token := r.URL.Query().Get("token")

	st := pageLocks.Status(idInt, user.Username, token)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"is_holder":           st.IsHolder,
		"holder":              st.Holder,
		"waiter_present":      st.WaiterPresent,
		"grace_remaining_sec": int(st.GraceRemaining.Seconds()),
		"lost":                st.Lost,
	})
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
