package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ページ権限の管理API（フェーズ3）。サイドカー（正本）を書き換えてから
// page_perms を更新します。これらのAPIのみがサイドカーを書き換えます。

// PagePermsHandler は GET で現在の権限を返し、POST で mode / group を変更します（chmod / chgrp）。
// GET: read 権限。POST: 対象ページの owner または admin（認証認可設計.md 3.5節）。
func PagePermsHandler(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	// サイドカーのパスに使うためゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	cur := page.GetPerms(pageID)

	// GET: 現在の権限を返す（read権限が必要）
	if r.Method == http.MethodGet {
		if !cur.CanRead(u) {
			http.Error(w, "このページの権限を参照できません", http.StatusForbidden)
			return
		}
		canPublish := u.IsAdmin || u.Username == cur.Owner
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"owner": cur.Owner, "group": cur.Group, "mode": cur.Mode,
			"can_chmod": canPublish, "can_chown": u.IsAdmin,
			// 匿名公開（認証認可設計.md 10章）。public はこのページ自身のフラグ、
			// effective_public は親チェーンとの AND による実際の公開状態。
			"public": cur.Public, "effective_public": page.EffectivePublic(pageID),
			"can_publish": canPublish, "can_write": cur.CanWrite(u),
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !u.IsAdmin && u.Username != cur.Owner {
		http.Error(w, "権限を変更できるのは所有者または管理者のみです", http.StatusForbidden)
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	var req struct {
		Mode   *string `json:"mode"`
		Group  *string `json:"group"`
		Public *bool   `json:"public"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}

	// 現在のサイドカー（正本）を起点に変更する。読めなければ**書かずに止める**
	// ——派生（page_perms）から組み立て直して書き戻すと、親も作成情報も失ったまま
	// 「見た目は健全なサイドカー」を新造してしまう（2026-08-21 決定）。
	p, ok := page.ReadSidecar(id)
	if !ok {
		http.Error(w, "ページ属性ファイルを読めないため権限を変更できません。管理者が手作業で修復してください。",
			http.StatusConflict)
		return
	}

	action := ""
	if req.Mode != nil {
		if !page.ValidMode(*req.Mode) {
			http.Error(w, "mode は3桁・各桁0〜3で指定してください（例: 330）", http.StatusBadRequest)
			return
		}
		p.Mode = *req.Mode
		action = "chmod"
	}
	if req.Group != nil {
		p.Group = *req.Group
		if action == "" {
			action = "chgrp"
		}
	}
	if req.Public != nil {
		// 匿名公開フラグの変更（認証認可設計.md 10章）。公開（true）にするときだけ
		// パスゲートを検証する: 親が実効公開でなければ公開できない（ルートは親なしで可）。
		if *req.Public && !parentIsPublishable(pageID) {
			http.Error(w, "親ページが公開されていないため公開できません。先に親ページを公開するか、公開可能な親へ移動してください。", http.StatusForbidden)
			return
		}
		p.Public = *req.Public
		if action == "" {
			if *req.Public {
				action = "publish"
			} else {
				action = "unpublish"
			}
		}
	}
	if action == "" {
		http.Error(w, "mode・group・public のいずれかを指定してください", http.StatusBadRequest)
		return
	}

	if err := page.WriteSidecar(id, p); err != nil {
		http.Error(w, "サイドカーの書き込みに失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := page.RefreshPerms(id); err != nil {
		http.Error(w, "権限インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	auth.Audit(u.Username, action, id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "owner": p.Owner, "group": p.Group, "mode": p.Mode,
		"public": p.Public, "effective_public": page.EffectivePublic(pageID),
	})
}

// parentIsPublishable は、ページ pageID を匿名公開してよいか（親チェーンが許すか）を返します。
// ルート（ID 0）は親なしで公開可。それ以外は親が実効公開（page.EffectivePublic）である必要があります
// （認証認可設計.md 10.2 のパスゲートを公開操作時に強制する）。
func parentIsPublishable(pageID int) bool {
	if pageID == 0 {
		return true // ルートは親なしで公開可（サイト全体のキルスイッチ側）
	}
	var parent sql.NullInt64
	if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", pageID).Scan(&parent); err != nil {
		return false
	}
	if !parent.Valid {
		return false // ルート以外で親なしは異常。安全側に倒す
	}
	return page.EffectivePublic(int(parent.Int64))
}

// PageChownHandler は所有者を変更します（chown）。権限: admin のみ。
func PageChownHandler(w http.ResponseWriter, r *http.Request) {
	if !page.RequireAdmin(w, r) {
		return
	}
	// サイドカーのパスに使うためゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if _, err := strconv.Atoi(id); err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	var req struct {
		Owner string `json:"owner"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	if req.Owner == "" {
		http.Error(w, "owner は必須です", http.StatusBadRequest)
		return
	}

	p, ok := page.ReadSidecar(id)
	if !ok {
		http.Error(w, "ページ属性ファイルを読めないため所有者を変更できません。管理者が手作業で修復してください。",
			http.StatusConflict)
		return
	}
	p.Owner = req.Owner

	if err := page.WriteSidecar(id, p); err != nil {
		http.Error(w, "サイドカーの書き込みに失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := page.RefreshPerms(id); err != nil {
		http.Error(w, "権限インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	auth.Audit(auth.CurrentUser(r).Username, "chown", id+"->"+req.Owner)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "owner": p.Owner})
}
