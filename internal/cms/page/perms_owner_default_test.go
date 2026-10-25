package page

import (
	"testing"

	"w-cms/internal/auth"
)

// TestDefaultOwnerIsNobodyByName は、⚠ **既定の持ち主 "admin" は、名前の同じ管理者でない利用者を持ち主にしない**ことを
// 固定します（2026-10-26。トップページは持ち主 "admin"・mode 302——持ち主になると、書けて公開も切り替えられた）。
func TestDefaultOwnerIsNobodyByName(t *testing.T) {
	top := PageMeta{Owner: DefaultOwner, Mode: "302"}
	demoted := &auth.User{Username: DefaultOwner}
	if top.OwnedBy(demoted) || top.CanWrite(demoted) {
		t.Errorf("管理者でない「%s」がトップページの持ち主・書ける人になりました", DefaultOwner)
	}
	if !top.CanRead(demoted) {
		t.Errorf("ほかの人と同じく読めるはずです")
	}
	if !top.CanWrite(&auth.User{Username: DefaultOwner, IsAdmin: true}) {
		t.Errorf("管理者は書けるはずです")
	}
	mine := PageMeta{Owner: "alice", Mode: "300"}
	if !mine.OwnedBy(&auth.User{Username: "alice"}) || !mine.CanWrite(&auth.User{Username: "alice"}) {
		t.Errorf("ふつうの持ち主が持ち主になりません")
	}
}
