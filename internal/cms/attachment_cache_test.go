package cms

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestAttachmentCacheByVersion は、添付の配信が（2026-10-27）
//   - 住所の ?v= が今の版の印と合えば、その人のブラウザに確かめずに使わせる（private・immutable）
//   - 印が無い・古ければ、毎回確かめさせる（private・no-cache——変わっていなければ 304）
//   - どちらも中継（VPS）には残させない（private）
//
// ことを固定します。
func TestAttachmentCacheByVersion(t *testing.T) {
	setupAttachmentTest(t)
	p := putAttachment(t, "000101", "ph11.jpg", noisyJPEG(t, 300, 200))
	fi, _ := os.Stat(p)
	v := page.AttachmentVersion(fi)
	get := func(url string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", url, nil)
		for k, val := range hdr {
			req.Header.Set(k, val)
		}
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		page.ServeCleanAttachment(rr, req, "000101", "ph11.jpg", http.NotFound)
		return rr
	}
	if rr := get("/000101/ph11.jpg?v="+v, nil); rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Errorf("今の印の住所を手元に残させていません: %d %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	for _, url := range []string{"/000101/ph11.jpg", "/000101/ph11.jpg?v=old"} {
		if rr := get(url, nil); rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s を毎回確かめさせていません: %d %q", url, rr.Code, rr.Header().Get("Cache-Control"))
		}
	}
	// 変わっていなければ中身を送らない。
	if rr := get("/000101/ph11.jpg", map[string]string{"If-Modified-Since": fi.ModTime().UTC().Add(time.Second).Format(http.TimeFormat)}); rr.Code != http.StatusNotModified {
		t.Errorf("変わっていないのに中身を送りました: %d", rr.Code)
	}
}

// TestRenderAttachmentVersions は、閲覧の描き方で本文の写真の住所に今の版の印を添えることを固定します（2026-10-27）。
// 無い添付・印の付いた住所・添付でない住所には触りません。
func TestRenderAttachmentVersions(t *testing.T) {
	setupAttachmentTest(t)
	p := putAttachment(t, "000101", "ph12.jpg", noisyJPEG(t, 300, 200))
	fi, _ := os.Stat(p)
	body := `<p><img src="/000101/ph12.jpg" alt="現場"><img alt="x" src="/000101/none.jpg"><img src="/000101/ph12.jpg?v=abc"><img src="/assets/a.png"></p>`
	out := RenderAttachmentVersions(body)
	want := `<p><img src="/000101/ph12.jpg?v=` + page.AttachmentVersion(fi) + `" alt="現場"><img alt="x" src="/000101/none.jpg"><img src="/000101/ph12.jpg?v=abc"><img src="/assets/a.png"></p>`
	if out != want {
		t.Errorf("印の添え方が違います:\n got %s\nwant %s", out, want)
	}
	// 上書きすると印が変わる。
	time.Sleep(5 * time.Millisecond)
	os.WriteFile(p, noisyJPEG(t, 200, 100), 0o644)
	if again := RenderAttachmentVersions(`<img src="/000101/ph12.jpg">`); strings.Contains(again, page.AttachmentVersion(fi)) || !strings.Contains(again, "?v=") {
		t.Errorf("上書きしても印が変わりません: %s", again)
	}
}

// TestLoadViewAddsAttachmentVersions は、閲覧の描き方（/api/load?view=1 とページを開いたとき）だけが写真の住所に版の印を添え、
// 書くための素の /api/load には添えない（添えると本文として保存される）ことを固定します（2026-10-27）。
func TestLoadViewAddsAttachmentVersions(t *testing.T) {
	setupAttachmentTest(t)
	putAttachment(t, "000101", "ph13.jpg", noisyJPEG(t, 300, 200))
	newPage(t, "000101", `<h1>部品A</h1><p><img src="/000101/ph13.jpg" alt="現場"></p>`, page.PageMeta{Owner: "alice", Mode: "333", ParentID: TopPageID})
	load := func(q string) string {
		req := httptest.NewRequest("GET", "/api/load?id=000101"+q, nil)
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		LoadAPIHandler(rr, req)
		if rr.Code != 200 {
			t.Fatalf("/api/load%s: %d %s", q, rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}
	if plain := load(""); strings.Contains(plain, "ph13.jpg?v=") {
		t.Errorf("素の /api/load に版の印が入っています（本文として保存されてしまう）: %s", plain)
	}
	if view := load("&view=1"); !strings.Contains(view, `src="/000101/ph13.jpg?v=`) {
		t.Errorf("閲覧の描き方に版の印がありません: %s", view)
	}
}
