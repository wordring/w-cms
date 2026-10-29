package cms

// ─────────────────────────────────────────────────────────────────────────
// 表示用の写真（2026-10-27）
//
// 利用者:「一般的にウェブサイトの画像はどの程度の大きさで表示されるでしょうか？その情報とw-cmsの画像表示の大きさから、
// ページを読み込んだとき最初に表示される画像の大きさを決めて、帯域をさらに節約できないでしょうか？クリックして大きな画像を
// 表示するときのために、大きな画像も必要と思われますが、二重にするとストレージを圧迫しますか？」
// →「表示用の写真（長い辺 1200 px・押すと元の写真）と、見えるまで読まない設定の両方を作ってください。」
// 利用者:「見えるまで読まないのがストレスになるようなら後から外します」——設定 `photo_view` の `lazy` で切り替える。
//
//   - 本文の写真は、文書の欄（パソコンで 666 px まで）かスマホの幅（360〜430 px・画素の細かさ 2〜3 倍）で出るので、
//     長い辺 1200 px あれば足ります。本物の写真30枚で測ると、いまの写真（長い辺 2000 px・平均 434 KB）が 177 KB（4割）。
//   - 本文の JPEG の写真の住所は `/<ページ>/<保存名>?view=1&v=<版の印>`（attachment_cache.go の RenderAttachmentVersions）。
//     この住所は、表示用に縮めた写しを配ります（ServePhotoView）。拡大表示（app.js の lightbox）は `view=1` を外して元の写真を出す。
//   - 写しは、初めて求められたときに作り、データの置き場の cache/photo-view/ に、元の写真の版の印ごとに貯めます。
//     元の写真を上書きすると印が変わって作り直し、古い写しは消します。夜の控えには入りません（消しても作り直す）。
//     写しの合計は、いまの写真の4割ほど（本番で約 0.5 GB）。
//   - 元が長い辺 1200 px 以下の写真は、縮めずに元の写真を配ります（写しを作らない）。
//   - 読む権限と手元への残し方は、元の写真と同じです（page.SetAttachmentCache）。
//   - `view=1` と `v=` は、保存の手前で外します（StripAttachmentVersions・app.js の serializeAttrs）——本文には残らない。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PhotoViewSettings は設定 `photo_view` です。
type PhotoViewSettings struct {
	// LongSide は表示用の写しの長い辺の画素です。
	LongSide int `json:"long_side"`
	// Quality は写しの JPEG の品質です。
	Quality int `json:"quality"`
	// Lazy は、本文の写真を、画面に近づくまで読まない（loading="lazy"）かです。
	Lazy bool `json:"lazy"`
}

func validatePhotoView(path string, c *PhotoViewSettings) error {
	if c == nil {
		return nil
	}
	if c.LongSide < 600 || c.LongSide > maxImageSide {
		return fmt.Errorf("%s: photo_view の long_side は 600 から %d のあいだで書いてください", path, maxImageSide)
	}
	if c.Quality < 50 || c.Quality > 95 {
		return fmt.Errorf("%s: photo_view の quality は 50 から 95 のあいだで書いてください", path)
	}
	return nil
}

// PhotoViewConfig は設定 `photo_view` を返します（無ければ nil＝表示用の写しを使わない）。
func PhotoViewConfig() *PhotoViewSettings { return settingsField(func(s *Settings) *PhotoViewSettings { return s.PhotoView }) }

// isViewablePhoto は、表示用の写しを作る写真（JPEG）の名前かです。
func isViewablePhoto(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return true
	}
	return false
}

var (
	photoViewMu      sync.Mutex
	photoViewRunning = map[string]chan struct{}{} // 作っている写し → 作り終えたら閉じる
	photoViewSlots   = make(chan struct{}, 2)     // 同時に縮めるのは2枚まで
)

// photoViewPath は表示用の写しの置き場です（元の写真の版の印ごと）。sameMark は、元が小さくて縮めなかった印のファイルです。
func photoViewPath(pageID, name, ver string) (img, sameMark string) {
	base := database.DataPath("cache", "photo-view", pageID, name+"."+ver)
	return base + ".jpg", base + ".same"
}

