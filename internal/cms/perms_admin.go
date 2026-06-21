package cms

import (
	"encoding/json"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
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
	id := r.URL.Query().Get("id")
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	cur := GetPerms(pageID)

	// GET: 現在の権限を返す（read権限が必要）
	if r.Method == http.MethodGet {
		if !cur.CanRead(u) {
			http.Error(w, "このページの権限を参照できません", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"owner": cur.Owner, "group": cur.Group, "mode": cur.Mode,
			"can_chmod": u.IsAdmin || u.Username == cur.Owner, "can_chown": u.IsAdmin,
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

	var req struct {
		Mode  *string `json:"mode"`
		Group *string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "リクエストの形式が不正です", http.StatusBadRequest)
		return
	}

	// 現在のサイドカー（無ければ現在の実効権限）を起点に変更する
	p, ok := ReadSidecar(id)
	if !ok {
		p = PageMeta{Owner: cur.Owner, Group: cur.Group, Mode: cur.Mode}
	}

	action := ""
	if req.Mode != nil {
		if !ValidMode(*req.Mode) {
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
	if action == "" {
		http.Error(w, "mode または group を指定してください", http.StatusBadRequest)
		return
	}

	if err := WriteSidecar(id, p); err != nil {
		http.Error(w, "サイドカーの書き込みに失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := RefreshPerms(id); err != nil {
		http.Error(w, "権限インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	auth.Audit(u.Username, action, id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "owner": p.Owner, "group": p.Group, "mode": p.Mode})
}

// PageChownHandler は所有者を変更します（chown）。権限: admin のみ。
func PageChownHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	var req struct {
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "リクエストの形式が不正です", http.StatusBadRequest)
		return
	}
	if req.Owner == "" {
		http.Error(w, "owner は必須です", http.StatusBadRequest)
		return
	}

	cur := GetPerms(pageID)
	p, ok := ReadSidecar(id)
	if !ok {
		p = PageMeta{Owner: cur.Owner, Group: cur.Group, Mode: cur.Mode}
	}
	p.Owner = req.Owner

	if err := WriteSidecar(id, p); err != nil {
		http.Error(w, "サイドカーの書き込みに失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := RefreshPerms(id); err != nil {
		http.Error(w, "権限インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	auth.Audit(auth.CurrentUser(r).Username, "chown", id+"->"+req.Owner)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "owner": p.Owner})
}
