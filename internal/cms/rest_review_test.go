package cms

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 2026-10-30 の見直し（【考察】コードの矛盾 §13）で直したものの見張りの試験です。

const reviewBox = "置き場の見直しの試験"

func withReviewBox(t *testing.T) {
	t.Helper()
	RegisterRequiredPage(RequiredPage{Title: reviewBox, Extension: "test", Why: "試験用"})
	t.Cleanup(func() { delete(requiredPageRegistry, reviewBox) })
}

// TestSetParentRefusesDuplicateBoxTitle は、置き場の題のページをトップ直下へ動かしても2枚にならないことを固定します。
func TestSetParentRefusesDuplicateBoxTitle(t *testing.T) {
	setupSaveTest(t)
	withReviewBox(t)
	admin := &auth.User{Username: "root", IsAdmin: true}
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "root", Mode: "330"})
	newPage(t, "000100", "<h1>"+reviewBox+"</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: TopPageID})
	newPage(t, "000101", "<h1>下のページ</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: TopPageID})
	newPage(t, "000102", "<h1>"+reviewBox+"</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: "000101"})
	_, _, err := SetPageParent(admin, "000102", TopPageID)
	if err == nil || !strings.Contains(err.Error(), "1枚だけ") {
		t.Fatalf("断るはず: %v", err)
	}
	if m, _ := page.ReadSidecar("000102"); m.ParentID != "000101" {
		t.Errorf("⚠ 動いています: %q", m.ParentID)
	}
}

// TestNewPageRefusesDuplicateBoxTitle は、置き場の題のテンプレートからトップ直下に作っても2枚にならないことを固定します。
func TestNewPageRefusesDuplicateBoxTitle(t *testing.T) {
	setupSaveTest(t)
	withReviewBox(t)
	admin := &auth.User{Username: "root", IsAdmin: true}
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "root", Mode: "330"})
	newPage(t, "000100", "<h1>"+reviewBox+"</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: TopPageID})
	root := newPageID(t, TopPageID, "テンプレート")
	tpl := newPageID(t, root, reviewBox)
	before := reviewPageCount(t)
	rr := newPageForm(t, admin, neturl.Values{"parent": {TopPageID}, "template": {tpl}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("断るはず: %d %s", rr.Code, rr.Body.String())
	}
	if got := reviewPageCount(t); got != before {
		t.Errorf("⚠ 行が増えています: %d → %d", before, got)
	}
}

func newPageID(t *testing.T, parent, title string) string {
	t.Helper()
	id, err := CreateChildPage(parent, "root", "<h1>"+title+"</h1>")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func reviewPageCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTopPageCannotMove は、トップページをほかのページの下へ動かせないことを固定します。
func TestTopPageCannotMove(t *testing.T) {
	setupSaveTest(t)
	admin := &auth.User{Username: "root", IsAdmin: true}
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "root", Mode: "330"})
	newPage(t, "000103", "<h1>孤児</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: "999999"})
	if msg, code := validateParentChange(admin, 0, "000103"); code == 0 {
		t.Errorf("⚠ 動かせます: %q", msg)
	}
}

// TestResyncSubtreeEndsOnCycle は、木が輪になっていても resyncSubtree が終わることを固定します。
func TestResyncSubtreeEndsOnCycle(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000110", "<h1>A</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: "000111"})
	newPage(t, "000111", "<h1>B</h1>", page.PageMeta{Owner: "root", Mode: "330", ParentID: "000110"})
	done := make(chan struct{})
	go func() { resyncSubtree("000110"); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("⚠ 終わりません")
	}
}

// TestChildInheritsPermsFromSidecar は、親の行が索引に無くても、子が正本の権限を継ぐことを固定します。
func TestChildInheritsPermsFromSidecar(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000120", "<h1>親</h1>", page.PageMeta{Owner: "alice", Group: "通信", Mode: "332"})
	if _, err := database.DB.Exec(`DELETE FROM page_perms WHERE page_id = 120`); err != nil {
		t.Fatal(err)
	}
	id, err := CreateChildPage("000120", "alice", "<h1>子</h1>")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := page.ReadSidecar(id)
	if m.Group != "通信" || m.Mode != "332" {
		t.Errorf("⚠ 既定へ倒れました: group=%q mode=%q", m.Group, m.Mode)
	}
}

