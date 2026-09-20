package editlock

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// RequireEditLock は、エディタ内の変更操作が現在の編集ロック保持者から来ていることを要求します。
// 本文保存・権限変更・親付け替え・将来のリソース操作（画像/PDF等）を、本文編集と同じロックで
// 直列化するための共通ゲートです。トークンは `X-Lock-Token` ヘッダ（無ければ `token` クエリ）で受けます。
//
// 検証規約は SaveAPIHandler と同一です（`Locks.Validate`）:
//   - ロックが無ければ許可（無競合。フロント未対応でも従来どおり動く）。
//   - 自分が保持者でトークン一致なら許可。
//   - 他者が保持中／トークン失効なら 409 Conflict を書いて false を返す。
//
// 許可なら true。呼び出し側は権限チェック（page.RequirePageWrite 等）の後にこれを通します。
func RequireEditLock(w http.ResponseWriter, r *http.Request, idStr string) bool {
	token := r.Header.Get("X-Lock-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	return RequireLockToken(w, r, idStr, token)
}

// RequireLockToken は、呼ぶ側が取り出したトークンで同じ検証をします
// （保存APIはトークンを JSON 本文で受けるので、取り出し方だけが違う）。
// 断り文とステータス（409）はここ1か所です。
func RequireLockToken(w http.ResponseWriter, r *http.Request, idStr, token string) bool {
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
	if !Locks.Validate(pageID, u.Username, token) {
		http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
		return false
	}
	return true
}

// RefuseWhileEditing は、**誰かがそのページを開いていたら 409 で断ります**（2026-09-14）。
//
// **機械が本文を書き換える口のための関門**です。一覧画面のボタン（連絡先の登録・
// 「対応：不要」・整理の実行）は**エディタを開いていない**ので編集トークンを持たず、
// `RequireEditLock` を通すと必ず断られます。かといって素通しにすると、
// **`RewriteBody` は読んで・変えて・書く**ので、誰かがエディタを開いていると
// オートセーブと機械の書き込みが黙って上書きし合います。
//
// **自分が開いている場合も断ります。** 手元のエディタは書き換え前の本文を持って
// いるので、そのまま保存すれば機械の変更が消えます——「自分だから安全」ではありません。
//
// 断るのは**いま開いている人が居るとき**だけです（`EditorOpen`）。ブラウザを
// 閉じただけの人のロックで永久に止まらないようにするため。
func RefuseWhileEditing(w http.ResponseWriter, idStr string) bool {
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	if holder, open := Locks.EditorOpen(pageID); open {
		http.Error(w,
			"このページは編集中です（"+holder+"）。閉じてからもう一度お試しください。",
			http.StatusConflict)
		return false
	}
	return true
}

// queryPageID は `?id=` を読み、ゼロ詰め6桁と数値の両方で返します。
// 不正なら 400 を書いて ok=false。
//
// **IDはハンドラの入口で6桁へ畳みます**（2026-09-14）。数値しか使わない口でも
// 畳んでおくのは、`page.GetPageDir(id)` / `page.AttachmentDir(id)` が**文字列を取る**
// ので、あとで1行足した人が `"1"` を渡すと `data/1/1.html` を探しに行くためです。
func queryPageID(w http.ResponseWriter, r *http.Request) (id string, idInt int, ok bool) {
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return "", 0, false
	}
	idInt, _ = strconv.Atoi(id) // NormalizeID を通った値は必ず数
	return id, idInt, true
}

// requireWriter は対象ページの write 権限を確かめ、その利用者を返します
// （断ったときは応答を書き終えていて nil）。ロックの口はすべて write が要ります。
func requireWriter(w http.ResponseWriter, r *http.Request, id string) *auth.User {
	if !page.RequirePageWrite(w, r, id) {
		return nil
	}
	return auth.CurrentUser(r) // RequirePageWrite が通っていれば nil ではない
}

// LockAPIHandler は編集ロックの取得を処理します。
// POST /api/lock?id=&token= 。対象ページの write 権限を要求します。
// 本文は返しません（取得口は GET /api/load の1つ。クライアントは取得後に読み直す）。
func LockAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, idInt, ok := queryPageID(w, r)
	if !ok {
		return
	}
	user := requireWriter(w, r, id)
	if user == nil {
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
	// 本文は同梱しない。ロック取得後にフロントが GET /api/load を読む
	// （そちらは計算ビューのサーバー事前描画を通るため。生のファイルを返すと
	// 編集モードへ入った瞬間にビューの中身が消える）。
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"token": res.Token,
	})
}

// LockEventsAPIHandler はロック状態の変化を SSE（text/event-stream）でプッシュします。
// GET /api/lock-events?id=&role=holder|waiter&token= 。対象ページの write 権限を要求します。
// 接続中＝presence とみなし、保持者の切断は即明け渡し、待機者の切断は猶予キャンセルに使います。
func LockEventsAPIHandler(w http.ResponseWriter, r *http.Request) {
	id, idInt, ok := queryPageID(w, r)
	if !ok {
		return
	}
	user := requireWriter(w, r, id)
	if user == nil {
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
	id, idInt, ok := queryPageID(w, r)
	if !ok {
		return
	}
	Locks.ForceRelease(idInt)
	auth.Audit(auth.CurrentUser(r).Username, "lock.force", id) // RequireAdmin が通っていれば nil ではない
	w.WriteHeader(http.StatusNoContent)
}
