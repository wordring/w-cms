package cms

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 表示用の写真（2026-10-27・photo_view.go）。
// 利用者:「表示用の写真（長い辺 1200 px・押すと元の写真）と、見えるまで読まない設定の両方を作ってください。」

func withPhotoView(t *testing.T, lazy bool) {
	t.Helper()
	withSettings(t, func(s *Settings) { s.PhotoView = &PhotoViewSettings{LongSide: 1200, Quality: 85, Lazy: lazy} })
}

// TestRenderPhotoViewURLs は、本文の JPEG の写真が表示用の写しの住所（?view=1&v=）になり、ほかの写真は ?v= のまま、
// lazy なら loading="lazy" が付くことと、保存の手前で全部外れて本文に残らないことを固定します。
func TestRenderPhotoViewURLs(t *testing.T) {
	setupAttachmentTest(t)
	withPhotoView(t, true)
	pj := putAttachment(t, "000101", "ph41.jpg", noisyJPEG(t, 300, 200))
	pp := putAttachment(t, "000101", "ph42.png", []byte("x"))
	fj, _ := os.Stat(pj)
	fp, _ := os.Stat(pp)
	out := RenderAttachmentVersions(`<p><img src="/000101/ph41.jpg" alt="a"><img src="/000101/ph42.png"></p>`)
	for _, want := range []string{
		`<img loading="lazy" src="/000101/ph41.jpg?view=1&amp;v=` + page.AttachmentVersion(fj) + `"`,
		`<img loading="lazy" src="/000101/ph42.png?v=` + page.AttachmentVersion(fp) + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ %q がありません: %s", want, out)
		}
	}
	// 画面が書き出した形（& は &amp;・loading 付き）でも、保存の手前で全部外れる。
	saved := Sanitize(`<p data-id="aaaa">` + strings.Replace(out, "<p>", "", 1))
	if strings.Contains(saved, "v=") || strings.Contains(saved, "view=") || strings.Contains(saved, "loading") ||
		!strings.Contains(saved, `src="/000101/ph41.jpg"`) || !strings.Contains(saved, `src="/000101/ph42.png"`) {
		t.Errorf("⚠ 印か loading が本文に残ります: %s", saved)
	}
	// lazy を外せば loading は付かない（利用者:「見えるまで読まないのがストレスになるようなら後から外します」）。
	withPhotoView(t, false)
	if out := RenderAttachmentVersions(`<img src="/000101/ph41.jpg">`); strings.Contains(out, "loading") || !strings.Contains(out, "?view=1&amp;v=") {
		t.Errorf("⚠ lazy を外しても loading が付くか、表示用の写しを指していません: %s", out)
	}
}

// TestServePhotoView は、表示用の写真の配り口が、長い写真を長い辺 1200 px に縮めて貯め（2回目は貯めたもの）、小さい写真は
// 元のまま配り、元の写真と同じ権限・同じ残し方で配り、上書きすると作り直して古い写しを消すことを固定します。
func TestServePhotoView(t *testing.T) {
	setupAttachmentTest(t)
	withPhotoView(t, true)
	big := putAttachment(t, "000101", "ph43.jpg", noisyJPEG(t, 2400, 1600))
	putAttachment(t, "000101", "ph44.jpg", noisyJPEG(t, 800, 600))
	putAttachment(t, "000103", "ph45.jpg", noisyJPEG(t, 2400, 1600)) // 非公開のページ
	get := func(pid, name, q string, user *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/"+pid+"/"+name+"?"+q, nil)
		if user != nil {
			req = auth.WithUser(req, user)
		}
		rr := httptest.NewRecorder()
		ServePhotoView(rr, req, pid, name, http.NotFound)
		return rr
	}
	alice := &auth.User{Username: "alice", IsAdmin: true}
	fi, _ := os.Stat(big)
	v := page.AttachmentVersion(fi)
	rr := get("000101", "ph43.jpg", "view=1&v="+v, alice)
	img, err := jpeg.DecodeConfig(bytes.NewReader(rr.Body.Bytes()))
	if rr.Code != 200 || err != nil || img.Width != 1200 || img.Height != 800 {
		t.Fatalf("⚠ 長い辺 1200 px に縮めていません: %d %v %dx%d", rr.Code, err, img.Width, img.Height)
	}
	if rr.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || rr.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("⚠ 配り方が元の写真と違います: %q %q", rr.Header().Get("Cache-Control"), rr.Header().Get("Content-Type"))
	}
	cached, _ := photoViewPath("000101", "ph43.jpg", v)
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("⚠ 写しを貯めていません: %v", err)
	}
	if rr2 := get("000101", "ph43.jpg", "view=1&v="+v, alice); !bytes.Equal(rr2.Body.Bytes(), rr.Body.Bytes()) {
		t.Errorf("2回目に違う写しを配りました")
	}
	small := get("000101", "ph44.jpg", "view=1", alice)
	if c, _ := jpeg.DecodeConfig(bytes.NewReader(small.Body.Bytes())); small.Code != 200 || c.Width != 800 {
		t.Errorf("⚠ 小さい写真を元のまま配っていません: %d %d", small.Code, c.Width)
	}
	if rr := get("000103", "ph45.jpg", "view=1", nil); rr.Code == 200 {
		t.Errorf("⚠ 非公開のページの写真を匿名に配りました")
	}

	// 上書きすると、版の印が変わって作り直し、古い写しは消える。
	os.WriteFile(big, noisyJPEG(t, 1600, 2400), 0o644)
	os.Chtimes(big, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	fi2, _ := os.Stat(big)
	v2 := page.AttachmentVersion(fi2)
	rr3 := get("000101", "ph43.jpg", "view=1&v="+v2, alice)
	if c, _ := jpeg.DecodeConfig(bytes.NewReader(rr3.Body.Bytes())); c.Width != 800 || c.Height != 1200 {
		t.Errorf("⚠ 上書きした写真の写しを作り直していません: %dx%d", c.Width, c.Height)
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Errorf("⚠ 古い版の写しが残っています")
	}
}

// TestFileViewPhotoUsesViewCopy は、ファイル表示とカルーセルの写真も表示用の写しを指すことを固定します。
func TestFileViewPhotoUsesViewCopy(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.jpg")
	writePDF(t, "000001", "c3p7.jpg", string(noisyJPEG(t, 300, 200)))
	withPhotoView(t, true)
	alice := &auth.User{Username: "alice", IsAdmin: true}
	out := renderFileViewBody(t, alice, 1, `<section data-type="file-view" data-ref="000001-c3p7"></section>`+
		`<section data-type="carousel"><section data-type="file-view" data-ref="000001-c3p7"></section></section>`)
	if n := strings.Count(out, `/000001/c3p7.jpg?view=1&amp;v=`); n != 2 {
		t.Errorf("⚠ ファイル表示とカルーセルの写真が表示用の写しを指していません（%d か所）:\n%s", n, out)
	}
	if n := strings.Count(out, `loading="lazy"`); n != 2 {
		t.Errorf("⚠ loading=\"lazy\" が %d か所（2 のはず）:\n%s", n, out)
	}
}
