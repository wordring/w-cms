package cms

// ─────────────────────────────────────────────────────────────────────────
// 参照が指すファイルを、その場で開く（`section[data-type="file-view" data-ref]`）
//
// ユーザー:「PDFを表示するタグが必要に思います」「表示するという意図を伝える名前が
// 良いと思います。ほかに編集するという意図も出てくると思います」（2026-09-14）。
// 「embedタグと同じようにリソースを指定して表示するだけのタグにしてはどうでしょう」
// 「PDFに限らず表示できるほうが良い」（2026-09-15）。
//
// **本文に在るのはマーカー1つだけ**です。中身はページを返すたびにここで描き、
// `.vocab-chrome` に包むのでシリアライザは保存しません（計算ビューと同じ流儀）。
//
//	<section data-type="file-view" data-ref="010272-c3p7"></section>
//
// ── なぜ配線を属性にしたか（2026-09-15 に作り直した理由）──
//
// 初めは中に参照タグ（`<dl data-type="tags"><dt>受信元</dt>…`）を1つ書かせ、
// **最初の `dl` の最初の参照**を読んでいました。3つまずいところがありました:
//
//	① **同じ値が2つの意味で並ぶ**。解析が書く本文では、図面ブロックの `受信元`
//	   （＝どのメールで届いたかの記録＝実データ）と、表示先を指す配線とが、
//	   同じ `010272-c3p7` として2行出ていました。
//	② **タグの名前に意味が無い**。`受信元` でも `あ` でも同じに動くので、
//	   読む人はどちらが配線か見分けられません。
//	③ **当てずっぽう**。「最初の1つ」なので、2つ書くと2つ目が黙って無視されます。
//
// 配線を属性へ出すと、3つとも消えます。リンクのプロパティ欄と同じ形——
// **値（見える文字）は本文、配線（見えないもの）は属性**で、属性はプロパティ欄が
// 書きます（ユーザー 2026-08-31:「編集するのにダイアログが出るのは使いにくかった。
// プロパティ欄があるものが使いやすかった」）。
//
// ── なぜURLではなく参照IDなのか ──
//
// `data-src="/010272/c3p7.pdf"` と書く案もありました（`<embed src>` に一番近く、
// サニタイザの許可も検査も既にある）。採らなかったのは、**URLが派生**だからです
// ——ページIDと添付IDと拡張子から組めるもので、配信のアドレスは一度変わっています
// （`/data/` → きれいなURL・2026-08-31「実際に保存される場所を推測されたくない」）。
// 本文は正本なので、次に変えた日に全部が古くなる書き方は選びません。
//
// ── なぜマーカーなのか（`<embed>` を本文に書けない理由）──
//
// `embed`・`object`・`iframe` はサニタイザが**部分木ごと消します**。危ないのは
// 要素そのものではなく、**`src` を書いた人が決めること**です——本文へ
// `<img src="/api/logout">` を1つ保存すると全員が無音でログアウトした、という
// 事故が実際に起きています（2026-08-21）。
//
// マーカー方式では宛先が機械の手に戻ります。書く人が選べるのは**ページIDと添付ID**
// だけで、URLはここが組みます。しかも配信は同じ認可付きの口を通るので
// （`page.CanView`）、**読めないページのファイルは出ません**。
// だから「`<embed>` を許す」のとは別の話になります（docs/旧文書/セキュリティ設計.md §4）。
//
// ── なぜ「表示できる形式」の名前にしないのか ──
//
// **どの形式でも受けます**（2026-09-15）。ブラウザが素で描けるものはその場に開き、
// 描けないもの（DXF・Excel・ZIP など）は**開く口を出します**——「見つかりません」と
// 言って黙るのは嘘でした。ファイルは在るのですから。
//
// 名前を `pdf-view` にしないのは、**同じ場所に別の意図が並ぶ**からです——
// 「編集する」（`file-edit`・WebDAV でローカルアプリへ渡す）が控えており、
// `pdf-edit` では PDF を編集する意味になってしまいます。対象（file）と意図（view）で
// 名づけると、形式が増えても名前が変わりません。
//
// ── 鏡型にした理由 ──
//
// 計算ビュー（`View: true`）は**要素を見ずにページIDだけで描く**ので、この形式では
// 使えません——`data-ref` は要素ごとに違うためです。鏡型（`RegisterMirror`）は
// 要素を受け取るので、1ページに何枚並べてもそれぞれが自分の参照を開きます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/database"
	"w-cms/internal/cms/page"
)

