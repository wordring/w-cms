package cms

// ─────────────────────────────────────────────────────────────────────────
// 拡張の画面の JS（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md）
//
// 拡張の画面の働きは、その拡張のフォルダの JS に置きます（例: `ext/comm/assets/comm.js`）。拡張は `init()` で
// RegisterScript を呼んで名乗り、サーバーは `/assets/ext/<拡張>/<ファイル>` として配り、編集用の殻に
// `<script src>` を差し込みます（WCMS_EXT_SCRIPTS の印の位置・コアの app.js のあと）。
//
//   - **コアの JS は拡張の JS を名指ししません**——拡張の JS は、コアの app.js が渡す `window.wcms` だけを使います。
//   - ビルドで外した拡張は登録しないので、その JS も配られません（サーバーの口は無いのにボタンだけ残る、が起きない）。
//   - `assets/` と同じく**ディスクから**配ります（入れ替えなしで直せる・`no-cache`）。配るのは登録した1本だけで、
//     同じフォルダのほかのファイルは配りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// extScript は登録された拡張の JS 1本です。
type extScript struct {
	URL  string // 配るアドレス（/assets/ext/<拡張>/<ファイル>）
	Path string // ディスクの場所（リポジトリの根から）
}

// extScripts は登録された JS です（配るアドレスが鍵）。**`init()` の中からだけ**登録します。
var extScripts = map[string]extScript{}

// ExtScriptPrefix は拡張の JS を配るアドレスの頭です。
const ExtScriptPrefix = "/assets/ext/"

// extScriptsPlaceholder は、編集用の殻に拡張の `<script>` を差し込む印です。
const extScriptsPlaceholder = "<!--WCMS_EXT_SCRIPTS-->"

// RegisterScript は拡張 extID の画面の JS を登録します（拡張の `init()` から呼ぶ）。file はリポジトリの根からの場所
// （例: `ext/comm/assets/comm.js`）で、`/assets/ext/<extID>/<ファイル名>` として配られます。
// 同じアドレスを2度登録するのは取り付けの誤りなので止めます。
func RegisterScript(extID, file string) {
	name := filepath.Base(file)
	if extID == "" || strings.Contains(extID, "..") || !strings.HasSuffix(name, ".js") {
		panic("拡張の JS の登録が正しくありません: " + extID + " " + file)
	}
	url := ExtScriptPrefix + extID + "/" + name
	if _, dup := extScripts[url]; dup {
		panic("拡張の JS " + url + " が2度登録されました")
	}
	extScripts[url] = extScript{URL: url, Path: filepath.FromSlash(file)}
}

// ExtScriptURLs は登録された JS の配るアドレスを順に返します（殻に差し込む順＝拡張の名前の順）。
func ExtScriptURLs() []string {
	out := make([]string, 0, len(extScripts))
	for u := range extScripts {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// extScriptTags は殻へ差し込む `<script>` の並びです。
func extScriptTags() string {
	var b strings.Builder
	for _, u := range ExtScriptURLs() {
		b.WriteString(`<script src="` + stdhtml.EscapeString(u) + `"></script>`)
	}
	return b.String()
}

// ExtScriptHandler は GET /assets/ext/… です。登録した JS だけを、ディスクから配ります（ほかは 404）。
func ExtScriptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s, ok := extScripts[path.Clean(r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache") // assets/ と同じ（使う前に毎回確かめる・変わっていなければ 304）
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	http.ServeFile(w, r, s.Path)
}