// TestPurgeFailureIsRetried は、索引から消せなかったページが、あとで消えることを固定します。
func TestPurgeFailureIsRetried(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000130", "<h1>消す</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	oldDelay := indexRetryDelay
	indexRetryDelay = 50 * time.Millisecond
	defer func() { indexRetryDelay = oldDelay }()

	if err := os.Remove(page.BodyPath("000130")); err != nil {
		t.Fatal(err)
	}
	good := database.DB
	bad, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	bad.Close()
	database.DB = bad
	err = PurgePageIndex("000130")
	database.DB = good
	if err == nil {
		t.Fatal("閉じた DB で消せています")
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if titleInIndex(130) == "" {
			return
		}
	}
	t.Errorf("⚠ 索引に残っています: %q", titleInIndex(130))
}

// TestFloorSurvivesEmptiedTrash は、ごみ箱を片付けて起こし直しても、消した番号を配らないことを固定します。
func TestFloorSurvivesEmptiedTrash(t *testing.T) {
	setupSaveTest(t)
	reset := func() {
		trashFloor.Lock()
		trashFloor.loaded, trashFloor.n = false, 0
		trashFloor.Unlock()
	}
	reset()
	defer reset()
	noteTrashed("000777")
	if err := os.RemoveAll(page.TrashDir()); err != nil {
		t.Fatal(err)
	}
	reset() // 起こし直し
	if got := trashHighWater(); got != 777 {
		t.Errorf("⚠ 上限を忘れました: %d", got)
	}
}

// TestRebuildRunsOneAtATime は、DB の作り直しの二度押しを断ることを固定します。
func TestRebuildRunsOneAtATime(t *testing.T) {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	if err := RebuildDatabase(); err == nil || !strings.Contains(err.Error(), "作り直しています") {
		t.Errorf("断るはず: %v", err)
	}
}

// TestPublicMoveNeedsOwner は、公開の印のあるページを公開の親の下へ動かせるのが持ち主か管理者だけであることを固定します。
func TestPublicMoveNeedsOwner(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "333", Public: true})
	newPage(t, "000140", "<h1>公開の親</h1>", page.PageMeta{Owner: "alice", Mode: "333", Public: true, ParentID: TopPageID})
	newPage(t, "000141", "<h1>閉じた親</h1>", page.PageMeta{Owner: "alice", Mode: "333", ParentID: TopPageID})
	newPage(t, "000142", "<h1>印のあるページ</h1>", page.PageMeta{Owner: "alice", Mode: "333", Public: true, ParentID: "000141"})
	bob := &auth.User{Username: "bob"}
	if _, code := validateParentChange(bob, 142, "000140"); code != http.StatusForbidden {
		t.Errorf("⚠ 持ち主でない人が公開できます: %d", code)
	}
	if msg, code := validateParentChange(&auth.User{Username: "alice"}, 142, "000140"); code != 0 {
		t.Errorf("持ち主は動かせるはず: %s", msg)
	}
}

