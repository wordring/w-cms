package page

import (
	"os"
	"testing"
)

// Windows が別の名前で開く名前を配らない（.json の検査を抜けて属性と目録が配られていた・2026-10-22 の見直し）。
func TestAliasedFileName(t *testing.T) {
	for _, n := range []string{"000001.meta.json.", "meta.json::$DATA", "a.pdf ", "x:y.pdf"} {
		if !aliasedFileName(n) {
			t.Errorf("⚠ %q を配ろうとします", n)
		}
	}
	for _, n := range []string{"図面.pdf", "a.b.c.dxf"} {
		if aliasedFileName(n) {
			t.Errorf("ふつうの名前 %q を断りました", n)
		}
	}
}

// TestUpdateSidecarDoesNotResurrect は、ごみ箱へ移ったページの属性を書き替えようとしても、ページのフォルダを作り直さない
// （幽霊のページを作らない）ことを固定します（2026-10-26）。新しく作るとき（WriteSidecar）はフォルダを作る。
func TestUpdateSidecarDoesNotResurrect(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := WriteSidecar("000077", PageMeta{Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(GetPageDir("000077")); err != nil { // ごみ箱へ移った
		t.Fatal(err)
	}
	if _, err := SetSidecarParent("000077", "000001"); err == nil {
		t.Error("⚠ 消えたページの親を書き替えられました")
	}
	// 読んだあとにフォルダが消えた（ごみ箱へ移った）ときの書き戻し。
	if err := rewriteSidecar("000077", PageMeta{Owner: "alice", SortKey: "x"}); err != ErrPageGone {
		t.Errorf("⚠ 消えたページの属性を書き戻しました: %v", err)
	}
	if _, err := os.Stat(GetPageDir("000077")); !os.IsNotExist(err) {
		t.Errorf("⚠ 消えたページのフォルダを作り直しました: %v", err)
	}
}
