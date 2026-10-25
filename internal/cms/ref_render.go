package cms

// ─────────────────────────────────────────────────────────────────────────
// 参照タグのリンク描画と宙ぶらりんの印（D-4・D-10。2026-08-31 実装）
//
// 参照は本文の「名前：値」タグで表し、値は **`ページID-ブロックID`**——
// 「URLのリンクみたいな感じ」（docs/旧文書/アーキテクチャとDBスキーマ.md §9.1〜§9.2）。
//
//	原発注書 : 000002-12   →  <dd><a href="/000002#12">000002-12</a></dd>
//
// この描画は **ページを返すときだけ** 行います（RenderAnchors と同じ規律。
// GET /api/load へ入れると、合成した <a> がシリアライザを通って本文として
// 保存されてしまう）。判定も**読むたび**——「先に参照を書いて、あとから参照元を
// 作る場合もあるので、ページを読むたびに背景の更新が必要」（D-10 ユーザー決定）。
// 保存時に焼くと、あとからページを作っても宙ぶらりんの印が残り続けます。
//
// 値の解釈は正規化ではなく**構文解析**です（D-7 決定）:
//   - ページIDは**ゼロ埋め6桁ちょうど**。桁を緩めると「123-45」のような普通の
//     型番・図面番号が参照に化けて、宙ぶらりんの赤が誤爆します。
//   - 区切りのハイフンは種類を畳みます（- ‐ − － ー ｰ。議論で示された実例が
//     半角カタカナの長音記号だったため——§9.1）。先頭のゼロは畳みません。
//   - ブロックIDは英数字（エディタの採番は base36）。
//
// 指す先の判定は**ページの存在だけ**です。ブロックの存在は本文を開かないと
// 分からず（段落など索引に載らないブロックがある）、読むたびに全参照先の本文を
// 開くのは釣り合いません。ページがあればリンク（ブロックが無ければ先頭に着地）、
// 無ければ `ref-missing` の印——「黙って切れるのが一番困る」（D-10）。
//
// サニタイズの**後**にHTMLを足す関数の1つです（RenderComputedViews・RenderAnchors・RenderPublicShell・
// RenderPageShell と同じエスケープ責任）。ノードを組んで html.Render に任せるので、
// テキストも属性値も自動でエスケープされます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// refViewer は、参照のリンクを描くときの「見ている人」です（2026-10-26）。user が nil なら、ログインしていない人
// （公開のページを見に来た社外の人）です。
//
// それまでの描き方は見ている人を知らず、匿名の人にも、公開していないページの番号がリンクか「見つかりません」かで
// 在るかどうかが分かり、メールのアドレスが連絡帳のページの番号へのリンクになった（連絡帳に載っていると分かった）。
type refViewer struct{ user *auth.User }

// canLink は、そのページへのリンクを描いてよいかを返します。ログインしている人には、在るページならリンクにします
// （読めないページでも、踏んだ先の関門が断る——ログインしている人には、在るかどうかは秘密ではない）。
// 匿名の人には、公開しているページだけです。
func (v refViewer) canLink(pageID string) bool {
	if !pageExists(pageID) {
		return false
	}
	if v.user != nil {
		return true
	}
	n, err := strconv.Atoi(pageID)
	return err == nil && page.EffectivePublic(n)
}

// markMissing は、指す先が無いことを知らせる印を付けます。匿名の人には付けません——「無い」と「公開していない」を
// 見分けさせないため（どちらも素の文字のまま）。印を付けたら true。
func (v refViewer) markMissing(n *html.Node, title string) bool {
	if v.user == nil {
		return false
	}
	setAttr(n, "class", "ref-missing")
	setAttr(n, "title", title)
	return true
}

// refValueRe は参照値の文法です（6桁のページID・ハイフン類・英数字のブロックID）。
var refValueRe = regexp.MustCompile(`^([0-9]{6})[-‐−－ーｰ]([0-9A-Za-z]+)$`)

// SplitRefList は参照のセルの値を、空白・「、」・「,」・「，」で区切って並べます（空は落とす・2026-10-19）。
// 2026-10-19 利用者:「資料の置き場をページ内に一つにして、資料一つ一つにいつものIDを付けましょう。ページID-いつものIDで資料を特定し、その連結IDを空白文字や「、」で区切って表の該当セルに羅列しておけば良いのでは？」
// 参照の列は、1つのセルに参照をいくつも並べて書けます（例 `001436-fhea、001436-k2pq`）。
func SplitRefList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '、', ',', '，':
			return true
		}
		return unicode.IsSpace(r)
	})
}

