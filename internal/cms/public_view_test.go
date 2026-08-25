package cms

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 公開専用ビューのテスト（要件定義書 §4.4・認証認可設計 §10.5）。
//
// 匿名の訪問者には**編集用クロームを含まない体裁**で届き、SEO/SNS共有のメタ情報が
// 付き、キャッシュ可能であること。逆に**認証済みの応答と非公開ページは絶対に
// キャッシュさせない**こと——ここが崩れると、共有キャッシュが他人の応答を配ります。

// writeTestPublicShell は公開専用の殻を作業ディレクトリへ用意します。
// 本物と同じプレースホルダを持たせ、合成のロジックだけを検証します。
func writeTestPublicShell(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll("assets", 0755); err != nil {
		t.Fatalf("assets作成エラー: %v", err)
	}
	shell := `<!DOCTYPE html><html lang="ja"><head><!--WCMS_HEAD--></head>` +
		`<body><main id="w-public-content"><!--WCMS_CONTENT--></main></body></html>`
	if err := os.WriteFile(filepath.Join("assets", "public.html"), []byte(shell), 0644); err != nil {
		t.Fatalf("公開シェル作成エラー: %v", err)
	}
	publicShellCache.Lock()
	publicShellCache.body = ""
	publicShellCache.modTime = 0
	publicShellCache.Unlock()
}

// setupPublicTest はページ本文の殻2種とDBを用意します。
func setupPublicTest(t *testing.T) {
	t.Helper()
	setupSaveTest(t)
	writeTestShell(t)
	writeTestPublicShell(t)
}

// TestPublicViewDropsEditorChrome は、匿名の応答に編集用クロームが**そもそも
// 含まれない**ことを検証します。CSSで隠すのではなく配信しないのが要件です。
func TestPublicViewDropsEditorChrome(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1><p>板金加工を承ります。</p>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	rr := getPage(t, "/000030", nil)
	if rr.Code != 200 {
		t.Fatalf("200ではありません: %d (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	if !strings.Contains(body, "板金加工を承ります。") {
		t.Errorf("本文が届いていません:\n%s", body)
	}
	// テスト用の殻は編集用が id="editor-content"、公開用が id="w-public-content"。
	// どちらの殻が使われたかで体裁の切り替えを判定する。
	if !strings.Contains(body, "w-public-content") {
		t.Errorf("公開専用の殻が使われていません:\n%s", body)
	}
	if strings.Contains(body, "editor-content") {
		t.Errorf("編集用の殻が匿名へ返っています:\n%s", body)
	}
}

// TestPublicShellShipsNoScript は、**実物の**公開シェルがスクリプトを一切
// 読み込まないことを固定します。閲覧はゼロJSで完結する（本文は標準HTML・
// 計算ビューはサーバー事前描画）ので、訪問者へ配る理由がありません。
// 後戻り——公開シェルへ `<script>` を足すこと——をここで検出します。
func TestPublicShellShipsNoScript(t *testing.T) {
	b, err := os.ReadFile("../../assets/public.html")
	if err != nil {
		t.Fatalf("公開シェルを読めません: %v", err)
	}
	if strings.Contains(string(b), "<script") {
		t.Error("公開シェルがスクリプトを読み込んでいます（閲覧はゼロJSで完結する設計）")
	}
	// 殻の id は接頭辞 `w-` を独占する（本文の id と衝突させないため）。
	for _, m := range idAttrRe.FindAllStringSubmatch(string(b), -1) {
		if !strings.HasPrefix(m[1], "w-") {
			t.Errorf("接頭辞の無い id が公開シェルにあります: %q", m[1])
		}
	}
}

// TestAuthenticatedGetsEditorShell は、認証済みには従来どおり編集用の殻を返す
// ことを検証します（同じページでも相手で体裁が変わる）。
func TestAuthenticatedGetsEditorShell(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1><p>本文</p>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	body := getPage(t, "/000030", &auth.User{Username: "alice"}).Body.String()
	if !strings.Contains(body, "editor-content") {
		t.Errorf("認証済みに編集用の殻が返っていません:\n%s", body)
	}
	if strings.Contains(body, "w-public-content") {
		t.Errorf("認証済みに公開専用の殻が返っています:\n%s", body)
	}
}

// TestPublicViewHasSEOTags は SEO/SNS共有のメタ情報を検証します。
// description は本文の最初の段落から作る（人が別途書かなくても成立させる）。
func TestPublicViewHasSEOTags(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1><p>板金加工を承ります。</p>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	req := httptest.NewRequest("GET", "/000030", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	RootHandler(rr, req)
	body := rr.Body.String()

	want := []string{
		`<title>会社案内 - w-cms</title>`,
		`<meta name="description" content="板金加工を承ります。">`,
		`<meta property="og:title" content="会社案内">`,
		`<meta property="og:type" content="article">`,
		`<link rel="canonical" href="http://example.com/000030">`,
		`<meta property="og:url" content="http://example.com/000030">`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s がありません:\n%s", w, body)
		}
	}
}

// TestPublicViewEscapesMetaValues は、利用者の入力がメタ情報を通じて
// HTMLへ漏れないことを検証します（サニタイズ後に文字列連結する経路なので）。
func TestPublicViewEscapesMetaValues(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000031", `<h1>"><script>alert(1)</script>題名</h1><p>"><b>本文</b></p>`,
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	rr := getPage(t, "/000031", nil)
	body := rr.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("メタ情報経由でスクリプトが混入しています:\n%s", body)
	}
}

