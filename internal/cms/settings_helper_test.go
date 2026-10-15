package cms

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 添付の上書きの試験の下ごしらえ（2026-10-15 に webdav_test.go から移した——WebDAV はやめた）。

// attachPage はページを1枚作り、attach が空でなければその名前の添付を1つ置きます。
func attachPage(t *testing.T, id, title, owner, mode, parent, attach string) {
	t.Helper()
	newPage(t, id, "<h1>"+title+"</h1>", page.PageMeta{
		Owner: owner, Mode: mode, ParentID: parent})
	if attach == "" {
		return
	}
	dir := page.AttachmentDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("添付フォルダを作れません: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, attach), []byte("0\nSECTION\n"), 0o644); err != nil {
		t.Fatalf("添付を作れません: %v", err)
	}
}

// setupAttachmentTest は木を1つ組みます。
//
//	トップ
//	├ 部品A       （添付 a1b2.dxf）
//	├ RE: 見積り
//	├ 秘密        （alice だけ・bob からは見えない）
//	└ 読むだけ    （添付 c3d4.dxf・bob は読めるが書けない）
//
// **ファイルDBを使います**——`visibleChildren` は `:memory:` では動きません
// （行を読みながら別のクエリを投げると別の空DBに当たる。引き継ぎの罠）。
func setupAttachmentTest(t *testing.T) {
	t.Helper()
	setupTemplateAPITest(t)
	restoreSettings(t)

	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "333"})
	attachPage(t, "000101", "部品A", "alice", "333", TopPageID, "a1b2.dxf")
	attachPage(t, "000102", "RE: 見積り", "alice", "333", TopPageID, "")
	attachPage(t, "000103", "秘密", "alice", "300", TopPageID, "")
	attachPage(t, "000104", "読むだけ", "alice", "332", TopPageID, "c3d4.dxf")

	if err := database.InitAuthDB(); err != nil {
		t.Fatalf("認証DBを用意できません: %v", err)
	}
	t.Cleanup(func() { database.AuthDB.Close() })
	for _, u := range []string{"alice", "bob"} {
		if err := auth.CreateUser(u, "pw", false, ""); err != nil {
			t.Fatalf("利用者を作れません: %v", err)
		}
	}
}

// withSettings は設定を**写しごと差し替えて**変更を当てます。
//
// **中身を直に書き換えてはいけません**——`settings` は共有され、`restoreSettings` が
// 戻すのは**ポインタだけ**なので、フィールドを直接いじると後続のテストへ漏れます
// （実際に漏らして、単独では通るのに並べると落ちるテストを作りました）。
// settings.go 自身が「差し替えは常にポインタごと」と書いている規律そのものです。
func withSettings(t *testing.T, apply func(*Settings)) {
	t.Helper()
	settingsMu.Lock()
	cp := *settings
	apply(&cp)
	settings = &cp
	settingsMu.Unlock()
}
