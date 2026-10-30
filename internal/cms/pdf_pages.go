package cms

// ─────────────────────────────────────────────────────────────────────────
// PDF を絵にして見せる（2026-10-27）
//
// 利用者:「PDFをインライン表示すると大量のJSを読み込むようです。SVGなどに変換して読む方法は無いですか？」
// →（問いへの答え）全部のページ・PNG・押すと本物の PDF を開く（どれも筆者の勧め）。
//
// ファイル表示の枠に PDF を `<embed>` で埋めると、枠1つごとにブラウザの PDF ビューアが1つ起きます（Chrome では、ビューアの
// 画面の JS をまとめて読み込む）。図面の多いページは重くなります。そこで、サーバーが PDF の各ページを PNG に描き、枠には絵を
// 縦に並べます（file_view.go）。押すと本物の PDF を開きます（拡大・印刷・ページ送りは PDF ビューアで）。
//
//   - 描くのは外の道具 mutool（MuPDF）です。w-cms には入れず（AGPL・同梱しない）、サーバーに入っているときだけ使います。
//     設定 `pdf_pages`（道具の名前 `tool`・絵の横幅 `width`・描く枚数の上限 `max_pages`）が無いか、道具が無ければ、
//     いままでどおり埋め込みます。
//   - 描いた絵は データの置き場/cache/pdf-pages/<ページ番号>/<保存名>.<版の印>/ に貯めます。PDF を上書きすると版の印が変わり、
//     新しく描き直します（古い印の絵は消す）。夜の控えには入りません（w-cms-backup は決まったフォルダだけを写す）。
//   - 初めて見るときに描きます。待つのは数秒まで（pdfPagesWait）で、間に合わなければその回は埋め込みで見せ、裏で描き終えます
//     （次に開いたときから絵）。同時に描くのは2つまで。1つの PDF を描くのは1分まで（壊れた PDF で止まらないように）。
//   - ⚠ 外から届いた PDF も描くので、mutool が PDF を読むところに穴があれば、そこを突かれえます。時間の上限で止め、
//     w-cms の systemd の閉じ込め（deploy/server/w-cms.service）の中で動かします。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"context"
	"fmt"
	stdhtml "html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PDFPagesSettings は設定 `pdf_pages` です。
type PDFPagesSettings struct {
	// Tool は PDF を描く道具（mutool）の名前か場所です。
	Tool string `json:"tool"`
	// Width は描いた絵の横幅（画素）です。
	Width int `json:"width"`
	// MaxPages は1つの PDF で描く枚数の上限です。これより多いページは、押して PDF で見てもらう。
	MaxPages int `json:"max_pages"`
}

func validatePDFPages(path string, c *PDFPagesSettings) error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.Tool) == "" {
		return fmt.Errorf("%s: pdf_pages の tool（PDF を描く道具の名前）を書いてください", path)
	}
	if c.Width < 600 || c.Width > 4000 {
		return fmt.Errorf("%s: pdf_pages の width は 600 から 4000 のあいだで書いてください", path)
	}
	if c.MaxPages < 1 || c.MaxPages > 200 {
		return fmt.Errorf("%s: pdf_pages の max_pages は 1 から 200 のあいだで書いてください", path)
	}
	return nil
}

// PDFPagesConfig は設定 `pdf_pages` を返します（無ければ nil＝絵にしない）。
func PDFPagesConfig() *PDFPagesSettings { return settingsField(func(s *Settings) *PDFPagesSettings { return s.PDFPages }) }

const (
	// pdfPagesWait は、ページを描くときに PDF の絵を待つ長さです。間に合わなければ、その回は埋め込みで見せる。
	pdfPagesWait = 4 * time.Second
	// pdfDrawMemBytes は、PDF を描く道具の使ってよいメモリです（大きな図面も描けるように、作り直しの 1 GiB より多め）。
	pdfDrawMemBytes = 2 << 30
	// pdfPagesTimeout は1つの PDF を描く長さの上限です。
	pdfPagesTimeout = time.Minute
	// pdfPagesDoneFile は、描き終えた置き場に書く印のファイルです（中身は「描いた枚数 全部の枚数」）。
	pdfPagesDoneFile = "done"
)

// pdfPagesResult は描いた結果です。
type pdfPagesResult struct {
	Drawn int // 描いた枚数（p1.png 〜）
	Total int // PDF の全部のページ数（読めなければ Drawn）
}

var (
	pdfPagesMu      sync.Mutex
	pdfPagesRunning = map[string]chan struct{}{} // 描いている置き場 → 描き終えたら閉じる
	pdfPagesFailed  = map[string]bool{}          // 描けなかった置き場（起動し直すまで描き直さない）
	pdfPagesSlots   = make(chan struct{}, 2)     // 同時に描くのは2つまで
)

