package cms

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ページの削除と保存の行き違い（2026-10-19・【考察】文書の食い違い §3.4）。
// 書ける形のタブが保存している最中にページを消すと、消したはずのページが残っていた（家の試験で、索引だけの幽霊のページと、
// ごみ箱へ移らないページができた）。削除は書き込みの錠の中で移し、保存は消されたページへ書かない。

// TestDeleteWaitsForWriteLock は、保存が書き込みの錠を持っているあいだ、削除がフォルダを動かさずに待つことを固定します。
func TestDeleteWaitsForWriteLock(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000021", "<h1>保存の最中</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	token := lockAs(t, 21, "alice")

	unlock := LockPageBody("000021") // 保存が読んでから書くまでのあいだ、と見立てる
	done := make(chan int, 1)
	go func() { done <- postDelete(t, "000021", token, &auth.User{Username: "alice"}).Code }()
	select {
	case code := <-done:
		unlock()
		t.Fatalf("⚠ 書き込みの錠を持っているのに、削除が終わりました（%d）", code)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(page.GetPageDir("000021")); err != nil {
		unlock()
		t.Fatalf("⚠ 錠を持っているあいだに、フォルダが動きました: %v", err)
	}
	unlock()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("錠を外したあとの削除が %d です", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("⚠ 錠を外しても削除が終わりません")
	}
	if _, err := os.Stat(page.GetTrashDir("000021")); err != nil {
		t.Errorf("ゴミ箱へ移っていません: %v", err)
	}
}

// TestWriteBodyRefusesDeletedPage は、ごみ箱へ移したページへ保存が来ても、フォルダを作り直さずに断ることを固定します
// （それまでは MkdirAll で作り直していた——本文だけのページが生き返った）。
func TestWriteBodyRefusesDeletedPage(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000022", "<h1>消すページ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	if _, err := moveToTrash("000022"); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	if _, ok := writeBody(rr, "000022", "<h1>あとから来た保存</h1>"); ok || rr.Code != 404 {
		t.Errorf("⚠ 消したページへ書けてしまいます: ok=%v code=%d", ok, rr.Code)
	}
	if _, err := os.Stat(page.GetPageDir("000022")); !os.IsNotExist(err) {
		t.Errorf("⚠ 消したページのフォルダが作り直されました: %v", err)
	}
}