// ensurePhotoView は表示用の写しを用意し、配るファイルの場所を返します（元が小さければ元の写真の場所）。
func ensurePhotoView(conf PhotoViewSettings, pageID, name, origPath, ver string) (string, error) {
	img, same := photoViewPath(pageID, name, ver)
	if _, err := os.Stat(img); err == nil {
		return img, nil
	}
	if _, err := os.Stat(same); err == nil {
		return origPath, nil
	}
	photoViewMu.Lock()
	done, running := photoViewRunning[img]
	if running {
		photoViewMu.Unlock()
		<-done
		return ensurePhotoViewDone(img, same, origPath)
	}
	done = make(chan struct{})
	photoViewRunning[img] = done
	photoViewMu.Unlock()
	defer func() {
		photoViewMu.Lock()
		delete(photoViewRunning, img)
		photoViewMu.Unlock()
		close(done)
	}()

	photoViewSlots <- struct{}{}
	defer func() { <-photoViewSlots }()
	content, err := os.ReadFile(origPath)
	if err != nil {
		return "", err
	}
	out, _, _, resized, err := reencodeJPEG(content, conf.LongSide, conf.Quality)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(img), 0o755); err != nil {
		return "", err
	}
	// 同じ写真の古い版の写しを消す。⚠ 縮めているあいだに元が上書きされていたら、自分が古い版なので、ほかを消さない（2026-10-27 の見直し）。
	if fi, err := os.Stat(origPath); err != nil || page.AttachmentVersion(fi) != ver {
		// 消さずに進む（自分の写しは、新しい版の写しが作られたときに消える）。
	} else if ents, err := os.ReadDir(filepath.Dir(img)); err == nil {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), name+".") && !strings.HasPrefix(e.Name(), name+"."+ver+".") {
				os.Remove(filepath.Join(filepath.Dir(img), e.Name()))
			}
		}
	}
	if !resized || len(out) >= len(content) {
		// 元が小さい（縮めても軽くならない）——元の写真を配る。
		if err := page.WriteFileAtomic(same, []byte{}, 0o644); err != nil {
			return "", err
		}
		return origPath, nil
	}
	if err := page.WriteFileAtomic(img, out, 0o644); err != nil {
		return "", err
	}
	return img, nil
}

func ensurePhotoViewDone(img, same, origPath string) (string, error) {
	if _, err := os.Stat(img); err == nil {
		return img, nil
	}
	if _, err := os.Stat(same); err == nil {
		return origPath, nil
	}
	return "", fmt.Errorf("表示用の写しを作れませんでした")
}

// ServePhotoView は表示用の写真（/<ページ>/<保存名>?view=1）を配ります。読む権限と手元への残し方は元の写真と同じ。
// 設定が無い・JPEG でない・作れないときは、元の写真を配ります（見えないよりはよい）。
func ServePhotoView(w http.ResponseWriter, r *http.Request, pageID, name string, notFound http.HandlerFunc) {
	conf := PhotoViewConfig()
	if conf == nil || !isViewablePhoto(name) || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		page.ServeCleanAttachment(w, r, pageID, name, notFound)
		return
	}
	if !page.RequirePageReadOrPublic(w, r, pageID) {
		return
	}
	if !page.AnonymousMayFetch(r, pageID, name) { // ログインしていない人には、本文が指す添付だけ（2026-10-27）
		notFound(w, r)
		return
	}
	orig, ok := page.AttachmentPath(pageID, name)
	if !ok {
		notFound(w, r)
		return
	}
	fi, err := os.Stat(orig)
	if err != nil || !fi.Mode().IsRegular() {
		notFound(w, r)
		return
	}
	p, err := ensurePhotoView(*conf, pageID, name, orig, page.AttachmentVersion(fi))
	if err != nil {
		page.ServeCleanAttachment(w, r, pageID, name, notFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	page.SetAttachmentCache(w, r, fi)
	http.ServeFile(w, r, p)
}
