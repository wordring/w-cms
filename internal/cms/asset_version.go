package cms

// ─────────────────────────────────────────────────────────────────────────
// 画面のファイル（/assets/ の JS・CSS）に版の印を添える（2026-10-27・【考察】キャッシュ制御 案B）
//
// 利用者:「インターネット公開のサイトを作る時に困るので、キャッシュ制御を作り込みましょう」
// →「【考察】キャッシュ制御の勧める順で作ってください。公開の添付は10分にとどめる（勧め）でお願いします。」
//
// それまでは、ページを開くたびに画面のファイル十数本を1本ずつ「変わったか」とたずねていました（`no-cache`・変わっていなければ
// 304）。社外から VPS を通して開くと、その往復のぶん遅れます。
//
//   - 殻（編集用の index.html・公開用の public.html）と、/assets/ の HTML のページ（管理画面など）が読む JS・CSS の住所に、
//     `?v=<版の印>` を添えます（StampAssetURLs）。印は、ファイルを書いた時刻と大きさから作ります（assetVersion）。
//   - 住所の印が今の印と合えば、1年たずねずに使ってよい（`public, max-age=31536000, immutable`）。だれが読んでも同じ中身なので
//     public です。合わない・無い住所は、いままでどおり毎回たずねる（`no-cache`）。
//   - 入れ替えでファイルが変わると印が変わり、殻が新しい住所を指すので、読み直せばすぐ新しいファイルが届きます。
//   - ⚠ 印を添えた HTML のページ自身は、古いまま使わせてはいけません（古い印の住所を指し続け、古い JS を1年使われる）。
//     殻は no-store（ログインした人）か ETag つき（公開ページ）、/assets/ の HTML のページは中身の指紋の ETag で毎回確かめさせます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// assetDir は /assets/ の中身の置き場です（作業の場所の assets/——cmd/w-cms の FileServer と同じ）。
const assetDir = "assets"

// assetDiskPath は、画面のファイルの住所（/assets/…）のディスクの場所を返します。拡張の画面のファイル（/assets/ext/…）は
// 拾った名簿から引きます。外へ出る住所（..）・無いファイルは false。
func assetDiskPath(urlPath string) (string, bool) {
	p := path.Clean(urlPath)
	// 逆スラッシュは断る——Windows（家の開発機）では区切りとして読まれ、`..\` で assets/ の外の HTML を読めた
	// （2026-10-27 の見直し・http.Dir も同じ理由で断る）。
	if !strings.HasPrefix(p, "/assets/") || strings.ContainsRune(p, '\\') {
		return "", false
	}
	if strings.HasPrefix(p, ExtScriptPrefix) {
		a, ok := discoverExtAssets()[p]
		return a.Path, ok
	}
	return filepath.Join(assetDir, filepath.FromSlash(strings.TrimPrefix(p, "/assets/"))), true
}

// assetVersion は画面のファイルの版の印です（書いた時刻のミリ秒と大きさ・36進）。
func assetVersion(fi os.FileInfo) string {
	return strconv.FormatInt(fi.ModTime().UnixMilli(), 36) + strconv.FormatInt(fi.Size(), 36)
}

// assetVersionOf は住所のファイルの今の版の印です（無ければ false）。
func assetVersionOf(urlPath string) (string, bool) {
	p, ok := assetDiskPath(urlPath)
	if !ok {
		return "", false
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return assetVersion(fi), true
}

// assetRefRe は、HTML の中の画面のファイル（JS・CSS）を指す src・href です（問い合わせの付いていないものだけ）。
var assetRefRe = regexp.MustCompile(`(\s(?:src|href)=")(/assets/[^"?#]+\.(?:js|css))"`)

// StampAssetURLs は、HTML の中の画面のファイルの住所に今の版の印を添えます。無いファイルの住所は、そのまま。
func StampAssetURLs(htmlStr string) string {
	return assetRefRe.ReplaceAllStringFunc(htmlStr, func(m string) string {
		sub := assetRefRe.FindStringSubmatch(m)
		v, ok := assetVersionOf(sub[2])
		if !ok {
			return m
		}
		return sub[1] + sub[2] + "?v=" + v + `"`
	})
}

// SetAssetCache は、画面のファイルの応答に、住所の印に合わせた Cache-Control を付けます。
func SetAssetCache(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("v"); v != "" {
		if now, ok := assetVersionOf(r.URL.Path); ok && v == now {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// ServeStampedHTML は、ディスクの HTML のページ（/assets/ の管理画面・拡張の画面など）を、画面のファイルの住所に印を添えて
// 配ります。中身の指紋を ETag にして毎回確かめさせます（変わっていなければ 304）——ページのファイルが同じでも、読むファイルの
// 印が変われば中身が変わるので、書いた時刻（Last-Modified）では確かめさせません。
func ServeStampedHTML(w http.ResponseWriter, r *http.Request, diskPath string) {
	b, err := os.ReadFile(diskPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out := StampAssetURLs(string(b))
	sum := sha256.Sum256([]byte(out))
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, "", time.Time{}, strings.NewReader(out))
}

// AssetsHandler は /assets/ を配ります（files は /assets/ を剥がしたあとの FileServer）。HTML のページは印を添えて
// ServeStampedHTML で、ほかは files で配り、住所の印に合わせて Cache-Control を付けます。
func AssetsHandler(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(path.Ext(r.URL.Path), ".html") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if p, ok := assetDiskPath(r.URL.Path); ok {
				if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
					ServeStampedHTML(w, r, p)
					return
				}
			}
		}
		SetAssetCache(w, r)
		files.ServeHTTP(w, r)
	})
}