// parseRefValue は参照値を（ページID, ブロックID）へ分解します。文法外は ok=false。
func parseRefValue(s string) (pageID, blockID string, ok bool) {
	m := refValueRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// RenderReferenceLinks は本文中の dl（名前：値）の値のうち、参照の文法に
// 一致するものをリンクへ合成して返します。指す先のページが無ければ
// `ref-missing` の印を付けます（見た目は assets の CSS が担う）。
// 2026-09-20 からは、登録された表の**参照の列**のセルも同じようにリンクにします（linkRefCells）。
// user は見ている人です（nil はログインしていない人——refViewer）。
func RenderReferenceLinks(user *auth.User, bodyHTML string) string {
	v := refViewer{user: user}
	// 早道: dl も table も無ければ参照は無い（普通のページにパースの費用を掛けない）。
	if !strings.Contains(bodyHTML, "<dl") && !strings.Contains(bodyHTML, "<table") {
		return bodyHTML
	}
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}

	changed := false
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			// **可変タグ（dl[data-type="tags"]）だけを見ます。**
			//
			// 参照は「名前：値」の**タグ**で表す、というのが D-4 の決定でした
			// （書く側もそこへ書きます——analyze_pdf.go・intake_eml.go）。
			// 業務文書ブロックのヘッダ（`data-type` を持たない素の dl）まで見ると、
			// **発注書番号が参照に化けます**——実データの `250401-203`
			// （26年06月02日＋連番）は「6桁＋ハイフン＋英数字」にそのまま当てはまり、
			// 「参照先のページ 250401 が見つかりません」の薄赤が全件に出ました
			// （2026-09-04・実メールの取り込みで発覚）。
			//
			// 桁を6に絞っても足りなかった、というのがここの学びです。**値の形だけでは
			// 参照と番号を見分けられない**ので、置き場所（可変タグかどうか）も条件にします。
			if n.Data == "table" {
				if linkRefCells(v, n) {
					changed = true
				}
				return
			}
			if n.Data != "dl" || Attr(n, "data-type") != "tags" || inVocabChrome(n) {
				return
			}
			eachDLPair(n, false, func(key string, dd *html.Node) bool {
				// **メールの相手は、登録してあれば連絡先ページへのリンクにします**
				// （2026-09-13 ユーザー:「表示するときにメールアドレスからアドレス帳の
				// ページへリンクがあると良いと思います」）。
				//
				// **本文は書き換えません**——値は届いたままの `名前 <アドレス>` で、
				// 描画のときだけリンクを被せます。登録・解除がその場で効き、
				// 登録していない相手は素のまま出ます（壊れない）。
				if InferColumnType(key) == ColEmail {
					if linkEmailDD(v, dd) {
						changed = true
					}
					return true
				}
				pageID, blockID, ok := parseRefValue(nodeText(dd))
				if !ok {
					// 形が合わなくても、**名前で宣言されたタグ**ならページ全体への
					// 参照として扱います（親ページID など——2026-10-09 に `返信元` から改名）。
					if pageID, ok = parsePageRef(key, nodeText(dd)); !ok {
						return true
					}
					blockID = ""
				}
				if v.canLink(pageID) {
					linkRefDD(dd, pageID, blockID)
					changed = true
				} else if v.markMissing(dd, "参照先のページ "+pageID+" が見つかりません") {
					// 宙ぶらりん——打ち間違いか、参照先をまだ作っていない。
					// 「先に参照を書く」運用があるので拒否はせず、背景で知らせる。
					changed = true
				}
				return true
			})
		})
	}
	if !changed {
		return bodyHTML
	}
	return htmldoc.Render(nodes)
}

// linkRefList は、参照をいくつも並べたセルを、1つずつのリンク（「、」で区切る）に描き直します。
// 形の合わない値・無いページを指す値があれば、セルに ref-missing の印と、どれかを添えます。
func linkRefList(v refViewer, cell *html.Node, refs []string) bool {
	for cell.FirstChild != nil {
		cell.RemoveChild(cell.FirstChild)
	}
	var bad []string
	for i, r := range refs {
		if i > 0 {
			cell.AppendChild(&html.Node{Type: html.TextNode, Data: "、"})
		}
		pageID, blockID, ok := parseRefValue(r)
		if !ok {
			if m := pageIDOnlyRe.FindStringSubmatch(r); m != nil {
				pageID, blockID, ok = m[1], "", true
			}
		}
		if !ok || !v.canLink(pageID) {
			bad = append(bad, r)
			cell.AppendChild(&html.Node{Type: html.TextNode, Data: r})
			continue
		}
		span := &html.Node{Type: html.ElementNode, Data: "span"}
		span.AppendChild(&html.Node{Type: html.TextNode, Data: r})
		linkRefDD(span, pageID, blockID)
		for span.FirstChild != nil {
			c := span.FirstChild
			span.RemoveChild(c)
			cell.AppendChild(c)
		}
	}
	if len(bad) > 0 {
		v.markMissing(cell, "参照の形でないか、ページが見つからない値があります: "+strings.Join(bad, "、"))
	}
	return true
}

