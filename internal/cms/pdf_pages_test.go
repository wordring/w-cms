package cms

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// PDF を絵にして見せる（2026-10-27・pdf_pages.go）。
// 利用者:「PDFをインライン表示すると大量のJSを読み込むようです。SVGなどに変換して読む方法は無いですか？」
// →全部のページ・PNG・押すと本物の PDF を開く。
//
// 本物の mutool の代わりに、この試験の実行ファイル自身を道具として呼びます（TestMain が fakeMutoolEnv を見る）。
// 偽の PDF の中身は「PAGES=<枚数>」で、FAIL と書けば描けない PDF です。

const fakeMutoolEnv = "WCMS_FAKE_MUTOOL"

// fakeMutool は mutool の draw と show の真似です（終了コードを返す）。
func fakeMutool(args []string) int {
	if len(args) == 0 {
		return 2
	}
	pages := func(pdf string) int {
		b, _ := os.ReadFile(pdf)
		s := string(b)
		if strings.Contains(s, "FAIL") {
			return -1
		}
		n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s[strings.Index(s, "PAGES=")+6:], "")))
		return n
	}
	switch args[0] {
	case "draw": // draw -q -w W -F png -o <out>/p%d.png <pdf> 1-<max>
		var out, pdf, rng string
		for i := 1; i < len(args); i++ {
			if args[i] == "-o" {
				out = args[i+1]
				i++
				continue
			}
			if args[i] == "-w" || args[i] == "-F" {
				i++
				continue
			}
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			if pdf == "" {
				pdf = args[i]
			} else {
				rng = args[i]
			}
		}
		n := pages(pdf)
		if n < 0 {
			os.Stderr.WriteString("error: cannot open document\n")
			return 1
		}
		max, _ := strconv.Atoi(strings.TrimPrefix(rng, "1-"))
		if n > max {
			n = max
		}
		img := image.NewRGBA(image.Rect(0, 0, 4, 3))
		img.Set(0, 0, color.Black)
		var buf bytes.Buffer
		png.Encode(&buf, img)
		for i := 1; i <= n; i++ {
			os.WriteFile(strings.Replace(out, "%d", strconv.Itoa(i), 1), buf.Bytes(), 0o644)
		}
		return 0
	case "show":
		os.Stdout.WriteString(strconv.Itoa(pages(args[1])) + "\n")
		return 0
	}
	return 2
}

