package cms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// サイドカー（属性の正本）が読めなくなったときの方針は
// **「自動で治さない。運用者へ知らせて止める」**です（2026-08-21 決定。正本は
// アーキテクチャとDBスキーマ.md の決定ログ「派生（DB）から正本（ファイル）へ書き戻さない」）。
//
// 派生（cms.db）から組み立て直して書き戻すと、正本の欠損が派生の内容で上書きされ
// 「見た目は健全なのに中身が変わっている」状態を作ります。**壊れたときは壊れたまま**に
// しておくほうが気づけます。
//
// 2026-08-21 の `bd7ee98` は、この経路で「親ページIDを失う」バグを塞ぐために
// 索引から親を拾い直して書き戻す形にしていました。欠損の緩和としては働きましたが
// 方針としては採らないため、期待ごと反転させています。
//
// なお、派生の側が自分の持つ値を保つこと（SyncIndex が parent_id を消さない）は
// 正本への書き戻しではないので、そのまま維持します。

// TestSaveFailsWhenSidecarUnreadable は、サイドカーが読めないとき保存が
// **失敗する**ことと、**壊れたサイドカーが上書きされない**ことを固定します。
func TestSaveFailsWhenSidecarUnreadable(t *testing.T) {
	const id = "000021"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"})

	if err := SyncIndex(id, "<h1>子ページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// サイドカーが壊れる（切り詰め）。ReadSidecar は失敗する。
	sidecar := filepath.Join(page.GetPageDir(id), id+".meta.json")
	if err := os.WriteFile(sidecar, []byte("{"), 0644); err != nil {
		t.Fatalf("サイドカー破損の再現に失敗: %v", err)
	}

	// 保存の起点。**エラーになること**が期待。
	_, err := page.BumpUpdatedAt(id)
	if err == nil {
		t.Fatal("サイドカーが読めないのに保存が通りました（自動で治してはいけない）")
	}
	// 運用者が何をすればよいか分かるメッセージであること
	if !strings.Contains(err.Error(), sidecar) {
		t.Errorf("エラーに修復対象のパスが含まれていません: %v", err)
	}

	// 壊れたファイルはそのまま残っていること（新造して欠損を隠さない）
	got, readErr := os.ReadFile(sidecar)
	if readErr != nil {
		t.Fatalf("サイドカーを読めません: %v", readErr)
	}
	if string(got) != "{" {
		t.Errorf("壊れたサイドカーが書き換えられました: %q", string(got))
	}

	// 親の付け替えも同じく止まること
	if _, err := page.SetSidecarParent(id, "000000"); err == nil {
		t.Error("サイドカーが読めないのに親の付け替えが通りました")
	}
}

// TestSyncKeepsParentWhenSidecarUnreadable は、**派生の側が自分の持つ親を
// 消さない**ことを固定します。これは正本への書き戻しではないので維持します
// （サイドカーが読めない＝親が「無い」のではなく「分からない」）。
func TestSyncKeepsParentWhenSidecarUnreadable(t *testing.T) {
	const id = "000021"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"})
	if err := SyncIndex(id, "<h1>子ページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if got := dbParent(t, 21); got != "000000" {
		t.Fatalf("前提が崩れています: parent_id=%q", got)
	}

	sidecar := filepath.Join(page.GetPageDir(id), id+".meta.json")
	if err := os.WriteFile(sidecar, []byte("{"), 0644); err != nil {
		t.Fatalf("サイドカー破損の再現に失敗: %v", err)
	}

	// 再同期しても索引の親は残ること（ページがツリーから消えない）
	if err := SyncIndex(id, "<h1>子ページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if got := dbParent(t, 21); got != "000000" {
		t.Errorf("サイドカーが読めないだけで索引の親が消えました: parent_id=%q", got)
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
