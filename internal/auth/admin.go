package auth

import (
	"errors"
	"net/http"
	"time"

	"w-cms/internal/database"
)

// ErrNotFound は対象ユーザー等が存在しない場合に返されます。
var ErrNotFound = errors.New("対象が見つかりません")

// UserInfo は一覧表示用のユーザー情報です。
type UserInfo struct {
	Username     string `json:"username"`
	IsAdmin      bool   `json:"is_admin"`
	PrimaryGroup string `json:"primary_group"`
	Disabled     bool   `json:"disabled"`
}

// RequireAdmin はadmin権限を要求します（auth側のHTTPハンドラ用）。
func RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u := CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return false
	}
	if !u.IsAdmin {
		http.Error(w, "管理者権限が必要です", http.StatusForbidden)
		return false
	}
	return true
}

// SetPassword はユーザーのパスワードを再設定します（admin によるリセット）。
func SetPassword(username, newPassword string) error {
	h, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := database.AuthDB.Exec(`UPDATE users SET password_hash = ? WHERE username = ?`, h, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	clearFailures(username)
	return nil
}

// SetDisabled はユーザーの有効/無効を切り替えます。
func SetDisabled(username string, disabled bool) error {
	d := 0
	if disabled {
		d = 1
	}
	res, err := database.AuthDB.Exec(`UPDATE users SET disabled = ? WHERE username = ?`, d, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUsers は全ユーザーを返します。
func ListUsers() ([]UserInfo, error) {
	rows, err := database.AuthDB.Query(`SELECT username, is_admin, COALESCE(primary_group, ''), disabled FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []UserInfo
	for rows.Next() {
		var u UserInfo
		var isAdmin, disabled int
		if err := rows.Scan(&u.Username, &isAdmin, &u.PrimaryGroup, &disabled); err == nil {
			u.IsAdmin = isAdmin != 0
			u.Disabled = disabled != 0
			users = append(users, u)
		}
	}
	return users, nil
}

// CreateGroup はグループを作成します（既存なら何もしない）。
func CreateGroup(name string) error {
	_, err := database.AuthDB.Exec(`INSERT OR IGNORE INTO groups (name) VALUES (?)`, name)
	return err
}

// AddGroupMember はユーザーをグループに追加します（グループは無ければ作成）。
func AddGroupMember(username, group string) error {
	if err := CreateGroup(group); err != nil {
		return err
	}
	_, err := database.AuthDB.Exec(
		`INSERT OR IGNORE INTO group_members (username, group_name) VALUES (?, ?)`, username, group)
	return err
}

// RemoveGroupMember はユーザーをグループから外します。
func RemoveGroupMember(username, group string) error {
	_, err := database.AuthDB.Exec(
		`DELETE FROM group_members WHERE username = ? AND group_name = ?`, username, group)
	return err
}

// ListGroups は全グループ名を返します。
func ListGroups() ([]string, error) {
	rows, err := database.AuthDB.Query(`SELECT name FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err == nil {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// Audit は監査ログに1件記録します（失敗しても本処理は止めない）。
// action例: "save"（書込）, "chmod", "chown", "chgrp", "user.create", "group.add" など。
func Audit(username, action, target string) {
	if database.AuthDB == nil {
		return
	}
	database.AuthDB.Exec(
		`INSERT INTO audit_log (ts, username, action, target) VALUES (?, ?, ?, ?)`,
		time.Now(), username, action, target)
}

// AuditEntry は監査ログの1行です。
type AuditEntry struct {
	TS       string `json:"ts"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Target   string `json:"target"`
}

// RecentAudit は新しい順に最大 limit 件の監査ログを返します。
func RecentAudit(limit int) ([]AuditEntry, error) {
	rows, err := database.AuthDB.Query(
		`SELECT ts, username, action, target FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ts time.Time
		if err := rows.Scan(&ts, &e.Username, &e.Action, &e.Target); err == nil {
			e.TS = ts.UTC().Format(time.RFC3339)
			entries = append(entries, e)
		}
	}
	return entries, nil
}
