package cms

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// setParent は POST /api/set-parent を利用者 u で呼びます（編集ロックを取り直してから）。
func setParent(t *testing.T, id, parent string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	idInt, _ := strconv.Atoi(id)
	editlock.Locks.ForceRelease(idInt)
	t.Cleanup(func() { editlock.Locks.ForceRelease(idInt) })
	a := editlock.Locks.TryAcquire(idInt, u.Username, "")
	if !a.Acquired {
		t.Fatalf("%s のロック取得に失敗", u.Username)
	}
	req := httptest.NewRequest("POST", "/api/set-parent?id="+id+"&parent="+parent, nil)
	req.Header.Set("X-Lock-Token", a.Token)
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	SetParentAPIHandler(rr, req)
	return rr
}

// indexRowsOf はページの汎用索引の行数を返します。
func indexRowsOf(t *testing.T, id string) int {
	t.Helper()
	idInt, _ := strconv.Atoi(id)
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM vocab_index WHERE page_id = ?`, idInt).Scan(&n); err != nil {
		t.Fatalf("索引のクエリでエラー: %v", err)
	}
	return n
}

// TestSetParentResyncsSubtreeIndex は、親の付け替えでテンプレート領域へ出入りした
// ページ（と**その配下**）の索引が、次の保存を待たずに同期し直されることを検証します。
// これが無いと、テンプレートへ移した仮の発注書が③計算に残り続ける（逆も然り）。
func TestSetParentResyncsSubtreeIndex(t *testing.T) {
	setupTemplateAPITest(t)
	classify := newTemplateTree(t) // トップ → テンプレート(000010) → 業務(000011)
	alice := &auth.User{Username: "alice"}

	newPage(t, "000020", clientOrderBody("受注A", "PO-A"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	newPage(t, "000021", clientOrderBody("受注Aの子", "PO-A2"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000020"})
	if indexRowsOf(t, "000020") == 0 || indexRowsOf(t, "000021") == 0 {
		t.Fatal("前提: 普通の場所にあるページは索引に載る")
	}

	// テンプレート領域（分類ページの下）へ動かす → 親も子も索引から消える。
	if rr := setParent(t, "000020", classify, alice); rr.Code != 200 {
		t.Fatalf("親の付け替えが失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := indexRowsOf(t, "000020"); n != 0 {
		t.Errorf("テンプレート領域へ移したのに索引に %d 行残っています", n)
	}
	if n := indexRowsOf(t, "000021"); n != 0 {
		t.Errorf("配下のページが索引に %d 行残っています（先祖辿りの再同期が漏れている）", n)
	}

	// 戻す → 親も子も索引に戻る。
	if rr := setParent(t, "000020", TopPageID, alice); rr.Code != 200 {
		t.Fatalf("戻しが失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if indexRowsOf(t, "000020") == 0 || indexRowsOf(t, "000021") == 0 {
		t.Error("テンプレート領域から戻したのに索引に載りません")
	}
}