// pdfPagesDir は PDF の絵の置き場です。
func pdfPagesDir(pageID, name, ver string) string {
	return database.DataPath("cache", "pdf-pages", pageID, name+"."+ver)
}

// readPDFPagesDone は、描き終えた置き場の結果を読みます（まだなら false）。
func readPDFPagesDone(dir string) (pdfPagesResult, bool) {
	b, err := os.ReadFile(filepath.Join(dir, pdfPagesDoneFile))
	if err != nil {
		return pdfPagesResult{}, false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return pdfPagesResult{}, false
	}
	drawn, err1 := strconv.Atoi(f[0])
	total, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || drawn < 1 {
		return pdfPagesResult{}, false
	}
	return pdfPagesResult{Drawn: drawn, Total: total}, true
}

// PDFPagesFor は、添付の PDF（ページ pageID の保存名 name）の絵を返します。まだなら描き始め、wait まで待ちます。
// 設定が無い・道具が無い・描けない・間に合わないときは false（呼び手は埋め込みで見せる）。
func PDFPagesFor(pageID, name string, wait time.Duration) (pdfPagesResult, string, bool) {
	conf := PDFPagesConfig()
	if conf == nil {
		return pdfPagesResult{}, "", false
	}
	p, ok := page.AttachmentPath(pageID, name)
	if !ok {
		return pdfPagesResult{}, "", false
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return pdfPagesResult{}, "", false
	}
	ver := page.AttachmentVersion(fi)
	dir := pdfPagesDir(pageID, name, ver)
	if res, ok := readPDFPagesDone(dir); ok {
		return res, ver, true
	}
	pdfPagesMu.Lock()
	if pdfPagesFailed[dir] {
		pdfPagesMu.Unlock()
		return pdfPagesResult{}, "", false
	}
	done, running := pdfPagesRunning[dir]
	if !running {
		done = make(chan struct{})
		pdfPagesRunning[dir] = done
		go drawPDFPages(*conf, p, pageID, name, ver, dir, done)
	}
	pdfPagesMu.Unlock()
	select {
	case <-done:
	case <-time.After(wait):
		return pdfPagesResult{}, "", false
	}
	res, ok := readPDFPagesDone(dir)
	return res, ver, ok
}

