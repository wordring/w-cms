package cms

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 拡張の画面の JS（ext_scripts.go・2026-10-20）——登録した1本だけを配り、殻に差し込むことを固定します。
func TestExtScriptServedAndInjected(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.js")
	if err := os.WriteFile(file, []byte("window.probe = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.js"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := extScripts
	extScripts = map[string]extScript{}
	t.Cleanup(func() { extScripts = saved })
	RegisterScript("probe", filepath.ToSlash(file))

	get := func(url string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		ExtScriptHandler(rr, httptest.NewRequest("GET", url, nil))
		return rr
	}
	if rr := get("/assets/ext/probe/probe.js"); rr.Code != 200 || !strings.Contains(rr.Body.String(), "window.probe") {
		t.Fatalf("登録した JS を配りません: %d %s", rr.Code, rr.Body.String())
	}
	for _, url := range []string{"/assets/ext/probe/secret.js", "/assets/ext/probe/../probe/secret.js", "/assets/ext/other/probe.js"} {
		if rr := get(url); rr.Code != http.StatusNotFound {
			t.Errorf("⚠ 登録していない %s を配っています: %d", url, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	ExtScriptHandler(rr, httptest.NewRequest("POST", "/assets/ext/probe/probe.js", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST を断っていません: %d", rr.Code)
	}
	if got := extScriptTags(); got != `<script src="/assets/ext/probe/probe.js"></script>` {
		t.Errorf("殻へ差し込む形が違います: %s", got)
	}
}

// TestShellHasExtScriptsMarker は、編集用の殻に拡張の JS を差し込む印があり、コアの app.js のあとにあることを固定します。
func TestShellHasExtScriptsMarker(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "assets", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	at, app := strings.Index(s, extScriptsPlaceholder), strings.Index(s, `<script src="/assets/app.js">`)
	if at < 0 || app < 0 || at < app {
		t.Errorf("殻の印が無いか、app.js より前にあります（印 %d・app.js %d）", at, app)
	}
}

// TestCoreJSDoesNotNameExtensionJS は、コアの app.js が拡張の JS を名指ししないことを固定します（2026-10-20・向きは「拡張 → コア」の1つ）。
// 拡張の働きを呼びたい所は、コアが口（window.wcms）を用意して、拡張が登録します。
func TestCoreJSDoesNotNameExtensionJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wcmsMailCompose", "/assets/ext/"} {
		if strings.Contains(string(b), name) {
			t.Errorf("⚠ コアの app.js が拡張の JS（%s）を名指ししています", name)
		}
	}
}
