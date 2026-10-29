package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
	"w-cms/internal/database"
)

// 管理API（すべて admin 限定）。フェーズ3。
// ユーザー・グループ・監査ログを操作します。

// maxAdminJSONBodyBytes は管理APIの本文サイズ上限です。要求はユーザー名・グループ名
// 程度の小さなJSONしか無いので 1MiB で十分（上限が無いとメモリを圧迫できる——
// 2026-08-05 監査の指摘。本文保存側の上限は internal/cms の maxJSONBodyBytes）。
const maxAdminJSONBodyBytes = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminJSONBodyBytes)
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
		if why := badUsername(req.Username); why != "" {
			http.Error(w, why, http.StatusBadRequest)
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

// badUsername は、作れない利用者名なら理由を返します（2026-10-26。それまでは何でも作れた——前後の空白は、ログインで打つ
// 名前と一致せず入れない利用者になり、制御文字は監査の記録を崩した）。中の空白は使える（連絡帳の人の題「姓 名」に合わせる）。
func badUsername(name string) string {
	if strings.TrimSpace(name) != name {
		return "利用者名の前後に空白は入れられません"
	}
	if utf8.RuneCountInString(name) > 64 {
		return "利用者名は64文字までです"
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "利用者名に制御文字は入れられません"
		}
	}
	return ""
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

// UserMailAPIHandler は、利用者がメールにサインインしてよいアドレスを決めます（POST・admin限定・2026-10-27・mail_address.go）。
// 入力: {username, addresses: ["…", …]}（空なら全部外す——その利用者はメールにサインインできない）。
func UserMailAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !RequireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username  string   `json:"username"`
		Addresses []string `json:"addresses"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		http.Error(w, "username は必須です", http.StatusBadRequest)
		return
	}
	var n int
	if err := database.AuthDB.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, req.Username).Scan(&n); err != nil || n == 0 {
		http.Error(w, "その利用者はいません", http.StatusNotFound)
		return
	}
	if err := SetMailAddresses(req.Username, req.Addresses); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	Audit(CurrentUser(r).Username, "user.mail", req.Username+": "+strings.Join(MailAddressesOf(req.Username), ", "))
	writeJSON(w, map[string]interface{}{"success": true, "addresses": MailAddressesOf(req.Username)})
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
	// 自分と、最後の有効な管理者は無効にしない（2026-10-02 に a を無効にして、だれも入れなくなった・2026-10-22 の見直し）。
	if req.Disabled {
		if req.Username == actor {
			http.Error(w, "自分を無効にはできません（ほかの管理者で入って、無効にしてください）", http.StatusConflict)
			return
		}
		if lastEnabledAdmin(req.Username) {
			http.Error(w, "最後の有効な管理者は無効にできません（先にほかの管理者を作ってください）", http.StatusConflict)
			return
		}
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
	action := "group.add"
	switch req.Action {
	case "remove":
		action = "group.remove"
		err = RemoveGroupMember(req.Username, req.Group)
	default: // add
		err = AddGroupMember(req.Username, req.Group)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 監査は、変えられたときだけ残す（それまでは失敗した追加・削除も残っていた——【考察】コードの矛盾 §2.5・2026-10-29）。
	Audit(actor, action, req.Username+"@"+req.Group)
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