// linkRefDD は dd（表のセルにも使う）の中身を参照リンクへ置き換えます（表示文字は元の値のまま）。
func linkRefDD(dd *html.Node, pageID, blockID string) {
	text := strings.TrimSpace(nodeText(dd))
	for dd.FirstChild != nil {
		dd.RemoveChild(dd.FirstChild)
	}
	a := &html.Node{Type: html.ElementNode, Data: "a"}
	// **ブロックIDが無ければページの先頭へ**（名前で宣言された参照＝ページ全体を指す）。
	href := "/" + pageID
	if blockID != "" {
		href += "#" + blockID
	}
	a.Attr = []html.Attribute{
		{Key: "href", Val: href},
		{Key: "class", Val: "ref-link"},
	}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	dd.AppendChild(a)
}

// ContactResolver は「このアドレスは誰のページか」に答える口です。
// 返すのは（ページID・題・見つかったか）。
// user は見ている人です（ログインしている人だけ——匿名の人には連絡帳のリンクを描かない）。読めない相手は
// 見つからない扱いにすること。
type ContactResolver func(user *auth.User, addr string) (pageID, title string, ok bool)

// contactResolver は登録された解決係です（未登録なら nil）。
var contactResolver ContactResolver

// RegisterContactResolver はアドレス→連絡先ページの解決係を登録します。
//
// **コアはアドレス帳を知りません。** `email` 型のタグを描くとき「このアドレスの
// ページはどれか」を知る必要がありますが、それに答えられるのは**業務側**です
// ——アドレス帳は板金にもメールにも依らない業務の持ち物、という判定が出ています
// （[docs/考察/【考察】アドレス帳の作り直し.md] §5b）。
//
// **登録が無ければリンクにしません**（素のテキストのまま）。`-tags minimal` の
// 素の w-cms は `email` 型のタグを普通に表示するだけで、壊れません。
//
// `RegisterVocab` / `RegisterView` / `RegisterMirror` と同じ形のフックです
// （`RegisterIntake` と `RegisterMailer` も同じ形ですが、2026-09-15 に
// **コアから `ext/comm` へ出ました**——コアのフックの並びには居ません）。**重複登録は panic**——2つの解決係が別々の答えを返すと、同じアドレスが
// 描くたびに違うページへ飛ぶ、という説明のつかない壊れ方をするためです。
func RegisterContactResolver(r ContactResolver) {
	if contactResolver != nil {
		panic("連絡先の解決係が重複しています")
	}
	contactResolver = r
}

// linkEmailDD は `名前 <アドレス>` のアドレス部分を、登録済みの連絡先ページへの
// リンクにします（登録されていなければ何もしません）。
//
// **見える文字は変えません。** `小澤 美智子 <suzuki@…>` の `suzuki@…` だけが押せる
// ようになり、名前はそのまま残ります——届いたままの値が見えていることが、
// 「利用者が、書いた通りに入っていると信じられる」ための条件だからです。
//
// 認可は解決係の中で見ます（読めない相手は見つからない扱い）。
// ここは**匿名でも通る描画経路**なので、利用者はまだ分かりません——
// リンクを踏んだ先で通常の関門が判定します（`linkRefDD` と同じ考え方）。
func linkEmailDD(v refViewer, dd *html.Node) bool {
	if contactResolver == nil {
		return false // アドレス帳を持たない構成——素のまま出す
	}
	if v.user == nil {
		return false // ログインしていない人には、連絡帳に載っているかを見せない（2026-10-26）
	}
	text := strings.TrimSpace(nodeText(dd))
	addr, ok := normalizeEmailTag(text)
	if !ok {
		return false // アドレスが無い（名前だけのヘッダ）
	}
	pageID, title, found := contactResolver(v.user, addr)
	if !found {
		return false // まだアドレス帳に無い——素のまま出す
	}

	// 生の値の中で、アドレスの部分だけを差し替えます。
	at, end := lastIndexFold(text, addr)
	if at < 0 {
		return false
	}
	before, after := text[:at], text[end:]

	for dd.FirstChild != nil {
		dd.RemoveChild(dd.FirstChild)
	}
	if before != "" {
		dd.AppendChild(&html.Node{Type: html.TextNode, Data: before})
	}
	a := &html.Node{Type: html.ElementNode, Data: "a"}
	// 相手のページの題は出さない（title 属性に入れていた）——解決係は見ている人を知らないので、読めない人や匿名の公開
	// ページの閲覧者にも題が見えた（2026-10-22 の見直し）。リンクの先は、踏んだ先の関門が見せ分ける。
	_ = title
	a.Attr = []html.Attribute{
		{Key: "href", Val: "/" + pageID},
		{Key: "class", Val: "ref-link"},
	}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: text[at:end]})
	dd.AppendChild(a)
	if after != "" {
		dd.AppendChild(&html.Node{Type: html.TextNode, Data: after})
	}
	return true
}

