package cms

import (
	"errors"
	"testing"

	"w-cms/internal/cms/page"
)

// TestTreeLockKeepsChildrenOffDeletedParents は、消している最中・消したページの下に子ページを作れず、子ページを動かして
// こられないこと・子のあるページは消している印を付けないことを固定します（2026-10-26・tree_lock.go）。
func TestTreeLockKeepsChildrenOffDeletedParents(t *testing.T) {
	newTestFileDB(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "333"})
	newPage(t, "000010", "<h1>消す</h1>", page.PageMeta{Owner: "alice", Mode: "333", ParentID: TopPageID})
	newPage(t, "000011", "<h1>動かす</h1>", page.PageMeta{Owner: "alice", Mode: "333", ParentID: TopPageID})

	ok, _, done, err := beginDeleteIfChildless(10)
	if err != nil || !ok {
		t.Fatalf("子の無いページに消している印を付けられません: %v %v", ok, err)
	}
	if _, err := CreateChildPage("000010", "alice", "<h1>子</h1>"); !errors.Is(err, ErrParentGone) {
		t.Errorf("⚠ 消している最中のページの下に子を作れました: %v", err)
	}
	if _, _, err := SetPageParent(admin, "000011", "000010"); !errors.Is(err, ErrParentGone) {
		t.Errorf("⚠ 消している最中のページの下へ動かせました: %v", err)
	}
	done()
	// 印を外したあとは作れる。作った子があると、消している印は付かない。
	if _, err := CreateChildPage("000010", "alice", "<h1>子</h1>"); err != nil {
		t.Fatalf("子を作れません: %v", err)
	}
	if ok, n, _, err := beginDeleteIfChildless(10); err != nil || ok || n != 1 {
		t.Errorf("⚠ 子のあるページに消している印を付けました: ok=%v 子=%d %v", ok, n, err)
	}
	// 無いページの下には作れない。
	if _, err := CreateChildPage("000099", "alice", "<h1>子</h1>"); !errors.Is(err, ErrParentGone) {
		t.Errorf("⚠ 無いページの下に子を作れました: %v", err)
	}
}