// drawPDFPages は PDF の各ページを PNG に描いて dir に置きます（描き終えたら done を閉じる）。
func drawPDFPages(conf PDFPagesSettings, pdfPath, pageID, name, ver, dir string, done chan struct{}) {
	ok := false
	defer func() {
		pdfPagesMu.Lock()
		delete(pdfPagesRunning, dir)
		if !ok {
			pdfPagesFailed[dir] = true
		}
		pdfPagesMu.Unlock()
		close(done)
	}()
	pdfPagesSlots <- struct{}{}
	defer func() { <-pdfPagesSlots }()

	ctx, cancel := context.WithTimeout(context.Background(), pdfPagesTimeout)
	defer cancel()
	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	// mutool draw -q -w <横幅> -F png -o <置き場>/p%d.png <PDF> 1-<上限>
	var stderr bytes.Buffer
	// 高さにも上限（横幅の3倍）——縦に極端に長いページ（10×500pt など）を横幅に合わせると、1ページで何 GB もの絵になった
	// （2026-10-27 の見直し）。mutool は -w と -h の枠に収まるよう縮める。
	// メモリも絞る（2026-10-30。外から届いた PDF を描くので、絞らないと w-cms の上限を食いつぶして、w-cms ごと止まりうる）。
	// 描けなければ埋め込みで見せるので、絞っても見えなくはならない。
	cmd := limitedCommand(ctx, conf.Tool, pdfDrawMemBytes, "draw", "-q", "-w", strconv.Itoa(conf.Width), "-h", strconv.Itoa(conf.Width*3), "-F", "png",
		"-o", filepath.Join(tmp, "p%d.png"), pdfPath, "1-"+strconv.Itoa(conf.MaxPages))
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		logPDFPages(pageID, name, "描けません: "+err.Error()+" "+strings.TrimSpace(firstLine(stderr.String())))
		return
	}
	drawn := 0
	for i := 1; i <= conf.MaxPages; i++ {
		if _, err := os.Stat(filepath.Join(tmp, "p"+strconv.Itoa(i)+".png")); err != nil {
			break
		}
		drawn = i
	}
	if drawn == 0 {
		logPDFPages(pageID, name, "1枚も描けませんでした")
		return
	}
	total := drawn
	if drawn == conf.MaxPages {
		// 上限まで描いたときだけ、全部のページ数を聞く（「ほかに N ページ」を出すため）。
		if out, err := exec.CommandContext(ctx, conf.Tool, "show", pdfPath, "trailer/Root/Pages/Count").Output(); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && n > drawn {
				total = n
			}
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, pdfPagesDoneFile), []byte(strconv.Itoa(drawn)+" "+strconv.Itoa(total)+"\n"), 0o644); err != nil {
		return
	}
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return
	}
	ok = true
	// 同じ PDF の古い版の絵を消す。⚠ 描いているあいだに PDF が上書きされていたら、自分が古い版なので、ほかを消さない
	// （新しい版の絵を「古い」として消していた・2026-10-27 の見直し）。自分の絵は、新しい版の絵が描けたときに消える。
	if fi, err := os.Stat(pdfPath); err != nil || page.AttachmentVersion(fi) != ver {
		return
	}
	parent := filepath.Dir(dir)
	if ents, err := os.ReadDir(parent); err == nil {
		for _, e := range ents {
			if e.IsDir() && strings.HasPrefix(e.Name(), name+".") && e.Name() != name+"."+ver && !strings.HasSuffix(e.Name(), ".partial") {
				os.RemoveAll(filepath.Join(parent, e.Name()))
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// logPDFPages は描けなかったことをサーバーの記録に残します。
var logPDFPages = func(pageID, name, msg string) {
	fmt.Fprintf(os.Stderr, "PDF の絵: /%s/%s %s\n", pageID, name, msg)
}

// ServePDFPage は PDF の1ページの絵（/<ページ番号>/<保存名>?pdfpage=<N>）を配ります。読む権限は PDF と同じ
// （実効公開なら匿名も）。手元への残し方も PDF と同じで、住所の版の印（?v=）は PDF の印です（page.SetAttachmentCache）。
// まだ描いていなければ 404（画面は描けたものしか指さない）。
func ServePDFPage(w http.ResponseWriter, r *http.Request, pageID, name string) {
	if !strings.EqualFold(filepath.Ext(name), ".pdf") || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	n, err := strconv.Atoi(r.URL.Query().Get("pdfpage"))
	if err != nil || n < 1 {
		http.NotFound(w, r)
		return
	}
	if !page.RequirePageReadOrPublic(w, r, pageID) {
		return
	}
	if !page.AnonymousMayFetch(r, pageID, name) { // ログインしていない人には、本文が指す添付だけ（2026-10-27）
		http.NotFound(w, r)
		return
	}
	p, ok := page.AttachmentPath(pageID, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir := pdfPagesDir(pageID, name, page.AttachmentVersion(fi))
	res, ok := readPDFPagesDone(dir)
	if !ok || n > res.Drawn {
		http.NotFound(w, r)
		return
	}
	img := filepath.Join(dir, "p"+strconv.Itoa(n)+".png")
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	page.SetAttachmentCache(w, r, fi)
	http.ServeFile(w, r, img)
}

// pdfPagesHTML は、ファイル表示の枠の中身（PDF の各ページの絵を縦に並べたもの）です。絵を押すと本物の PDF を新しいタブで開きます。
// 描いていないページがあれば、その数を添えます。url は添付の住所（/ページ番号/保存名）・ver は PDF の版の印。
func pdfPagesHTML(url, fileName, ver string, res pdfPagesResult) string {
	src := stdhtml.EscapeString(url)
	name := stdhtml.EscapeString(fileName)
	var b strings.Builder
	b.WriteString(`<div class="file-view-body file-view-pages"><a class="file-view-pages-link" href="` + src + `?v=` + stdhtml.EscapeString(ver) +
		`" target="_blank" rel="noopener" title="押すと PDF を開きます（拡大・印刷・保存はそちらで）">`)
	for i := 1; i <= res.Drawn; i++ {
		n := strconv.Itoa(i)
		b.WriteString(`<img class="file-view-page" src="` + src + `?pdfpage=` + n + `&amp;v=` + stdhtml.EscapeString(ver) + `" alt="` + name + `（` + n + `ページ）"`)
		if i > 1 {
			b.WriteString(` loading="lazy"`)
		}
		b.WriteString(`>`)
	}
	b.WriteString(`</a>`)
	if rest := res.Total - res.Drawn; rest > 0 {
		b.WriteString(`<p class="file-view-pages-more">ほかに ` + strconv.Itoa(rest) + ` ページあります。絵を押すと PDF で全部を見られます。</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