// FileViewType は「ここにファイルを開く」マーカーの形式名です。
const FileViewType = "file-view"

// FileRefAttr は開くファイルを指す配線の属性名です（`ページID-添付ID`）。
//
// **サニタイザの許可と対**です（htmldoc の structuralElements で `section` に限って
// 通しています）。名前をここで定数にしているのは、書き手（拡張）・読み手（この鏡）・
// エディタの3者が同じ綴りを見るためです。
const FileRefAttr = "data-ref"

func init() {
	RegisterVocab(VocabDef{
		Type:        FileViewType,
		DisplayName: "ファイル表示",
		Category:    "共通",
		Icon:        "📄",
		Element:     "section",
		// **列はありません**——配線は属性で、中に書くものがないためです。
		// スラッシュメニューが挿すのは空の `<section data-type="file-view">` で、
		// エディタがその場に配線の札を出します（assets/app.js の `decorateFileViews`。
		// 札を押すと `openFileViewPopover` が欄を開き、`applyFileViewRef` が書き戻す）。
		// ⚠ エディタ側では `usesHeadingForm` の例外に入れておくこと——見出し形で挿すと
		// `data-type` が付かず、札の絞り込みから外れます。
	})
	RegisterMirror(FileViewType, MirrorHandlerFunc(renderFileView))
}

// renderFileView はマーカーの `data-ref` を読み、指す先のファイルを開く枠を足します。
//
// ⚠ **中の文字は名札で、表示では消します**（2026-09-27 利用者:「保存時は
// `<section data-type="file-view" data-ref="…">短縮ファイル名</section>` としておいて、表示する
// ときに消せば良いのでは？」）。名札は保存のたびにサーバーが書き直します（`FillFileViewNames`）
// ——編集モードで保存すると中は一度空になりますが、サーバーがまた書き込みます。
func renderFileView(ctx *MirrorContext, el *html.Node) (bool, error) {
	// 名札と前回のクロームを落とす（毎回描き直す）。
	for el.FirstChild != nil {
		el.RemoveChild(el.FirstChild)
	}

	ref := strings.TrimSpace(Attr(el, FileRefAttr))
	if ref == "" {
		appendChrome(el, `<p class="view-error">`+
			`ファイル表示に参照がありません（編集モードでこの枠を選び、`+
			`「参照」の欄へ「ページID-添付ID」を貼ってください）。</p>`)
		return false, nil
	}
	refPage, blockID, okRef := parseRefValue(ref)
	if !okRef || blockID == "" {
		// ページ全体への参照（`010272`）はファイルを指しません。
		appendChrome(el, `<p class="view-error">`+
			stdhtml.EscapeString(ref)+
			` はファイルの参照ではありません（「ページID-添付ID」の形で書いてください）。</p>`)
		return false, nil
	}

	url, fileName, kind, ok := attachmentURLFor(ctx.Viewer, refPage, blockID)
	if !ok {
		// **理由を出します。** 無言の空白にすると、消されたのか書き損じたのか
		// 分かりません（`missingViewHTML` と同じ流儀）。
		appendChrome(el, `<p class="view-error">`+
			stdhtml.EscapeString(ref)+
			` のファイルが見つかりません（消された・読む権限が無い・`+
			`ZIPの中のファイルなど）。</p>`)
		return false, nil
	}

	appendChrome(el, fileViewInnerHTML(refPage, blockID, fileName, url, kind))
	// 中身はサーバーの所有物なので、その先へは配らない。
	return false, nil
}

// fileViewKind は開き方の種別です（拡張子から決まります）。
type fileViewKind string

const (
	kindPDF   fileViewKind = "pdf"
	kindImage fileViewKind = "image"
	kindVideo fileViewKind = "video"
	kindAudio fileViewKind = "audio"
	// kindOther は**ブラウザでは描けないもの**です。枠の代わりに開く口を出します。
	kindOther fileViewKind = "other"
)

