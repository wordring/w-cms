package cms

// ─────────────────────────────────────────────────────────────────────────
// 参照タグのリンク描画と宙ぶらりんの印（D-4・D-10。2026-08-31 実装）
//
// 参照は本文の「名前：値」タグで表し、値は **`ページID-ブロックID`**——
// 「URLのリンクみたいな感じ」（docs/アーキテクチャとDBスキーマ.md §9.1〜§9.2）。
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

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/database"
)

// refValueRe は参照値の文法です（6桁のページID・ハイフン類・英数字のブロックID）。
var refValueRe = regexp.MustCompile(`^([0-9]{6})[-‐−－ーｰ]([0-9A-Za-z]+)$`)

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
func RenderReferenceLinks(bodyHTML string) string {
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
				if linkRefCells(n) {
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
					if linkEmailDD(dd) {
						changed = true
					}
					return true
				}
				pageID, blockID, ok := parseRefValue(nodeText(dd))
				if !ok {
					// 形が合わなくても、**名前で宣言されたタグ**ならページ全体への
					// 参照として扱います（返信元など）。
					if pageID, ok = parsePageRef(key, nodeText(dd)); !ok {
						return true
					}
					blockID = ""
				}
				if pageExists(pageID) {
					linkRefDD(dd, pageID, blockID)
				} else {
					// 宙ぶらりん——打ち間違いか、参照先をまだ作っていない。
					// 「先に参照を書く」運用があるので拒否はせず、背景で知らせる。
					setAttr(dd, "class", "ref-missing")
					setAttr(dd, "title", "参照先のページ "+pageID+" が見つかりません")
				}
				changed = true
				return true
			})
		})
	}
	if !changed {
		return bodyHTML
	}
	return htmldoc.Render(nodes)
}

// linkRefDD は dd の中身を参照リンクへ置き換えます（表示文字は元の値のまま）。
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
type ContactResolver func(addr string) (pageID, title string, ok bool)

// contactResolver は登録された解決係です（未登録なら nil）。
var contactResolver ContactResolver

// RegisterContactResolver はアドレス→連絡先ページの解決係を登録します。
//
// **コアはアドレス帳を知りません。** `email` 型のタグを描くとき「このアドレスの
// ページはどれか」を知る必要がありますが、それに答えられるのは**業務側**です
// ——アドレス帳は板金にもメールにも依らない業務の持ち物、という判定が出ています
// （[docs/【考察】アドレス帳の作り直し.md] §5b）。
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
func linkEmailDD(dd *html.Node) bool {
	if contactResolver == nil {
		return false // アドレス帳を持たない構成——素のまま出す
	}
	text := strings.TrimSpace(nodeText(dd))
	addr, ok := normalizeEmailTag(text)
	if !ok {
		return false // アドレスが無い（名前だけのヘッダ）
	}
	pageID, title, found := contactResolver(addr)
	if !found {
		return false // まだアドレス帳に無い——素のまま出す
	}

	// 生の値の中で、アドレスの部分だけを差し替えます。
	at := strings.LastIndex(strings.ToLower(text), addr)
	if at < 0 {
		return false
	}
	before, after := text[:at], text[at+len(addr):]

	for dd.FirstChild != nil {
		dd.RemoveChild(dd.FirstChild)
	}
	if before != "" {
		dd.AppendChild(&html.Node{Type: html.TextNode, Data: before})
	}
	a := &html.Node{Type: html.ElementNode, Data: "a"}
	a.Attr = []html.Attribute{
		{Key: "href", Val: "/" + pageID},
		{Key: "class", Val: "ref-link"},
		{Key: "title", Val: title + " のページへ"},
	}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: text[at : at+len(addr)]})
	dd.AppendChild(a)
	if after != "" {
		dd.AppendChild(&html.Node{Type: html.TextNode, Data: after})
	}
	return true
}

// pageExists は派生索引でページの存在を引きます（描画のたびに呼ぶので索引で足りる。
// 認可はしない——リンクを踏んだ先で通常の関門が判定する。存在の秘匿は匿名にだけ
// 意味があり、匿名へ返る公開ページに書かれた参照はどのみち書き手が可視化している）。
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
// けれど**ページ全体を指したいこともあります**（返信元＝どの記録への返信か）。
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
func linkRefCells(table *html.Node) bool {
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
				setAttr(cell, "class", "ref-missing")
				setAttr(cell, "title", "参照の形（ページID または ページID-ブロックID）ではありません")
				changed = true
				continue
			}
			if pageExists(pageID) {
				linkRefDD(cell, pageID, blockID)
			} else {
				setAttr(cell, "class", "ref-missing")
				setAttr(cell, "title", "参照先のページ "+pageID+" が見つかりません")
			}
			changed = true
		}
	}
	return changed
}
