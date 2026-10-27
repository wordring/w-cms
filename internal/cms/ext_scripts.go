package cms

// ─────────────────────────────────────────────────────────────────────────
// 拡張の画面のファイル（JS・CSS・HTML）（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md）
//
// 拡張の画面の働きと見た目は、その拡張のフォルダの `assets/` に置きます（例: `ext/comm/assets/comm.js`・`ext/toho/assets/toho.css`）。
// **置き場の決まりで拾います**——拡張は `init()` で `RegisterExtension` を呼ぶだけでよく、ファイルを1本ずつ名乗りません
// （2026-10-20 利用者:「できれば、拡張をフォルダに入れてビルドすると入るような仕組みが良いです」）。
//
//   - 拡張の名前はパッケージの道と揃えてあるので（`comm/mail` なら `ext/comm/mail`）、載っている拡張ごとに
//     `ext/<名前>/assets/` を見ます。拾うのは `.js`・`.css`・`.html` の3種類だけで、`/assets/ext/<名前>/<ファイル>` として配ります。
//     その下のフォルダ（1段だけ・例: `assets/pages/`）のファイルは、配るだけで殻へは差し込みません（拡張の別の画面のため）。
//   - 編集用の殻には、`.js` を `<script src>`（WCMS_EXT_SCRIPTS の印・コアの app.js のあと）、`.css` を `<link>`（WCMS_EXT_STYLES の印・
//     コアの app.css のあと）で差し込みます。`.html` は、その拡張の画面として配るだけです（例: メールの一覧）。
//   - **コアの JS は拡張の JS を名指ししません**——拡張の JS は、コアの app.js が渡す `window.wcms` だけを使います。
//   - ビルドで外した拡張は名簿に載らないので、そのファイルも配られません（サーバーの口は無いのにボタンだけ残る、が起きない）。
//   - `assets/` と同じく**ディスクから**配ります（入れ替えなしで直せる・`no-cache`）。拾うのは起動して最初に使うとき1度だけ
//     （ファイルを足したら起動し直す）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// extAsset は拾った拡張の画面のファイル1本です。
type extAsset struct {
	URL  string // 配るアドレス（/assets/ext/<拡張>/<ファイル>）
	Path string // ディスクの場所
	Type string // 配るときの Content-Type
	// PageOnly は、assets/ の下のフォルダ（例: pages/）のファイルです。配るだけで、殻へは差し込みません。
	PageOnly bool
}

// ExtScriptPrefix は拡張の画面のファイルを配るアドレスの頭です。
const ExtScriptPrefix = "/assets/ext/"

// extScriptsPlaceholder・extStylesPlaceholder は、編集用の殻に拡張の `<script>`・`<link>` を差し込む印です。
const (
	extScriptsPlaceholder = "<!--WCMS_EXT_SCRIPTS-->"
	extStylesPlaceholder  = "<!--WCMS_EXT_STYLES-->"
)

// extAssetTypes は拾うファイルの種類です（ほかは置いてあっても配らない）。
var extAssetTypes = map[string]string{
	".js":   "application/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8",
}

var (
	// extAssetRoot は拡張のフォルダを探す根です（ふだんはリポジトリの根＝作業の場所）。試験だけが SetExtAssetRoot で替える。
	extAssetRoot = "."
	extAssetMu   sync.Mutex
	extAssets    map[string]extAsset // 配るアドレス → ファイル（拾うのは1度だけ）
)

// SetExtAssetRoot は拡張のフォルダを探す根を替え、拾い直させます（試験だけ——パッケージの試験は自分のフォルダで走るため）。
func SetExtAssetRoot(root string) {
	extAssetMu.Lock()
	defer extAssetMu.Unlock()
	extAssetRoot = root
	extAssets = nil
}

// discoverExtAssets は、載っている拡張ごとに `ext/<名前>/assets/` の JS・CSS・HTML を拾います。
func discoverExtAssets() map[string]extAsset {
	extAssetMu.Lock()
	defer extAssetMu.Unlock()
	if extAssets != nil {
		return extAssets
	}
	out := map[string]extAsset{}
	for _, e := range Extensions() {
		if e.ID == "" || strings.Contains(e.ID, "..") {
			continue
		}
		dir := filepath.Join(extAssetRoot, "ext", filepath.FromSlash(e.ID), "assets")
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue // 画面のファイルを持たない拡張
		}
		for _, f := range ents {
			if f.IsDir() {
				// 下のフォルダ（例: pages/）のファイルは、配るだけで殻へは差し込まない——拡張の別の画面（メールの一覧など）の
				// 見た目と働きで、殻に入れると編集の画面まで変わるため（2026-10-20）。1段だけ見る。
				sub := filepath.Join(dir, f.Name())
				subEnts, err := os.ReadDir(sub)
				if err != nil {
					continue
				}
				for _, g := range subEnts {
					typ, ok := extAssetTypes[strings.ToLower(filepath.Ext(g.Name()))]
					if g.IsDir() || !ok {
						continue
					}
					url := ExtScriptPrefix + e.ID + "/" + f.Name() + "/" + g.Name()
					out[url] = extAsset{URL: url, Path: filepath.Join(sub, g.Name()), Type: typ, PageOnly: true}
				}
				continue
			}
			typ, ok := extAssetTypes[strings.ToLower(filepath.Ext(f.Name()))]
			if !ok {
				continue
			}
			url := ExtScriptPrefix + e.ID + "/" + f.Name()
			out[url] = extAsset{URL: url, Path: filepath.Join(dir, f.Name()), Type: typ}
		}
	}
	extAssets = out
	return out
}

// extAssetURLs は、拾ったファイルのうち拡張子 ext のものの配るアドレスを順に返します（殻に差し込む順＝拡張の名前・ファイルの名前の順）。
func extAssetURLs(ext string) []string {
	var out []string
	for u, a := range discoverExtAssets() {
		if !a.PageOnly && strings.EqualFold(path.Ext(u), ext) {
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

// ExtScriptURLs は拡張の JS の配るアドレスを順に返します。
func ExtScriptURLs() []string { return extAssetURLs(".js") }

// ExtStyleURLs は拡張の CSS の配るアドレスを順に返します。
func ExtStyleURLs() []string { return extAssetURLs(".css") }

// extScriptTags・extStyleTags は殻へ差し込む `<script>`・`<link>` の並びです。
func extScriptTags() string {
	var b strings.Builder
	for _, u := range ExtScriptURLs() {
		b.WriteString(`<script src="` + stdhtml.EscapeString(u) + `"></script>`)
	}
	return b.String()
}

func extStyleTags() string {
	var b strings.Builder
	for _, u := range ExtStyleURLs() {
		b.WriteString(`<link rel="stylesheet" href="` + stdhtml.EscapeString(u) + `">`)
	}
	return b.String()
}

// ExtScriptHandler は GET /assets/ext/… です。拾ったファイルだけを、ディスクから配ります（ほかは 404）。
func ExtScriptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a, ok := discoverExtAssets()[path.Clean(r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if path.Ext(a.URL) == ".html" {
		ServeStampedHTML(w, r, a.Path) // 拡張の画面（メールの一覧など）——読むファイルの住所に版の印を添える（asset_version.go）
		return
	}
	SetAssetCache(w, r) // assets/ と同じ（印が合えば1年・合わなければ使う前に毎回確かめる——asset_version.go）
	w.Header().Set("Content-Type", a.Type)
	http.ServeFile(w, r, a.Path)
}
