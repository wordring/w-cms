package cms

import (
	"net/http/httptest"
	"os"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// postDelete は削除APIを呼びます（ロックトークン付き）。
func postDelete(t *testing.T, id, token string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/delete-page?id="+id, nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	rr := httptest.NewRecorder()
	DeletePageAPIHandler(rr, req)
	return rr
}

// lockAs はページのロックを取り、そのトークンを返します。
func lockAs(t *testing.T, pageID int, user string) string {
	t.Helper()
	editlock.Locks.ForceRelease(pageID)
	t.Cleanup(func() { editlock.Locks.ForceRelease(pageID) })
	res := editlock.Locks.TryAcquire(pageID, user, "")
	if !res.Acquired {
		t.Fatalf("ロックを取得できません: %+v", res)
	}
	return res.Token
}

// TestDeletePageMovesToTrash は、削除がページを**ゴミ箱へ移す**（物理削除しない）ことと、
// 索引から消えることを検証します。「常に柔軟性」——削除自体も取り消せること
// （docs/考察/【考察】通信記録処理.md §2.7④）。
func TestDeletePageMovesToTrash(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000020", "<h1>消す対象</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	token := lockAs(t, 20, "alice")
	rr := postDelete(t, "000020", token, &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("削除に失敗しました: code=%d body=%s", rr.Code, rr.Body.String())
	}

	if _, err := os.Stat(page.GetPageDir("000020")); !os.IsNotExist(err) {
		t.Errorf("正本のフォルダが残っています: %v", err)
	}
	if _, err := os.Stat(page.GetTrashDir("000020")); err != nil {
		t.Errorf("ゴミ箱へ移っていません: %v", err)
	}

	var n int
	database.DB.QueryRow("SELECT COUNT(*) FROM pages WHERE id = ?", 20).Scan(&n)
	if n != 0 {
		t.Errorf("pages から消えていません: %d 件", n)
	}
	database.DB.QueryRow("SELECT COUNT(*) FROM page_perms WHERE page_id = ?", 20).Scan(&n)
	if n != 0 {
		t.Errorf("page_perms から消えていません: %d 件", n)
	}
}

// TestDeletePageRejectsTopPage はトップページを削除できないことを検証します。
func TestDeletePageRejectsTopPage(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	token := lockAs(t, 0, "alice")
	rr := postDelete(t, "000000", token, &auth.User{Username: "alice"})
	if rr.Code == 200 {
		t.Errorf("トップページが削除できてしまいます: %s", rr.Body.String())
	}
	if _, err := os.Stat(page.GetPageDir("000000")); err != nil {
		t.Errorf("トップページのフォルダが消えています: %v", err)
	}
}

// TestDeletePageRejectsWithChildren は、子ページを持つページを削除できないことを検証します。
// 子を道連れにする（再帰削除）より、先に子を移すか消すよう促すほうが安全側。
func TestDeletePageRejectsWithChildren(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000021", "<h1>親</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000022", "<h1>子</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: "000021"})

	token := lockAs(t, 21, "alice")
	rr := postDelete(t, "000021", token, &auth.User{Username: "alice"})
	if rr.Code != 409 {
		t.Errorf("子ページがあるのに削除できました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(page.GetPageDir("000021")); err != nil {
		t.Errorf("親のフォルダが消えています: %v", err)
	}
}

// TestDeletePageRequiresLock は、他者がロックを保持していると削除できないことを検証します。
func TestDeletePageRequiresLock(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000023", "<h1>編集中</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	lockAs(t, 23, "bob") // 他者が保持
	rr := postDelete(t, "000023", "", &auth.User{Username: "alice"})
	if rr.Code != 409 {
		t.Errorf("他者ロック中に削除できました: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDeletePageNeedsParentWrite は、消すには親にも書けることを固定します（2026-10-27 の見直し——移す口と同じ決まり。
// それまでは、書けない置き場から自分のページをごみ箱へ持ち出せた）。
func TestDeletePageNeedsParentWrite(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000030", "<h1>書けない置き場</h1>", page.PageMeta{Owner: "bob", Mode: "300"})
	newPage(t, "000031", "<h1>自分のページ</h1>", page.PageMeta{Owner: "alice", Mode: "300", ParentID: "000030"})
	alice := &auth.User{Username: "alice"}
	if rr := postDelete(t, "000031", lockAs(t, 31, "alice"), alice); rr.Code != 403 {
		t.Errorf("⚠ 書けない置き場からページを消せました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(page.GetPageDir("000031")); err != nil {
		t.Errorf("断ったのにページが消えています: %v", err)
	}
	bob := &auth.User{Username: "bob", IsAdmin: true}
	if rr := postDelete(t, "000031", lockAs(t, 31, "bob"), bob); rr.Code != 200 {
		t.Errorf("親に書ける人が消せません: code=%d body=%s", rr.Code, rr.Body.String())
	}
}
