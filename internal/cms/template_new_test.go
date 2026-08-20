package cms

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ── ページテンプレート 第2段: コピーと新規化パス ────────────────────────
// 正本は docs/【考察】ページテンプレート.md §4・§5。

// templateOrderBody は空欄（発注書番号・発注日）を持つテンプレート本文です。
// 「書いてある値（発注元）はそのまま・空欄は型の既定値で埋まる」を確かめます。
const templateOrderBody = `<h1>受注ページ</h1>` +
	`<section data-type="client-order">` +
	`<dl><dt>発注書番号</dt><dd><br></dd>` +
	`<dt>発注元</dt><dd>得意先A</dd>` +
	`<dt>発注日</dt><dd></dd></dl>` +
	`<table data-type="client-order-items"><tbody>` +
	`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
	`<tr><td></td><td></td><td></td><td></td><td></td></tr>` +
	`</tbody></table></section>`

// newPageWithTemplate は /api/new-page をテンプレート付きで呼びます。
// POST 限定（保存型CSRF対策。method_guard_test.go 参照）なので、引数はフォームで送ります。
func newPageWithTemplate(t *testing.T, parent, tmpl string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	form := neturl.Values{}
	form.Set("parent", parent)
	if tmpl != "" {
		form.Set("template", tmpl)
	}
	req := httptest.NewRequest("POST", "/api/new-page", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	NewPageAPIHandler(rr, req)
	return rr
}

// bodyOf は保存された正本HTMLを読みます。
func bodyOf(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("正本を読めません page=%s: %v", id, err)
	}
	return string(data)
}

// TestFreshenFillsEmptyCellsOnly は新規化の規則
// 「空欄は列型の既定値で埋める・書いてある値はそのまま保つ」を検証します。
func TestFreshenFillsEmptyCellsOnly(t *testing.T) {
	got := FreshenTemplateBody(templateOrderBody, "000123")
	today := time.Now().Format("2006-01-02")

	if !strings.Contains(got, "<dd>PO-000123</dd>") {
		t.Errorf("発注書番号が採番されていません:\n%s", got)
	}
	if !strings.Contains(got, "<dd>得意先A</dd>") {
		t.Errorf("書いてある値が失われています:\n%s", got)
	}
	if !strings.Contains(got, "<dd>"+today+"</dd>") {
		t.Errorf("発注日が今日で埋まっていません:\n%s", got)
	}
	// text 列は空のまま（人が書く）。品番のセルが勝手に埋まっていないこと。
	if !strings.Contains(got, "<td></td>") {
		t.Errorf("text 列まで埋められています:\n%s", got)
	}
}

// TestFreshenKeepsWrittenOrderNo は、テンプレートに番号が書かれている場合は
// **触らない**ことを検証します（作者が意図して入れた値を消さない）。
func TestFreshenKeepsWrittenOrderNo(t *testing.T) {
	body := `<section data-type="client-order"><dl>` +
		`<dt>発注書番号</dt><dd>PO-FIXED</dd></dl></section>`
	if got := FreshenTemplateBody(body, "000123"); !strings.Contains(got, "PO-FIXED") {
		t.Errorf("書かれた発注書番号が失われています: %s", got)
	}
}

// TestFreshenNumbersMultipleOrderBlocks は、1ページに発注書ブロックが複数あっても
// 番号が衝突しないことを検証します（order_no は UNIQUE 制約を持つ）。
func TestFreshenNumbersMultipleOrderBlocks(t *testing.T) {
	body := `<section data-type="client-order"><dl><dt>発注書番号</dt><dd></dd></dl></section>` +
		`<section data-type="client-order"><dl><dt>発注書番号</dt><dd></dd></dl></section>`
	got := FreshenTemplateBody(body, "000123")
	if !strings.Contains(got, "PO-000123<") || !strings.Contains(got, "PO-000123-2<") {
		t.Errorf("複数ブロックの採番が衝突しています:\n%s", got)
	}
}

// TestNewPageFromTemplate は、テンプレートを指定した新規作成が本文を写して
// 新規化することを検証します（エンドツーエンド）。
func TestNewPageFromTemplate(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)
	newPage(t, "000012", templateOrderBody, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	// コピー先の親（普通のページ）。
	newPage(t, "000020", "<h1>案件</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})

	rr := newPageWithTemplate(t, "000020", "000012", &auth.User{Username: "alice"})
	if rr.Code != 302 {
		t.Fatalf("新規作成に失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	newID := strings.TrimSuffix(strings.TrimPrefix(rr.Header().Get("Location"), "/"), "?edit=true")

	body := bodyOf(t, newID)
	if !strings.Contains(body, "受注ページ") {
		t.Errorf("テンプレートの本文が写っていません:\n%s", body)
	}
	if !strings.Contains(body, "PO-"+newID) {
		t.Errorf("発注書番号が新ページIDで採番されていません:\n%s", body)
	}
	if !strings.Contains(body, "得意先A") {
		t.Errorf("テンプレートに書かれた値が失われています:\n%s", body)
	}

	// コピー先はテンプレート領域の外なので、③計算テーブルへ載る。
	if n := countOrders(t); n != 1 {
		t.Errorf("コピー先が索引に載っていません: client_orders に %d 行", n)
	}
}

// TestNewPageRejectsNonTemplate は、テンプレート領域の外のページや分類フォルダを
// テンプレートとして指定できないことを検証します。
func TestNewPageRejectsNonTemplate(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)
	newPage(t, "000012", templateOrderBody, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	newPage(t, "000020", "<h1>普通のページ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})

	alice := &auth.User{Username: "alice"}
	for _, tc := range []struct{ name, tmpl string }{
		{"領域外の普通のページ", "000020"},
		{"分類フォルダ（子を持つ）", classify},
		{"テンプレートルート自身", "000010"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := newPageWithTemplate(t, "000020", tc.tmpl, alice)
			if rr.Code != 400 {
				t.Errorf("拒否されていません: code=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestNewPageRejectsUnreadableTemplate は、read 権限の無いテンプレートを
// 写せないことを検証します（テンプレートの中身を読む操作なので read が要る）。
func TestNewPageRejectsUnreadableTemplate(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)
	// mode 300 ＝ owner のみ rw、group/other は権限なし。
	newPage(t, "000012", templateOrderBody, page.PageMeta{
		Owner: "alice", Mode: "300", ParentID: classify})
	newPage(t, "000020", "<h1>案件</h1>", page.PageMeta{
		Owner: "bob", Mode: page.DefaultMode, ParentID: TopPageID})

	rr := newPageWithTemplate(t, "000020", "000012", &auth.User{Username: "bob"})
	if rr.Code == 302 {
		t.Fatal("読めないテンプレートが写されました")
	}
}

// TestFailedTemplateLeavesNoOrphanPage は、テンプレート指定が不正なときに
// **ページ行が残らない**ことを検証します（採番より前に検証する順序の固定）。
func TestFailedTemplateLeavesNoOrphanPage(t *testing.T) {
	setupSaveTest(t)
	newTemplateTree(t)
	newPage(t, "000020", "<h1>案件</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})

	var before int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&before)

	rr := newPageWithTemplate(t, "000020", "000020", &auth.User{Username: "alice"})
	if rr.Code != 400 {
		t.Fatalf("拒否されていません: code=%d", rr.Code)
	}

	var after int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&after)
	if after != before {
		t.Errorf("ファイルの無いページ行が残りました: %d → %d", before, after)
	}
}

// ── 第3段: 一覧の読み口 ────────────────────────────────────────────

// setupTemplateAPITest は **ファイルDB** でテスト環境を用意します。
//
// setupSaveTest の ":memory:" が使えないのは visibleChildren が
// 「行を回しながら1件ずつ page.GetPerms を引く」形だからです——入れ子のクエリは
// プールの別接続で走り、":memory:" では接続ごとに**別の空DB**になるため、
// 権限がフェイルクローズして子ページが1件も見えなくなります。
// 本番（data/cms.db）はファイルなので同じ問題は起きません。
func setupTemplateAPITest(t *testing.T) {
	t.Helper()
	origWd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	dsn := filepath.ToSlash(filepath.Join(dir, "t.db")) +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
}

// getTemplates は GET /api/templates を呼びます。
func getTemplates(t *testing.T, u *auth.User) []TemplateNode {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/templates", nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	TemplatesAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("/api/templates が失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []TemplateNode
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("JSONを解釈できません: %v (%s)", err, rr.Body.String())
	}
	return out
}

// TestTemplatesAPIReturnsTree は、テンプレート一覧が階層のまま返ること
// （枝＝分類・葉＝テンプレート）を検証します。
func TestTemplatesAPIReturnsTree(t *testing.T) {
	setupTemplateAPITest(t)
	classify := newTemplateTree(t) // トップ → テンプレート(000010) → 業務(000011)
	newPage(t, "000012", "<h1>受注ページ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})

	tree := getTemplates(t, &auth.User{Username: "alice"})
	if len(tree) != 1 || tree[0].Title != "業務" {
		t.Fatalf("分類が返っていません: %+v", tree)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Title != "受注ページ" {
		t.Fatalf("葉が返っていません: %+v", tree[0].Children)
	}
	if tree[0].Children[0].ID != "000012" {
		t.Errorf("IDが期待と異なります: %q", tree[0].Children[0].ID)
	}
}

// TestTemplatesAPIEmptyWithoutRoot は、テンプレートルートが無ければ空配列を返すことを
// 検証します（従来どおり「空のページ」だけが作られる）。
func TestTemplatesAPIEmptyWithoutRoot(t *testing.T) {
	setupTemplateAPITest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000020", "<h1>普通のページ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})

	if tree := getTemplates(t, &auth.User{Username: "alice"}); len(tree) != 0 {
		t.Errorf("ルートが無いのにテンプレートが返りました: %+v", tree)
	}
}
