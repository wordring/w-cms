package cms

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 拡張の画面のファイル（ext_scripts.go・2026-10-20）——載っている拡張の ext/<名前>/assets/ から JS・CSS・HTML だけを拾って配り、
// 殻に差し込むことを固定します（置き場の決まり・1本ずつ名乗らない）。
func TestExtAssetsDiscoveredServedAndInjected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ext", "probe", "sub", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"a.js": "window.probe = 1;", "a.css": ".probe{}", "page.html": "<p>probe</p>", "secret.txt": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 載っていない拡張のフォルダは拾わない。
	other := filepath.Join(root, "ext", "notloaded", "assets")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "x.js"), []byte("x"), 0o644)

	savedReg := extensionRegistry
	extensionRegistry = map[string]ExtensionInfo{"probe/sub": {ID: "probe/sub", Name: "試験"}}
	SetExtAssetRoot(root)
	t.Cleanup(func() { extensionRegistry = savedReg; SetExtAssetRoot(".") })

	get := func(method, url string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		ExtScriptHandler(rr, httptest.NewRequest(method, url, nil))
		return rr
	}
	for url, typ := range map[string]string{"/assets/ext/probe/sub/a.js": "javascript", "/assets/ext/probe/sub/a.css": "text/css", "/assets/ext/probe/sub/page.html": "text/html"} {
		rr := get("GET", url)
		if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), typ) {
			t.Errorf("%s を配りません: %d %s", url, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
	for _, url := range []string{"/assets/ext/probe/sub/secret.txt", "/assets/ext/probe/sub/../sub/secret.txt", "/assets/ext/notloaded/x.js"} {
		if rr := get("GET", url); rr.Code != http.StatusNotFound {
			t.Errorf("⚠ 配ってはいけない %s を配っています: %d", url, rr.Code)
		}
	}
	if rr := get("POST", "/assets/ext/probe/sub/a.js"); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST を断っていません: %d", rr.Code)
	}
	if got := extScriptTags(); got != `<script src="/assets/ext/probe/sub/a.js"></script>` {
		t.Errorf("殻へ差し込む JS の形が違います: %s", got)
	}
	if got := extStyleTags(); got != `<link rel="stylesheet" href="/assets/ext/probe/sub/a.css">` {
		t.Errorf("殻へ差し込む CSS の形が違います: %s", got)
	}
}

// TestShellHasExtMarkers は、編集用の殻に拡張の JS・CSS を差し込む印があり、それぞれコアの app.js・app.css のあとにあることを固定します。
func TestShellHasExtMarkers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "assets", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, c := range [][2]string{{extScriptsPlaceholder, `<script src="/assets/app.js">`}, {extStylesPlaceholder, `href="/assets/app.css"`}} {
		at, core := strings.Index(s, c[0]), strings.Index(s, c[1])
		if at < 0 || core < 0 || at < core {
			t.Errorf("殻の印 %s が無いか、コアの %s より前にあります（印 %d・コア %d）", c[0], c[1], at, core)
		}
	}
}

// TestCoreJSDoesNotNameExtensionJS は、コアの app.js が拡張の JS を名指ししないことを固定します（2026-10-20・向きは「拡張 → コア」の1つ）。
// 拡張の働きを呼びたい所は、コアが口（window.wcms）を用意して、拡張が登録します。
func TestCoreJSDoesNotNameExtensionJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(b), "")
	for _, name := range []string{"wcmsMailCompose", "/assets/ext/"} {
		if strings.Contains(code, name) {
			t.Errorf("⚠ コアの app.js が拡張の JS（%s）を名指ししています", name)
		}
	}
}
