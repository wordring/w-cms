package cms

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// サイドカー（属性の正本）が読めなくなったとき、派生索引の側がその欠損を
// 「親なし・作成情報なし」として確定させてしまう経路を塞ぎます。
//
// 起きること: readMetaOrPerms はサイドカーが読めないと DB の page_perms へ落ちるが、
// GetPerms が返すのは owner/group/mode/public だけなので ParentID・CreatedAt・CreatedBy は
// 空のまま。BumpUpdatedAt がそれを WriteSidecar すると、**見た目は健全なのに親を失った
// サイドカーが新造**され、続く SyncIndex が pages.parent_id を NULL にする。
// 結果、ページがツリーから消える（親から見た子ページ一覧が空になる）。
//
// サイドカーは os.WriteFile（O_TRUNC）で書かれるので、この経路自身が前提条件
// （書き込み中の切り詰め破損）を作れます。

// TestSyncKeepsParentWhenSidecarUnreadable は、サイドカーが読めないときに
// DB 側の親を消さないことを固定します。
func TestSyncKeepsParentWhenSidecarUnreadable(t *testing.T) {
	const id = "000021"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"})

	// 正常な状態を作る（親が DB に載る）
	if err := SyncIndex(id, "<h1>子ページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if got := dbParent(t, 21); got != "000000" {
		t.Fatalf("前提が崩れています: parent_id=%q", got)
	}

	// サイドカーが壊れる（切り詰め）。ReadSidecar は失敗する。
	sidecar := filepath.Join(page.GetPageDir(id), id+".meta.json")
	if err := os.WriteFile(sidecar, []byte("{"), 0644); err != nil {
		t.Fatalf("サイドカー破損の再現に失敗: %v", err)
	}
	if _, ok := page.ReadSidecar(id); ok {
		t.Fatal("前提が崩れています: 壊したサイドカーが読めています")
	}

	// この状態で1回保存する（本文保存の経路と同じ順序）
	if _, err := page.BumpUpdatedAt(id); err != nil {
		t.Fatalf("BumpUpdatedAtエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>子ページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if got := dbParent(t, 21); got != "000000" {
		t.Errorf("サイドカーが読めないだけで親が失われました: parent_id=%q", got)
	}
	// 新造されたサイドカーも親を持っていること（欠損を隠した健全な見た目にしない）
	meta, ok := page.ReadSidecar(id)
	if !ok {
		t.Fatal("サイドカーが再作成されていません")
	}
	if meta.ParentID != "000000" {
		t.Errorf("再作成されたサイドカーが親を失っています: %+v", meta)
	}
	if meta.Owner != "alice" {
		t.Errorf("再作成されたサイドカーが所有者を失っています: %+v", meta)
	}
}

// TestSyncStillAllowsTopLevel は、サイドカーが**読めたうえで**親が空なら
// トップレベルとして扱われることを固定します（親の保護が付け替えを潰さないこと）。
func TestSyncStillAllowsTopLevel(t *testing.T) {
	const id = "000022"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"})
	if err := SyncIndex(id, "<h1>子</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if got := dbParent(t, 22); got != "000000" {
		t.Fatalf("前提が崩れています: parent_id=%q", got)
	}

	// admin がトップレベルへ付け替える（サイドカーの親を空にする）
	if _, err := page.SetSidecarParent(id, ""); err != nil {
		t.Fatalf("SetSidecarParentエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>子</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if got := dbParent(t, 22); got != "" {
		t.Errorf("トップレベルへの付け替えが元に戻されました: parent_id=%q", got)
	}
}

// dbParent は pages.parent_id を6桁文字列で返します（NULL は空文字）。
func dbParent(t *testing.T, idInt int) string {
	t.Helper()
	var parent *int64
	if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", idInt).Scan(&parent); err != nil {
		t.Fatalf("parent_id を読めません: %v", err)
	}
	if parent == nil {
		return ""
	}
	return fmt.Sprintf("%0*d", page.IDLength, *parent)
}
