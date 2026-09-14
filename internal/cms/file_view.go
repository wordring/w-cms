package cms

// ─────────────────────────────────────────────────────────────────────────
// 参照が指すファイルを、その場で開く（`section[data-type="file-view"]`・2026-09-14）
//
// ユーザー:「PDFを表示するタグが必要に思います」「表示するという意図を伝える名前が
// 良いと思います。ほかに編集するという意図も出てくると思います」。
//
// **本文に在るのはマーカーだけ**です。中身はページを返すたびにここで描き、
// `.vocab-chrome` に包むのでシリアライザは保存しません（計算ビューと同じ流儀）。
//
//	<section data-type="file-view">
//	  <dl data-type="tags"><dt>受信元</dt><dd>010272-c3p7</dd></dl>
//	</section>
//
// ── なぜマーカーなのか（`<embed>` を本文に書けない理由）──
//
// `embed`・`object`・`iframe` はサニタイザが**部分木ごと消します**。危ないのは
// 要素そのものではなく、**`src` を書いた人が決めること**です——本文へ
// `<img src="/api/logout">` を1つ保存すると全員が無音でログアウトした、という
// 事故が実際に起きています（2026-08-21）。
//
// マーカー方式では宛先が機械の手に戻ります。ここが組み立てるURLは
// **参照の値から導いた `/<6桁>/<英数字>.<拡張子>`** だけで、書いた人は
// ページIDと添付IDしか選べません。しかも配信は同じ認可付きの口を通るので
// （`page.RequirePageReadOrPublic`）、**読めないページのファイルは出ません**。
// だから「`<embed>` を許す」のとは別の話になります。
//
// ── なぜ「表示できる形式」の名前にしないのか ──
//
// ブラウザに任せて出せる形式のうち、**マーカーが要るのは PDF だけ**です
// （画像・SVG・動画・音声は `<img>`・`<video>`・`<audio>` が素で許可されていて、
// 本文にそのまま書けます）。それでも名前を `pdf-view` にしないのは、
// **同じ場所に別の意図が並ぶ**からです——「編集する」（WebDAV でローカルアプリへ
// 渡す）が控えており、`pdf-edit` では PDF を編集する意味になってしまいます。
// 対象（file）と意図（view）で名づけると、形式が増えても名前が変わりません。
//
// ── 鏡型にした理由 ──
//
// 計算ビュー（`View: true`）は**中身を全部消して描き直す**ので、マーカーの中に
// 書いた参照タグが画面から消えます。それでは「HTMLに在るものが見えている」に
// ならないので、**鏡型**（`RegisterMirror`）にして、人が書いた参照タグは残したまま
// 枠をその下へ足します。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// FileViewType は「ここにファイルを開く」マーカーの形式名です。
const FileViewType = "file-view"

func init() {
	RegisterVocab(VocabDef{
		Type:        FileViewType,
		DisplayName: "ファイル表示",
		Category:    "共通",
		Icon:        "📄",
		Element:     "section",
		// **空の欄を1つ置きます**。スラッシュメニューから挿したときに
		// 「どこへ参照を書くのか」が見えていないと、人は何も書けません
		// （空の `<section>` だけが挿さり、画面には「参照がありません」と出る）。
		// 値は添付の隣の「🔗 参照」で写せます。
		Columns: []VocabColumn{{Label: "ファイル", Type: ColRef}},
	})
	RegisterMirror(FileViewType, MirrorHandlerFunc(renderFileView))
}

// renderFileView はマーカーの中の参照を読み、指す先のファイルを開く枠を足します。
//
// **人が書いた中身は消しません**——消すのは前回描いたクロームだけです。
// 参照タグが画面に残るので、出ている枠が「その参照を開いたもの」だと読めます。
func renderFileView(ctx *MirrorContext, el *html.Node) (bool, error) {
	// 前回のクロームを落とす（毎回描き直す。`class` は保存されないので残骸は出ない）。
	var stale []*html.Node
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if isChrome(c) {
			stale = append(stale, c)
		}
	}
	for _, n := range stale {
		el.RemoveChild(n)
	}

	name, refPage, blockID, ok := firstRefInTags(el)
	if !ok {
		appendChrome(el, `<p class="view-error">`+
			`このファイル表示に参照がありません（可変タグに `+
			`「名前：ページID-添付ID」を1つ書いてください）。</p>`)
		return false, nil
	}

	url, kind, ok := attachmentURLFor(ctx.Viewer, refPage, blockID)
	if !ok {
		// **理由を出します。** 無言の空白にすると、消されたのか書き損じたのか
		// 分かりません（`missingViewHTML` と同じ流儀）。
		appendChrome(el, `<p class="view-error">`+
			stdhtml.EscapeString(name+"："+refPage+"-"+blockID)+
			` のファイルが見つかりません（消された・読む権限が無い・`+
			`ZIPの中のファイルなど）。</p>`)
		return false, nil
	}

	appendChrome(el, fileViewInnerHTML(name, refPage, blockID, url, kind))
	// 中身はサーバーの所有物なので、その先へは配らない。
	return false, nil
}

