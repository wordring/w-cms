package cms

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 画面のファイルに版の印を添える（2026-10-27・【考察】キャッシュ制御 案B・asset_version.go）。

// inAssetDir は、作業の場所を一時の場所へ移し、assets/ に files を置きます（終わったら戻す）。
func inAssetDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	for name, body := range files {
		p := filepath.Join(dir, "assets", filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStampAssetURLs は、HTML の中の画面のファイル（JS・CSS）の住所に今の版の印を添え、無いファイル・問い合わせの付いた住所・
// 画面のファイルでない住所には触らないことを固定します。
func TestStampAssetURLs(t *testing.T) {
	inAssetDir(t, map[string]string{"app.js": "1", "app.css": "body{}"})
	fi, _ := os.Stat(filepath.Join("assets", "app.js"))
	in := `<script src="/assets/app.js"></script><link rel="stylesheet" href="/assets/app.css"><script src="/assets/none.js"></script>` +
		`<script src="/assets/app.js?x=1"></script><a href="/001351">p</a><img src="/assets/logo.png">`
	got := StampAssetURLs(in)
	if !strings.Contains(got, `src="/assets/app.js?v=`+assetVersion(fi)+`"`) || !strings.Contains(got, `href="/assets/app.css?v=`) {
		t.Errorf("⚠ 画面のファイルに印が添わっていません: %s", got)
	}
	for _, keep := range []string{`src="/assets/none.js"`, `src="/assets/app.js?x=1"`, `href="/001351"`, `src="/assets/logo.png"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("⚠ %q を変えました: %s", keep, got)
		}
	}
	// 書き換えると印が変わる（入れ替えたら新しい住所になる）。
	before := StampAssetURLs(`<script src="/assets/app.js"></script>`)
	os.WriteFile(filepath.Join("assets", "app.js"), []byte("22"), 0o644)
	os.Chtimes(filepath.Join("assets", "app.js"), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if after := StampAssetURLs(`<script src="/assets/app.js"></script>`); after == before {
		t.Errorf("⚠ 書き換えても印が変わりません: %s", after)
	}
}

// TestAssetsHandlerCache は、/assets/ の配り口が、今の印の住所は1年使わせ（public・immutable）、印が無い・古い住所は毎回確かめさせ、
// HTML のページは読むファイルに印を添えて ETag で確かめさせる（変わっていなければ 304）ことを固定します。
func TestAssetsHandlerCache(t *testing.T) {
	inAssetDir(t, map[string]string{"app.js": "1", "admin.html": `<html><script src="/assets/app.js"></script></html>`})
	files := http.StripPrefix("/assets/", http.FileServer(http.Dir("assets")))
	h := AssetsHandler(files)
	get := func(url string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr
	}
	v, _ := assetVersionOf("/assets/app.js")
	if rr := get("/assets/app.js?v="+v, nil); rr.Code != 200 || rr.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("⚠ 今の印の住所を1年使わせていません: %d %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	for _, u := range []string{"/assets/app.js", "/assets/app.js?v=old"} {
		if rr := get(u, nil); rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("⚠ %s を毎回確かめさせていません: %d %q", u, rr.Code, rr.Header().Get("Cache-Control"))
		}
	}
	page := get("/assets/admin.html", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `src="/assets/app.js?v=`+v+`"`) || page.Header().Get("Cache-Control") != "no-cache" || page.Header().Get("ETag") == "" {
		t.Fatalf("⚠ HTML のページに印が添わっていないか、確かめさせていません: %d %q %q %s", page.Code, page.Header().Get("Cache-Control"), page.Header().Get("ETag"), page.Body.String())
	}
	if rr := get("/assets/admin.html", map[string]string{"If-None-Match": page.Header().Get("ETag")}); rr.Code != http.StatusNotModified {
		t.Errorf("変わっていない HTML のページに中身を送りました: %d", rr.Code)
	}
	// 読むファイルが変われば、HTML のページのファイルが同じでも ETag が変わる（古い印の住所を指し続けない）。
	os.WriteFile(filepath.Join("assets", "app.js"), []byte("22"), 0o644)
	os.Chtimes(filepath.Join("assets", "app.js"), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if rr := get("/assets/admin.html", map[string]string{"If-None-Match": page.Header().Get("ETag")}); rr.Code != 200 || rr.Header().Get("ETag") == page.Header().Get("ETag") {
		t.Errorf("⚠ 読むファイルが変わったのに、古い HTML のページを使わせます: %d", rr.Code)
	}
}