// lastIndexFold は、text の中で大小を問わず sub に合う最後の所を、text の中の位置 [at, end) で返します（無ければ -1）。
// ⚠ strings.ToLower(text) の中の位置で text を切らない——小文字にするとバイトの長さが変わる字（Ⱥ など）があり、
// 差出人の名前に混ぜるだけで描くたびに panic した（2026-10-22 の見直し）。
func lastIndexFold(text, sub string) (int, int) {
	n := utf8.RuneCountInString(sub)
	for i := len(text) - 1; i >= 0; i-- {
		if !utf8.RuneStart(text[i]) {
			continue
		}
		j, k := i, 0
		for j < len(text) && k < n {
			_, w := utf8.DecodeRuneInString(text[j:])
			j += w
			k++
		}
		if k == n && strings.EqualFold(text[i:j], sub) {
			return i, j
		}
	}
	return -1, -1
}

// pageExists は派生索引でページの存在を引きます（描画のたびに呼ぶので索引で足りる。
// 認可はしない——リンクを踏んだ先で通常の関門が判定する。存在の秘匿は匿名にだけ
// 意味があり、匿名へ返る公開ページに書かれた参照はどのみち書き手が可視化している）。
// 匿名の人に在るかどうかを見せないのは、呼び手の refViewer です（2026-10-26）。
func pageExists(pageID string) bool {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return false
	}
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = ?`, idInt).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// setAttr は属性を上書きまたは追加します。
func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// pageRefTags は「値がページ全体を指す」と宣言されたタグの名前です。
//
// **桁を緩めない代わりに、名前で宣言します。** 参照の文法は
// `ページID-ブロックID` の形しか認めません——6桁の数字だけでリンクにすると、
// 発注書番号や図番のような普通の値まで拾ってしまうためです
// （`TestRenderReferenceLinks` がその誤爆を固定しています）。
//
// けれど**ページ全体を指したいこともあります**（親ページID＝どの記録への返信か。2026-10-09 までの名前は `返信元`）。
// そこだけは「このタグはページを指す」と名前で宣言してもらいます。宣言があれば
// 誤爆の余地はありません——他のタグの6桁の値は今までどおり素通りします。
//
// 2026-09-03 ユーザー:「このリンクを付けるのは誰か？という所から始めて下さい」
// ——付けるのは**表示のときのコア**（本文には書かない）。どの値が参照かは、
// 値の形（文法）か、タグの名前（この宣言）で決まります。
// **宣言の置き場所は推論辞書です**（2026-09-06 に Goコードの表から移しました）。
// ユーザー:「名前が『受信元』であれば値は『参照』」——型を名前で決めるのは
// この仕組みの根幹（「項目の鍵は見出しの表示文字」）で、参照だけ別の原理
// （属性で明示）にする理由がありません。辞書にあることで**運用者が自分の
// 参照タグを足せます**。既定は `受信元`・`返信元`・`相手`（vocab.go）。
// （いまの正本は config/settings.json の `vocabulary` で、`"type": "ref"` の語です——`受信元`・`親ページID`・
// `相手`・`連絡帳` など。`返信元` は 2026-10-09 に `親ページID` へ改めました。）
func isPageRefTag(tagName string) bool {
	return InferColumnType(strings.TrimSpace(tagName)) == ColRef
}

// pageIDOnlyRe はページIDだけの参照値です（宣言されたタグでのみ使います）。
var pageIDOnlyRe = regexp.MustCompile(`^([0-9]{6})$`)

// parsePageRef は宣言されたタグの値をページIDへ分解します。
func parsePageRef(tagName, value string) (pageID string, ok bool) {
	if !isPageRefTag(tagName) {
		return "", false
	}
	m := pageIDOnlyRe.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// linkRefCells は表の**参照の列**のセルをリンクへ合成します（2026-09-20）。
//
// ⚠ **列の宣言が `ref` のときだけ**です。可変タグの側で学んだことがそのまま効きます
// ——「**値の形だけでは参照と番号を見分けられない**」（実データの発注書番号
// `250401-203` が「6桁＋ハイフン＋英数字」に当てはまり、全件が薄赤になった）。
// タグでは「可変タグの中か」を条件にしましたが、表では**列の型宣言**という、
// もっと強い手掛かりがあります。
//
// これが要るのは `弊社品番`（加工製品ページのページ番号）を足したためです
// ——**加工中に使う番号**なので、押して飛べないと意味が半分になります。
//
// ⚠ **登録されていない形式の表は触りません。** 索引に載らないのと同じ線引きで、
// 普通の文章の表に書かれた `000047` のような数をリンクにしないためです。
func linkRefCells(v refViewer, table *html.Node) bool {
	if inVocabChrome(table) {
		return false
	}
	def, known := VocabDefByType(vocabTypeOf(table))
	if !known {
		return false
	}
	rows := tableRows(table)
	if len(rows) < 2 {
		return false // 見出しだけの表には値が無い
	}
	// 見出し行から、参照の列の位置を拾う。
	headers := rowCells(rows[0])
	refCol := make([]bool, len(headers))
	any := false
	for i, h := range headers {
		key := strings.TrimSpace(nodeText(h))
		if resolveColumnType(h, def, key) == ColRef {
			refCol[i] = true
			any = true
		}
	}
	if !any {
		return false
	}

	changed := false
	for _, row := range rows[1:] {
		// ⚠ **クロームの行は本文データではありません**（2026-09-21 に実データで踏んだ）。
		// 表の上（`linkRefCells(table)`）では足りません——**表そのものは本文**で、
		// 中の一部だけがクロームだからです。鏡が `<tfoot>` へ足した検算の行は
		// `colspan` で横いっぱいのセル1つなので、**1列目（`弊社品番`＝`ref`）の値**と
		// 読まれ、**「合っています」の行が宙ぶらりんの参照の薄赤で塗られて**いました
		// （合格を警告の色で見せる、いちばん誤解を生む壊れ方）。
		//
		// ⚠ **順番がこの罠を作ります**——`RenderReferenceLinks` は
		// `RenderComputedViews` の**後**に走るので（handler_view.go）、**鏡が描いた
		// ものを本文として読みます**。クロームを足す鏡が増えるたびに同じ穴が開くので、
		// 表の中でも**行ごとに**見ます。
		if inVocabChrome(row) {
			continue
		}
		for i, cell := range rowCells(row) {
			if i >= len(refCol) || !refCol[i] {
				continue
			}
			// 参照をいくつも並べたセル（2026-10-19）——1つずつリンクにする。
			if refs := SplitRefList(nodeText(cell)); len(refs) > 1 {
				if linkRefList(v, cell, refs) {
					changed = true
				}
				continue
			}
			pageID, blockID, ok := parseRefValue(nodeText(cell))
			if !ok {
				// ⚠ **空欄は普通です**——解析は弊社品番を空で出し、人が後から結びます。
				// 薄赤にすると「まだ決めていない」行が全部赤くなります。
				if strings.TrimSpace(nodeText(cell)) == "" {
					continue
				}
				// **ページ番号だけでも受けます**（`000047`）。タグの側では「名前で
				// 参照と宣言されているか」を条件にしましたが（`parsePageRef`）、
				// ここでは**列がすでに `ref` と宣言されている**ので、それが許可です。
				// ⚠ 弊社品番は**加工製品ページそのもの**を指すので、ブロックIDは要りません。
				if m := pageIDOnlyRe.FindStringSubmatch(strings.TrimSpace(nodeText(cell))); m != nil {
					pageID, blockID, ok = m[1], "", true
				}
			}
			if !ok {
				// 形が合わない値（打ちかけ・別の番号）も、宙ぶらりんとして知らせる。
				if v.markMissing(cell, "参照の形（ページID または ページID-ブロックID）ではありません") {
					changed = true
				}
				continue
			}
			if v.canLink(pageID) {
				linkRefDD(cell, pageID, blockID)
				changed = true
			} else if v.markMissing(cell, "参照先のページ "+pageID+" が見つかりません") {
				changed = true
			}
		}
	}
	return changed
}
