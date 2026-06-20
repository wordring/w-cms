package auth

import "testing"

func TestUserManagement(t *testing.T) {
	setupAuthDB(t)

	if err := CreateUser("alice", "oldpass", false, "sales"); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}

	// パスワードリセット：新パスワードが通り、旧パスワードは通らない
	if err := SetPassword("alice", "newpass"); err != nil {
		t.Fatalf("SetPasswordエラー: %v", err)
	}
	if _, err := Authenticate("alice", "newpass"); err != nil {
		t.Errorf("新パスワードで認証できません: %v", err)
	}
	if _, err := Authenticate("alice", "oldpass"); err != ErrAuthFailed {
		t.Errorf("旧パスワードがまだ通ります: %v", err)
	}

	// 無効化：無効なユーザーは認証できない
	if err := SetDisabled("alice", true); err != nil {
		t.Fatalf("SetDisabledエラー: %v", err)
	}
	if _, err := Authenticate("alice", "newpass"); err != ErrAuthFailed {
		t.Errorf("無効化したユーザーが認証できてしまいます: %v", err)
	}

	// 存在しないユーザーへの操作は ErrNotFound
	if err := SetPassword("ghost", "x"); err != ErrNotFound {
		t.Errorf("ErrNotFoundを期待: %v", err)
	}

	users, err := ListUsers()
	if err != nil || len(users) != 1 {
		t.Errorf("ListUsers: len=%d err=%v", len(users), err)
	}
}

func TestGroupManagement(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("bob", "p", false, ""); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}

	// メンバー追加（グループは自動作成される）
	if err := AddGroupMember("bob", "sales"); err != nil {
		t.Fatalf("AddGroupMemberエラー: %v", err)
	}
	u, err := Authenticate("bob", "p")
	if err != nil {
		t.Fatalf("認証エラー: %v", err)
	}
	found := false
	for _, g := range u.Groups {
		if g == "sales" {
			found = true
		}
	}
	if !found {
		t.Errorf("認証結果に所属グループ sales が含まれません: %+v", u.Groups)
	}

	groups, _ := ListGroups()
	if len(groups) != 1 || groups[0] != "sales" {
		t.Errorf("ListGroupsが不正: %v", groups)
	}

	// メンバー削除
	if err := RemoveGroupMember("bob", "sales"); err != nil {
		t.Fatalf("RemoveGroupMemberエラー: %v", err)
	}
	if g := GroupsFor("bob"); len(g) != 0 {
		t.Errorf("削除後も所属が残っています: %v", g)
	}
}

func TestAudit(t *testing.T) {
	setupAuthDB(t)
	Audit("alice", "save", "000001")
	Audit("root", "chmod", "000001")

	entries, err := RecentAudit(10)
	if err != nil {
		t.Fatalf("RecentAuditエラー: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("監査ログ件数が合いません: %d", len(entries))
	}
	// 新しい順
	if entries[0].Action != "chmod" || entries[1].Action != "save" {
		t.Errorf("監査ログの順序/内容が不正: %+v", entries)
	}
}
