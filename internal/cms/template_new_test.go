package cms

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ── ページテンプレート 第2段: コピー ────────────────────────────────────
// 正本は docs/【考察】ページテンプレート.md §4・§5。⚠ 2026-09-25 から純粋なコピー
// （新規化パスは撤去）。

// templateOrderBody は空欄（発注書番号・発注日）を持つテンプレート本文です。
// 「書いてある値（発注元）はそのまま・空欄は型の既定値で埋まる」を確かめます。
const templateOrderBody = `<h1>受注ページ</h1>` +
	`<section data-type="client-order">` +
	`<dl data-type="tags"><dt>発注書番号</dt><dd><br></dd>` +
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

// TestCopyTemplateKeepsValuesAndBlanks は、テンプレートが**純粋なコピー**であることを
// 固定します（2026-09-25 ユーザー:「純粋なコピーにしましょう」）。
//
// ⚠ **書いてある値はそのまま・空欄は空欄のまま**。2026-09-25 までは空の日付の列に
// 今日を入れていました（新規化）が、**空欄は「まだ分からない」**（09-21 の決定）で、
// 作った日を受領日や発注日と決めつけません。番号も入れません（09-21 に撤去済み）。
func TestCopyTemplateKeepsValuesAndBlanks(t *testing.T) {
	got := CopyTemplateBody(templateOrderBody)

	if !strings.Contains(got, "<dd>得意先A</dd>") {
		t.Errorf("書いてある値が失われています:\n%s", got)
	}
	if strings.Contains(got, time.Now().Format("2006-01-02")) {
		t.Errorf("⚠ 空の日付の列に今日を入れています（純粋なコピーのはず）:\n%s", got)
	}
	if regexp.MustCompile(`PO-[0-9-]+`).MatchString(got) {
		t.Errorf("⚠ 機械が発注書番号を入れています（お客様の番号です）:\n%s", got)
	}
	if !strings.Contains(got, "<dt>発注日</dt><dd></dd>") {
		t.Errorf("空欄が空欄のまま写っていません:\n%s", got)
	}
	if !strings.Contains(got, `<table data-type="client-order-items">`) {
		t.Errorf("ブロックID以外の属性まで落としています:\n%s", got)
	}
}

// TestCopyTemplateDropsBlockIDs は、⚠ **ブロックID（`data-id`）だけを外す**ことを
// 固定します。
//
// エディタは保存のたびにブロックIDを振るので、テンプレートのページも持っています
// （職場のテンプレートにも付いていた）。写したページが同じIDを持つと、ページどうしで
// ブロックを運ぶとき（改訂の合流）に衝突します。
// ⚠ **入れ子も外すこと**——表の**行**にもIDが振られます（社内コードの後半）。
func TestCopyTemplateDropsBlockIDs(t *testing.T) {
	body := `<h1 data-id="ab12">加工製品</h1>` +
		`<section data-id="cd34"><h2>材料</h2>` +
		`<table data-id="z184" data-type="part-materials"><tbody>` +
		`<tr><th>材質</th></tr><tr data-id="ef56"><td>鉄</td></tr></tbody></table>` +
		`<section data-type="file-view" data-ref="000001-gh78"></section></section>`
	got := CopyTemplateBody(body)

	if strings.Contains(got, "data-id") {
		t.Errorf("⚠ ブロックIDが残っています:\n%s", got)
	}
	for _, keep := range []string{`data-type="part-materials"`, `data-ref="000001-gh78"`,
		"<h2>材料</h2>", "<td>鉄</td>"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s まで落としています:\n%s", keep, got)
		}
	}
}

// TestNewPageFromTemplate は、テンプレートを指定した新規作成が本文を写すことを
// 検証します（エンドツーエンド・2026-09-25 から純粋なコピー）。
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
	// ⚠ **発注書番号は埋めません**（同上・お客様の番号）。
	if strings.Contains(body, "PO-"+newID) {
		t.Errorf("⚠ 機械が発注書番号を入れています（お客様の番号です）:\n%s", body)
	}
	if !strings.Contains(body, "得意先A") {
		t.Errorf("テンプレートに書かれた値が失われています:\n%s", body)
	}
	// ⚠ **空欄は空欄のまま**（新規化をやめた）——口を通しても今日が入らないこと。
	if strings.Contains(body, time.Now().Format("2006-01-02")) {
		t.Errorf("⚠ 空の日付の列に今日を入れています:\n%s", body)
	}

	// コピー先はテンプレート領域の外なので、③計算テーブルへ載る。
	if n := countOrders(t); n != 1 {
		t.Errorf("コピー先が索引に載っていません: 受注ヘッダ %d 件", n)
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