// fileViewKinds は拡張子 → 開き方です。
//
// **ここに無い形式も受けます**（`kindOther`）——CADもExcelもZIPも、開く口は出します。
//
// ⚠ **この表は配信側（page.setAttachmentHeaders）の帰結であって、独立した判断ではありません。**
// 配信が `inline` で返す形式（PDF・ラスタ画像・SVG）だけが、その場に描けます。
// それ以外は `application/octet-stream`＋`Content-Disposition: attachment`＋`nosniff` で
// 配られるので、ここで「描ける」と宣言しても**ブラウザは描きません**——2026-09-15 に
// `.txt/.csv/.log/.md` を `<embed>` で出す枝を足して、そうなりました（しかもその拡張子は
// 添付として保存できる一覧に無く、届きようもなかった）。**足すときは配信側を先に**。
// 動画・音声はメディア要素が `Content-Disposition` を無視するので鳴る見込みですが、
// 配信側は octet-stream のままで、**実測はしていません**（コードレビュー §2.6）。
//
// 外部の描画ライブラリを連れてくるのは、開発方針 §1（外部依存の最小化）に触れます。
var fileViewKinds = map[string]fileViewKind{
	".pdf":  kindPDF,
	".png":  kindImage,
	".jpg":  kindImage,
	".jpeg": kindImage,
	".webp": kindImage,
	".gif":  kindImage,
	".svg":  kindImage, // `<img>` で出す。`<embed>` だと同一オリジンのスクリプトとして動く余地が生まれる
	".mp4":  kindVideo,
	".m4a":  kindAudio,
	".mp3":  kindAudio,
}

// attachmentURLFor は参照の指す添付のURL・ファイル名・開き方を返します。
//
// **読めない相手には出しません**（見せ分けC案——黙って落ちる、ではなく理由は出す。
// ただし「読めない」と「無い」は区別しません。匿名の404統一と同じ規律）。
func attachmentURLFor(user *auth.User, pageID, blockID string) (url, fileName string, kind fileViewKind, ok bool) {
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		return "", "", "", false
	}
	// 添付は `files/` の中。**中身を読まず、名前だけを見ます**——開くのはブラウザで、
	// ここが要るのは「在るか」と「どう開くか」だけです（探すのは attachment_ref.go と共通）。
	n, found := attachmentFileOf(pageID, blockID)
	if !found {
		return "", "", "", false
	}
	k, known := fileViewKinds[strings.ToLower(filepath.Ext(n))]
	if !known {
		k = kindOther // 在るが、ブラウザには描けない形式（DXF・Excel・ZIP など）
	}
	// 見出しに出すのは**届いたときの名前**（目録があれば。無ければ保存名）——2026-09-17。
	return "/" + pageID + "/" + n, AttachmentDisplayName(pageID, n), k, true
}