// TestFillTableKeepsFoot は、テンプレートの表を埋めても足元の行が残り、データの行が thead に入らないことを固定します。
func TestFillTableKeepsFoot(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<table><caption>表</caption><thead><tr><th>品</th></tr></thead><tbody><tr><td>見本</td></tr></tbody><tfoot><tr><td>合計</td></tr></tfoot></table>`))
	if err != nil {
		t.Fatal(err)
	}
	b := DraftBlock{n: root, d: &PageDraft{}}
	if _, err := b.FillTable("表", []string{"品"}, [][]string{{"甲"}, {"乙"}}); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	html.Render(&sb, root)
	got := sb.String()
	if !strings.Contains(got, "<tfoot><tr><td>合計</td></tr></tfoot>") {
		t.Errorf("⚠ 足元の行が消えました: %s", got)
	}
	if !strings.Contains(got, "</thead><tbody><tr><td>甲</td></tr><tr><td>乙</td></tr></tbody>") || strings.Contains(got, "見本") {
		t.Errorf("⚠ データの行の場所: %s", got)
	}
}

// TestPageLinksForms は、住所つき・古い添付の住所・末尾の / も「指している」と数えることを固定します。
func TestPageLinksForms(t *testing.T) {
	for _, u := range []string{"/001234", "/001234/", "/001234/a.pdf", "/001234#ab12", "https://w-cms.example/001234",
		"/data/master/12/001234/files/a.pdf"} {
		root, _ := html.Parse(strings.NewReader(`<a href="` + u + `">x</a>`))
		got := pageLinksOf(root, 1)
		if len(got) != 1 || got[0].target != 1234 {
			t.Errorf("⚠ %s: %+v", u, got)
		}
	}
	root, _ := html.Parse(strings.NewReader(`<a href="https://w-cms.example/docs/a">x</a><a href="/assets/app.js">y</a>`))
	if got := pageLinksOf(root, 1); len(got) != 0 {
		t.Errorf("⚠ ページでない住所: %+v", got)
	}
}

// TestPublicPageSubscribableWithoutRead は、read の無い公開ページを開ける人が、更新の知らせと権限の表示を受けられることを固定します。
func TestPublicPageSubscribableWithoutRead(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000000", "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "300", Public: true})
	newPage(t, "000150", "<h1>公開</h1>", page.PageMeta{Owner: "alice", Mode: "300", Public: true, ParentID: TopPageID})
	bob := &auth.User{Username: "bob"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 流し始めたらすぐ終わる
	req := auth.WithUser(httptest.NewRequest("GET", "/api/page-events?id=000150", nil).WithContext(ctx), bob)
	rr := httptest.NewRecorder()
	PageEventsAPIHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("⚠ 更新の知らせ: %d %s", rr.Code, rr.Body.String())
	}

	req = auth.WithUser(httptest.NewRequest("GET", "/api/page-perms?id=000150", nil), bob)
	rr = httptest.NewRecorder()
	PagePermsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("⚠ 権限の表示: %d %s", rr.Code, rr.Body.String())
	}
}

// TestLoadIsNotCached は、本文の口がキャッシュさせないことを固定します。
func TestLoadIsNotCached(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000160", "<h1>本文</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	req := auth.WithUser(httptest.NewRequest("GET", "/api/load?id=000160", nil), &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	LoadAPIHandler(rr, req)
	if rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%d Cache-Control=%q", rr.Code, rr.Header().Get("Cache-Control"))
	}
}

// TestSumCellsAreLimited は、全部の行に合計を書いた大きな表でも、計算が止まらないことを固定します。
func TestSumCellsAreLimited(t *testing.T) {
	rows := make([][]string, 20000)
	for i := range rows {
		rows[i] = []string{"=合計(上)"}
	}
	start := time.Now()
	res := EvalTableFormulas([]string{"価格"}, rows)
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("⚠ 遅すぎます: %v", d)
	}
	if !strings.Contains(res[len(res)-1][0].Err, "多すぎ") {
		t.Errorf("⚠ 上限が効いていません: %+v", res[len(res)-1][0])
	}
	// ふつうの大きさの表は計算できる。
	small := make([][]string, 500)
	small[0] = []string{"1"}
	for i := 1; i < len(small); i++ {
		small[i] = []string{"=合計(上)"}
	}
	if got := EvalTableFormulas([]string{"価格"}, small)[499][0]; got.Err != "" {
		t.Errorf("500 行で断りました: %s", got.Err)
	}
}

// TestRenameTagValueOnlyInTags は、タグの値の置き換えが可変タグの組だけに当たることを固定します。
func TestRenameTagValueOnlyInTags(t *testing.T) {
	body := `<dl data-type="tags"><dt>区分</dt><dd>見積もり</dd></dl><dl><dt>区分</dt><dd>見積もり</dd></dl><table><tr><td><dl><dt>区分</dt><dd>見積もり</dd></dl></td></tr></table>`
	got := renameTagValue(body, "区分", "見積もり", "見積")
	want := `<dl data-type="tags"><dt>区分</dt><dd>見積</dd></dl><dl><dt>区分</dt><dd>見積もり</dd></dl><table><tr><td><dl><dt>区分</dt><dd>見積もり</dd></dl></td></tr></table>`
	if got != want {
		t.Errorf("⚠ %s", got)
	}
}
