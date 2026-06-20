package auth

import (
	"encoding/json"
	"net/http"
)

// 管理API（すべて admin 限定）。フェーズ3。
// ユーザー・グループ・監査ログを操作します。

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "リクエストの形式が不正です", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// UsersAPIHandler は GET で一覧、POST でユーザー作成を行います（admin限定）。
func UsersAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	actor := CurrentUser(r).Username

	switch r.Method {
	case http.MethodGet:
		users, err := ListUsers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, users)
	case http.MethodPost:
		var req struct {
			Username     string `json:"username"`
			Password     string `json:"password"`
			IsAdmin      bool   `json:"is_admin"`
			PrimaryGroup string `json:"primary_group"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Username == "" {
			http.Error(w, "username は必須です", http.StatusBadRequest)
			return
		}
		if err := CreateUser(req.Username, req.Password, req.IsAdmin, req.PrimaryGroup); err != nil {
			http.Error(w, "ユーザー作成に失敗しました: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.PrimaryGroup != "" {
			CreateGroup(req.PrimaryGroup)
		}
		Audit(actor, "user.create", req.Username)
		writeJSON(w, map[string]interface{}{"success": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// UserPasswordAPIHandler はユーザーのパスワードをリセットします（admin限定）。
func UserPasswordAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	actor := CurrentUser(r).Username
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username と password は必須です", http.StatusBadRequest)
		return
	}
	if err := SetPassword(req.Username, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	Audit(actor, "user.password", req.Username)
	writeJSON(w, map[string]interface{}{"success": true})
}

// UserDisableAPIHandler はユーザーの有効/無効を切り替えます（admin限定）。
func UserDisableAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	actor := CurrentUser(r).Username
	var req struct {
		Username string `json:"username"`
		Disabled bool   `json:"disabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := SetDisabled(req.Username, req.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := "user.enable"
	if req.Disabled {
		action = "user.disable"
	}
	Audit(actor, action, req.Username)
	writeJSON(w, map[string]interface{}{"success": true})
}

// GroupsAPIHandler は GET で一覧、POST でグループ作成を行います（admin限定）。
func GroupsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	actor := CurrentUser(r).Username

	switch r.Method {
	case http.MethodGet:
		groups, err := ListGroups()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, groups)
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == "" {
			http.Error(w, "name は必須です", http.StatusBadRequest)
			return
		}
		if err := CreateGroup(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		Audit(actor, "group.create", req.Name)
		writeJSON(w, map[string]interface{}{"success": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// GroupMembersAPIHandler はグループのメンバーを追加/削除します（admin限定）。
func GroupMembersAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	actor := CurrentUser(r).Username
	var req struct {
		Username string `json:"username"`
		Group    string `json:"group"`
		Action   string `json:"action"` // "add" | "remove"
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Group == "" {
		http.Error(w, "username と group は必須です", http.StatusBadRequest)
		return
	}
	var err error
	switch req.Action {
	case "remove":
		err = RemoveGroupMember(req.Username, req.Group)
		Audit(actor, "group.remove", req.Username+"@"+req.Group)
	default: // add
		err = AddGroupMember(req.Username, req.Group)
		Audit(actor, "group.add", req.Username+"@"+req.Group)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"success": true})
}

// AuditAPIHandler は最近の監査ログを返します（admin限定）。
func AuditAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	entries, err := RecentAudit(200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}