// TestPublicPageIsCacheable は、匿名×実効公開の応答だけがキャッシュ可能で
// あることを検証します。`Vary: Cookie` が無いと、共有キャッシュが認証済みの
// 応答を匿名へ配りかねません。
func TestPublicPageIsCacheable(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1><p>本文</p>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	rr := getPage(t, "/000030", nil)
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=600") {
		t.Errorf("公開ページがキャッシュ可能になっていません: %q", cc)
	}
	if v := rr.Header().Get("Vary"); !strings.Contains(v, "Cookie") {
		t.Errorf("Vary: Cookie がありません: %q", v)
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("ETag がありません")
	}
}

// TestAuthenticatedResponseIsNeverCacheable は、認証済みの応答が
// キャッシュされないことを検証します（**絶対条件**・要件 §4.4）。
func TestAuthenticatedResponseIsNeverCacheable(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	rr := getPage(t, "/000030", &auth.User{Username: "alice"})
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("認証済みの応答がキャッシュ可能になっています: %q", cc)
	}
}

// TestPrivatePageResponseIsNeverCacheable は、非公開ページ（匿名には404）の
// 応答がキャッシュされないことを検証します。
func TestPrivatePageResponseIsNeverCacheable(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000032", "<h1>社外秘</h1>", page.PageMeta{Owner: "alice", Mode: "300"})

	rr := getPage(t, "/000032", nil)
	if rr.Code != 404 {
		t.Fatalf("404ではありません: %d", rr.Code)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("非公開ページの応答がキャッシュ可能になっています: %q", cc)
	}
}

// TestPublicPageRevalidatesWithETag は、同じ ETag で問い合わせ直したら
// 304 を返すことを検証します（本文を再送しない）。
func TestPublicPageRevalidatesWithETag(t *testing.T) {
	setupPublicTest(t)
	newPage(t, "000030", "<h1>会社案内</h1><p>本文</p>",
		page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	first := getPage(t, "/000030", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag がありません")
	}

	req := httptest.NewRequest("GET", "/000030", nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	RootHandler(rr, req)
	if rr.Code != 304 {
		t.Errorf("304 になりません: %d", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("304 なのに本文が返っています: %q", rr.Body.String())
	}
}

// TestSitemapListsOnlyPublicPages は、sitemap.xml に実効公開のページだけが
// 載ることを検証します（非公開の存在を外へ漏らさない）。
func TestSitemapListsOnlyPublicPages(t *testing.T) {
	setupPublicTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "300", Public: true})
	newPage(t, "000030", "<h1>会社案内</h1>", page.PageMeta{
		Owner: "alice", Mode: "300", Public: true, ParentID: TopPageID})
	newPage(t, "000032", "<h1>社外秘</h1>", page.PageMeta{
		Owner: "alice", Mode: "300", ParentID: TopPageID})

	req := httptest.NewRequest("GET", "/sitemap.xml", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	SitemapHandler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("200ではありません: %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Errorf("Content-Type が XML ではありません: %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "http://example.com/000030") {
		t.Errorf("公開ページが載っていません:\n%s", body)
	}
	if strings.Contains(body, "000032") {
		t.Errorf("非公開ページが載っています:\n%s", body)
	}
}

// TestRobotsClosesPrivateSite は、サイト全体が非公開（トップが非公開）のときに
// robots.txt が全面拒否になることを検証します。業務インスタンスは匿名に何も
// 見せない運用なので、クローラにも入らせない。
func TestRobotsClosesPrivateSite(t *testing.T) {
	setupPublicTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "300"})

	req := httptest.NewRequest("GET", "/robots.txt", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	RobotsHandler(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "Disallow: /") || strings.Contains(body, "Sitemap:") {
		t.Errorf("非公開サイトなのに開いています:\n%s", body)
	}
}

// TestRobotsOpensPublicSite は、公開サイトでは sitemap を案内し、
// アプリのAPIだけを閉じることを検証します。
func TestRobotsOpensPublicSite(t *testing.T) {
	setupPublicTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	req := httptest.NewRequest("GET", "/robots.txt", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	RobotsHandler(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "Sitemap: http://example.com/sitemap.xml") {
		t.Errorf("sitemap を案内していません:\n%s", body)
	}
	if !strings.Contains(body, "Disallow: /api/") {
		t.Errorf("APIを閉じていません:\n%s", body)
	}
}