// fileViewInnerHTML は枠のHTMLを組みます。**サニタイズの後に足すので、自前で
// エスケープの責任を負います**（RenderComputedViews と同じ規律）。
func fileViewInnerHTML(pageID, blockID, fileName, url string, kind fileViewKind) string {
	// 出どころの1行。押すと元の添付のあるページへ飛びます。
	ref := stdhtml.EscapeString(pageID + "-" + blockID)
	name := stdhtml.EscapeString(fileName)
	src := stdhtml.EscapeString(url)

	if kind == kindOther {
		// **描けないものは、開く口を出します。** ここで「見つかりません」と言うのは
		// 嘘でした——ファイルは在るのですから（2026-09-15）。
		// ⚠ **文は短く1行に**（2026-09-30 利用者:「この形式はブラウザでは開けません。押すと保存でき、手元のアプリで
		// 開けますという表示は冗長すぎます」）——説明はマウスを載せたときに（title）、出どころは括弧で添えるだけ。
		// ⚠ ID（`ref`）は**画面が「🔗 ID」の札にして出します**（2026-10-01・app.js の decorateLocalEdit——押すと
		// 写せる）。ここに ID の文字も出すと同じ値が2つ並ぶので、出どころへのリンクは「出どころ」の語にした。
		return `<div class="file-view file-view-plain">` +
			`<p class="file-view-head" title="押すと保存します（ブラウザでは開けない形式）">📎 <a href="` + src + `">` + name + `</a>` +
			` <span class="file-view-note">（<a href="/` + stdhtml.EscapeString(pageID) + `#` +
			stdhtml.EscapeString(blockID) + `" title="` + ref + ` の出どころのページへ">出どころ</a>）</span></p></div>`
	}

	// **頭の行を押すと閉じられます**（2026-09-27 利用者:「この中の表示は閉じることが出来るように
	// したいです」）——`<details>` の `<summary>` にして、最初は開いた状態。JS は要りません。
	// 枠（`.file-view`）の外側を包むのは、枠が高さを持つ縦並びだからです（枠そのものを
	// `<details>` にすると、閉じても高さが残ります）。
	// ⚠ 頭の行は**ファイル名だけ**（2026-10-01 利用者:「を開いていますは冗長です。ファイル名だけで分かります」）。
	head := `<summary class="file-view-head">📄 <a href="/` + stdhtml.EscapeString(pageID) +
		`#` + stdhtml.EscapeString(blockID) + `">` + name + `</a></summary>`

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
		body = `<img class="file-view-body" src="` + src + `" alt="` + name + `">`
	case kindVideo:
		body = `<video class="file-view-body" src="` + src + `" controls preload="metadata"></video>`
	case kindAudio:
		body = `<audio class="file-view-body" src="` + src + `" controls preload="metadata"></audio>`
	}
	return `<details class="file-view-fold" open>` + head +
		`<div class="file-view" title="右下をつまむと大きさを変えられます">` + body + `</div></details>`
}

// FillFileViewNames は、本文のファイル表示の印の中へ、指す添付の**短縮ファイル名**を書き込みます
// （保存のとき・2026-09-27）。保存される HTML を読めば、何を表示しているかが分かるように——
// `<section data-type="file-view" data-ref="000008-nyd3">A100-B01-001.pdf</section>`。
//
// ⚠ **名前は保存した人の権限で引きます**——読めないページの添付の名前を本文に書き込むと、
// ファイル名（顧客名が入ることがある）が漏れます。引けなければ、中身には触りません
// （別の人が書いた名札を消さない）。表示のときは `renderFileView` がこの名札を消します。
func FillFileViewNames(user *auth.User, bodyHTML string) string {
	if user == nil || !strings.Contains(bodyHTML, `data-type="`+FileViewType+`"`) {
		return bodyHTML
	}
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}
	changed := false
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if el.Data != "section" || Attr(el, "data-type") != FileViewType {
				return
			}
			refPage, blockID, ok := parseRefValue(strings.TrimSpace(Attr(el, FileRefAttr)))
			if !ok || blockID == "" {
				return
			}
			_, name, _, ok := attachmentURLFor(user, refPage, blockID)
			if !ok || name == "" {
				return
			}
			if el.FirstChild != nil && el.FirstChild == el.LastChild &&
				el.FirstChild.Type == html.TextNode && el.FirstChild.Data == name {
				return // 既に同じ名札
			}
			for el.FirstChild != nil {
				el.RemoveChild(el.FirstChild)
			}
			el.AppendChild(&html.Node{Type: html.TextNode, Data: name})
			changed = true
		})
	}
	if !changed {
		return bodyHTML
	}
	return htmldoc.Render(nodes)
}

// fillFileViewNamesAs は、利用者名しか持たない書き手（機械が書く道）のための入口です。
func fillFileViewNamesAs(username, bodyHTML string) string {
	// 印の無い本文では利用者を引きません（ほとんどの書き込みはこちら）。利用者の DB が
	// 開いていない（試験など）ときも名札は書きません——名札は読む人のための飾りで、
	// 無くても表示は目録から引くので壊れません。
	if !strings.Contains(bodyHTML, `data-type="`+FileViewType+`"`) || database.AuthDB == nil {
		return bodyHTML
	}
	u, err := auth.LookupUser(username)
	if err != nil || u == nil {
		return bodyHTML
	}
	return FillFileViewNames(u, bodyHTML)
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