// withFakeMutool は、設定 pdf_pages の道具をこの試験の実行ファイルにします。
func withFakeMutool(t *testing.T, maxPages int) {
	t.Helper()
	t.Setenv(fakeMutoolEnv, "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	withSettings(t, func(s *Settings) { s.PDFPages = &PDFPagesSettings{Tool: exe, Width: 1600, MaxPages: maxPages} })
	pdfPagesMu.Lock()
	pdfPagesFailed = map[string]bool{}
	pdfPagesMu.Unlock()
}

func writePDF(t *testing.T, hostID, name, content string) {
	t.Helper()
	dir := filepath.Join(page.GetPageDir(hostID), "files")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFileViewDrawsPDFPages は、PDF のファイル表示が、描いた全部のページの絵を縦に並べ、押すと本物の PDF を開くことを固定します。
// 上限より多いページは「ほかに N ページ」と添え、描けない PDF は、いままでどおり埋め込みます。
func TestFileViewDrawsPDFPages(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.pdf")
	withFakeMutool(t, 2)
	writePDF(t, "000001", "c3p7.pdf", "%PDF PAGES=2")
	alice := &auth.User{Username: "alice", IsAdmin: true}

	out := renderFileViewBody(t, alice, 1, `<section data-type="file-view" data-ref="000001-c3p7"></section>`)
	for _, want := range []string{`class="file-view-body file-view-pages"`, `?pdfpage=1&amp;v=`, `?pdfpage=2&amp;v=`, `target="_blank"`, `href="/000001/c3p7.pdf?v=`} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ %q がありません:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<embed") || strings.Contains(out, "ほかに") {
		t.Errorf("⚠ 絵にできたのに埋め込んだか、余計な「ほかに」があります:\n%s", out)
	}

	// 上限（2枚）より多いページの PDF。
	writePDF(t, "000001", "m4n5.pdf", "%PDF PAGES=5")
	out = renderFileViewBody(t, alice, 1, `<section data-type="file-view" data-ref="000001-m4n5"></section>`)
	if !strings.Contains(out, "?pdfpage=2&amp;") || strings.Contains(out, "?pdfpage=3&amp;") || !strings.Contains(out, "ほかに 3 ページあります") {
		t.Errorf("⚠ 上限までの絵と「ほかに 3 ページ」が出ていません:\n%s", out)
	}

	// 描けない PDF は埋め込む。
	writePDF(t, "000001", "f6g7.pdf", "%PDF FAIL")
	out = renderFileViewBody(t, alice, 1, `<section data-type="file-view" data-ref="000001-f6g7"></section>`)
	if !strings.Contains(out, `<embed class="file-view-body" type="application/pdf"`) {
		t.Errorf("⚠ 描けない PDF を埋め込んでいません:\n%s", out)
	}
}

// TestServePDFPage は、PDF の1ページの絵の配り口が、PDF と同じ権限・同じ残し方で配り、描いていないページは 404 にすることと、
// PDF を上書きすると描き直して古い絵を消すことを固定します。
func TestServePDFPage(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.pdf")
	withFakeMutool(t, 20)
	writePDF(t, "000001", "c3p7.pdf", "%PDF PAGES=2")
	res, ver, ok := PDFPagesFor("000001", "c3p7.pdf", 10*time.Second)
	if !ok || res.Drawn != 2 || res.Total != 2 {
		t.Fatalf("描けません: %+v %v", res, ok)
	}
	get := func(q string, user *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/000001/c3p7.pdf?"+q, nil)
		if user != nil {
			req = auth.WithUser(req, user)
		}
		rr := httptest.NewRecorder()
		ServePDFPage(rr, req, "000001", "c3p7.pdf")
		return rr
	}
	alice := &auth.User{Username: "alice", IsAdmin: true}
	rr := get("pdfpage=1&v="+ver, alice)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" || rr.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Errorf("⚠ 1ページ目の絵の配り方が違います: %d %q %q", rr.Code, rr.Header().Get("Content-Type"), rr.Header().Get("Cache-Control"))
	}
	if rr := get("pdfpage=3", alice); rr.Code != http.StatusNotFound {
		t.Errorf("描いていないページを配りました: %d", rr.Code)
	}
	if rr := get("pdfpage=1", nil); rr.Code == 200 {
		t.Errorf("⚠ 非公開のページの PDF の絵を匿名に配りました")
	}

	// 上書きすると、版の印が変わって描き直し、古い絵は消える。
	old := pdfPagesDir("000001", "c3p7.pdf", ver)
	writePDF(t, "000001", "c3p7.pdf", "%PDF PAGES=1")
	p, _ := page.AttachmentPath("000001", "c3p7.pdf")
	os.Chtimes(p, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	res2, ver2, ok := PDFPagesFor("000001", "c3p7.pdf", 10*time.Second)
	if !ok || ver2 == ver || res2.Drawn != 1 {
		t.Fatalf("⚠ 上書きしたのに描き直していません: %+v %s %s", res2, ver, ver2)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("⚠ 古い版の絵が残っています: %s", old)
	}
}

// TestPDFPagesSettingsValidated は設定 pdf_pages の柵を固定します。
func TestPDFPagesSettingsValidated(t *testing.T) {
	for _, c := range []PDFPagesSettings{{Tool: "", Width: 1600, MaxPages: 20}, {Tool: "mutool", Width: 100, MaxPages: 20}, {Tool: "mutool", Width: 1600, MaxPages: 0}} {
		c := c
		if err := validatePDFPages("x", &c); err == nil {
			t.Errorf("通してはいけない設定を通しました: %+v", c)
		}
	}
	if err := validatePDFPages("x", &PDFPagesSettings{Tool: "mutool", Width: 1600, MaxPages: 20}); err != nil {
		t.Error(err)
	}
}