// firstRefInTags はマーカーの中の最初の参照（名前・ページID・ブロックID）を返します。
//
// **ここは素の `dl` も見ます。** 普段「参照と読むのは可変タグの中だけ」という規律が
// あるのは、本文を丸ごと走査すると番号が参照に化けるからです——実データの発注書番号
// `250401-203` が「6桁-英数字」に当てはまり、薄赤が全件に出ました（2026-09-04）。
// **ここではページ全体を走査しません**。見るのは `file-view` のマーカーの中だけで、
// そこは「ファイルを1つ指す」ためだけに在る場所なので、化ける相手が居ません。
// スラッシュメニューが挿す骨格は素の `dl` なので、見ないと**挿した直後に使えません**。
func firstRefInTags(el *html.Node) (name, pageID, blockID string, ok bool) {
	var found bool
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if found || n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "dl" && !isChrome(n) {
			eachDLPair(n, false, func(key string, dd *html.Node) bool {
				p, b, okRef := parseRefValue(nodeText(dd))
				if !okRef || b == "" {
					return true // ページ全体への参照はファイルを指さない
				}
				name, pageID, blockID, found = key, p, b, true
				return false
			})
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(el)
	return name, pageID, blockID, found
}

// fileViewKind は開き方の種別です（拡張子から決まります）。
type fileViewKind string

const (
	kindPDF   fileViewKind = "pdf"
	kindImage fileViewKind = "image"
	kindVideo fileViewKind = "video"
	kindAudio fileViewKind = "audio"
)

// fileViewKinds は拡張子 → 開き方です。
//
// **ここに無い形式は開けません**（CADもExcelもブラウザは描けない）。増やすときは、
// ブラウザが**素で**描けるかどうかだけを基準にします——外部の描画ライブラリを
// 連れてくると、開発方針 §1（外部依存の最小化）に触れます。
var fileViewKinds = map[string]fileViewKind{
	".pdf":  kindPDF,
	".png":  kindImage,
	".jpg":  kindImage,
	".jpeg": kindImage,
	".webp": kindImage,
	".gif":  kindImage,
	".svg":  kindImage,
	".mp4":  kindVideo,
	".m4a":  kindAudio,
	".mp3":  kindAudio,
}

// attachmentURLFor は参照の指す添付のURLと開き方を返します。
//
// **読めない相手には出しません**（見せ分けC案——黙って落ちる、ではなく理由は出す。
// ただし「読めない」と「無い」は区別しません。匿名の404統一と同じ規律）。
func attachmentURLFor(user *auth.User, pageID, blockID string) (url string, kind fileViewKind, ok bool) {
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		return "", "", false
	}
	// 添付は `files/` の中。**中身を読まず、名前だけを見ます**——開くのはブラウザで、
	// ここが要るのは「在るか」と「どう開くか」だけです。
	dir := filepath.Join(page.GetPageDir(pageID), "files")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if strings.TrimSuffix(n, filepath.Ext(n)) != blockID {
			continue
		}
		k, known := fileViewKinds[ext]
		if !known {
			return "", "", false // 在るが、ブラウザには開けない形式
		}
		return "/" + pageID + "/" + n, k, true
	}
	return "", "", false
}

// fileViewInnerHTML は枠のHTMLを組みます。**サニタイズの後に足すので、自前で
// エスケープの責任を負います**（RenderComputedViews と同じ規律）。
func fileViewInnerHTML(name, pageID, blockID, url string, kind fileViewKind) string {
	// 出どころの1行。押すと元の添付のあるページへ飛びます。
	label := stdhtml.EscapeString(name + "：" + pageID + "-" + blockID)
	head := `<p class="file-view-head">📄 <a href="/` + stdhtml.EscapeString(pageID) +
		`#` + stdhtml.EscapeString(blockID) + `">` + label + `</a> を開いています</p>`

	src := stdhtml.EscapeString(url)
	var body string
	switch kind {
	case kindPDF:
		// **サムネイル欄を閉じ、幅に合わせて開きます**（2026-09-10）。既定の23%だと
		// 左のサムネイル欄と黒い余白が枠の3/4を占め、図面を見に来た人の邪魔になります。
		// **ツールバーは残します**——印刷・ダウンロード・回転はここにしかありません
		// （ユーザー:「印刷ボタンなどが無くなったのですが、べんりなのでふっかつしたい」）。
		body = `<embed class="file-view-body" type="application/pdf" src="` +
			src + `#navpanes=0&amp;view=FitH">`
	case kindImage:
		body = `<img class="file-view-body" src="` + src + `" alt="` + label + `">`
	case kindVideo:
		body = `<video class="file-view-body" src="` + src + `" controls preload="metadata"></video>`
	case kindAudio:
		body = `<audio class="file-view-body" src="` + src + `" controls preload="metadata"></audio>`
	}
	return `<div class="file-view" title="右下をつまむと大きさを変えられます">` + head + body + `</div>`
}

// appendChrome は `.vocab-chrome` に包んだ中身を要素の末尾へ足します。
// **シリアライザは `.vocab-chrome` を保存しません**ので、正本にはマーカーだけが残ります。
func appendChrome(el *html.Node, innerHTML string) {
	nodes, err := htmldoc.ParseFragment(
		`<div class="vocab-chrome" contenteditable="false">` + innerHTML + `</div>`)
	if err != nil {
		return
	}
	for _, n := range nodes {
		el.AppendChild(n)
	}
}
