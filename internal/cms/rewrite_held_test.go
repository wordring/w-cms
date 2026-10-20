package cms

import (
	"errors"
	"strings"
	"testing"

	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// RewriteBodyUnlessHeld（append_page.go・2026-10-20）——道具が編集権を握っていれば書かずに *HeldError（握っている人つき）、
// 放せば書く。拡張の9か所が写していた「EditorOpen を見てから RewriteBody」を寄せた口の番人です。
func TestRewriteBodyUnlessHeld(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000054", `<h1>握る</h1><p>一</p>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	editlock.Locks.ForceRelease(54)
	if res := editlock.Locks.TryAcquire(54, "bob", ""); !res.Acquired {
		t.Fatalf("下ごしらえの編集権が取れません: %+v", res)
	}
	defer editlock.Locks.ForceRelease(54)

	add := func(cur string) string { return cur + "<p>二</p>" }
	err := RewriteBodyUnlessHeld("000054", "機械", add)
	var held *HeldError
	if !errors.As(err, &held) || held.Holder != "bob" {
		t.Fatalf("握られているのに断りません（または握っている人が違う）: %v", err)
	}
	if body, _ := ReadPageBody("000054"); strings.Contains(body, "二") {
		t.Errorf("⚠ 握られているのに書いています:\n%s", body)
	}

	editlock.Locks.ForceRelease(54)
	if err := RewriteBodyUnlessHeld("000054", "機械", add); err != nil {
		t.Fatalf("放したあとに書けません: %v", err)
	}
	if body, _ := ReadPageBody("000054"); !strings.Contains(body, "二") {
		t.Errorf("放したあとに書いていません:\n%s", body)
	}
}
