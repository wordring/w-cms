package toho

// ─────────────────────────────────────────────────────────────────────────
// 本文の表を読み書きする小さな口（2026-09-22 に寄せた）
//
// 09-21 に一日で書かれた4本（受注残の書き戻し・弊社品番の埋め・部材発注書のPDF・
// 手配状況）が、**同じ数行を4回ずつ写していました**——見出しの表示文字から列の
// 位置を引く・行のセルの文字を集める・セルの中身を差し替える・「その形式の表か」を
// 属性と caption の両方で見る。ここへ寄せ、以後はここを通します。
//
// ⚠ **`cellsOf`・`rowsOf`・`textOf` は drawing_mirror.go**（表を読む鏡が最初に
// 置いた口）。ここはその上に載る1段です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// headerIndex は見出し行の「表示文字 → 列の位置」を返します。
//
// **項目の鍵は見出しの表示文字**なので、列の順番ではなくこの表で引きます。
//
// ⚠ **同じ見出しが2つあれば先勝ち**です（2026-09-23 に揃えた）。09-22 まではここだけ
// 後勝ちで、1列版の `headerIndexOf`（先勝ち）と画面（`draftLinesOf` の `indexOf`）とは
// **同じ表を別の列に読んでいました**——見出しが重なった表でだけ答えが変わる形で、
// エラーは出ません。「最初に名乗った列」が効き、2つ目は宣言に無い列として読まれません。
func headerIndex(tr *html.Node) map[string]int {
	out := map[string]int{}
	for i, c := range cellsOf(tr) {
		key := strings.TrimSpace(textOf(c))
		if _, dup := out[key]; !dup {
			out[key] = i
		}
	}
	return out
}

// headerIndexOf は見出し行からその列の位置を返します（無ければ -1）。
//
// `headerIndex` の1列版で、規則（先勝ち）は同じです。09-21 に material_price.go が
// 置いたものを、規則を1つにするためここへ寄せました（2026-09-23）。
func headerIndexOf(head *html.Node, label string) int {
	if i, ok := headerIndex(head)[label]; ok {
		return i
	}
	return -1
}

// cellTexts は行のセルの文字を、前後の空白を落として並び順に返します。
func cellTexts(tr *html.Node) []string {
	cells := cellsOf(tr)
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.TrimSpace(textOf(c))
	}
	return out
}

// setCellText はセルの中身をその文字だけにします（空なら空のセルにする）。
func setCellText(cell *html.Node, s string) {
	for cell.FirstChild != nil {
		cell.RemoveChild(cell.FirstChild)
	}
	if s != "" {
		cell.AppendChild(&html.Node{Type: html.TextNode, Data: s})
	}
}

// isTableOfType は表がその形式かを見ます（属性でも caption でも）。
//
// ⚠ **両方見ます。** 形式の宣言は `data-type` から見える文字（`<caption>`）へ移る
// 途中で、しばらく併存します（[docs/考察/【考察】発注書から受注明細へ.md] §2.4）。
// 片方しか見ないと、移した日に**黙って見つからなくなります**——エラーは出ません。
func isTableOfType(t *html.Node, vocabType string) bool {
	if cms.Attr(t, "data-type") == vocabType {
		return true
	}
	def, ok := cms.VocabDefByType(vocabType)
	if !ok {
		return false
	}
	cap := lastChild(t, "caption")
	return cap != nil && strings.TrimSpace(textOf(cap)) == def.DisplayName
}

// tablesOfType は断片の中から、その形式の表を文書順に集めます。
//
// 当たった表の中へは降りません（表の中に同じ形式の表は無い）。当たらない表の中へは
// 降ります——寄せる前の4か所がそう歩いていたので、振る舞いを揃えてあります。
func tablesOfType(nodes []*html.Node, vocabType string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" && isTableOfType(n, vocabType) {
			out = append(out, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// pagesByAnyTag は、どれかの名前のタグがその値を持つページを、重複なく昇順で返します。
//
// ⚠ **畳んだ一致で引きます**（`PagesByTagLoose`）——`code` 型は空白・ハイフン・
// 長音・大小を畳むので、`P103-227-6` を `P103 227 6` と打っても当たります。
// **空の値では引きません**（空の鍵で引き当てると `"" == ""` で無関係な行に当たる）。
func pagesByAnyTag(db cms.ReadOnlyDB, names []string, value string) []int {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, name := range names {
		ids, err := cms.PagesByTagLoose(db, name, value)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Ints(out)
	return out
}

// appendFootRow は表の足元（`<tfoot>`）に、横いっぱいの1行を足します。
//
// ⚠ **`<tfoot>` の行として足します**——表の中に `<p>` は置けません（パーサが
// 表の外へ追い出し、`<div>` が明細の手前に飛び出します・引き継ぎの罠）。
// `tfoot` は `vocab-chrome` なので保存されません。検算の行と「移行の確認前」の行が
// 同じ12行を写していたので寄せました。
func appendFootRow(table *html.Node, span int, trClass, text string) {
	footCell(table, span, trClass).AppendChild(&html.Node{Type: html.TextNode, Data: text})
}

// appendFootHTML は表の足元へ、**HTMLの中身**を持つ行を1つ足します。
//
// ⚠ **`appendFootRow` はテキストだけ**です（検算の ⚠／✓ の文）。こちらは**入力欄**を
// 置くために要ります——発注部材表の足元の「部材発注書を作る」（2026-09-22）。
// （2026-09-28 から「部材発注書を作る」は表の外〔包む節の中〕へ移しました——order_draft_mirror.go。いまここを
// 通るのは、部材発注明細表の足元の「送る」欄〔our_order_mirror.go〕などです。）
//
// ⚠ **表の中へ `<div>` を直に足してはいけません**——HTMLパーサが表の外（手前）へ
// 追い出します。だから `<tfoot>` の `<td>` の中に入れます。
func appendFootHTML(table *html.Node, span int, trClass, innerHTML string) {
	appendHTML(footCell(table, span, trClass), innerHTML)
}

// footCell は `<tfoot class="vocab-chrome">`（無ければ作る）に横いっぱいの行を1つ足し、
// その `<td>` を返します。テキストの行と入力欄の行が同じ12行を写していたので寄せました。
func footCell(table *html.Node, span int, trClass string) *html.Node {
	foot := lastChild(table, "tfoot")
	if foot == nil {
		foot = &html.Node{Type: html.ElementNode, Data: "tfoot",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome"}}}
		table.AppendChild(foot)
	}
	td := &html.Node{Type: html.ElementNode, Data: "td",
		Attr: []html.Attribute{{Key: "colspan", Val: strconv.Itoa(span)}}}
	tr := &html.Node{Type: html.ElementNode, Data: "tr",
		Attr: []html.Attribute{{Key: "class", Val: trClass}}}
	tr.AppendChild(td)
	foot.AppendChild(tr)
	return td
}

// appendHTML は断片を解析して、その要素の末尾へ子として足します（解析できなければ何もしない）。
//
// 鏡が押すものを足す場所（足元の欄・行末のボタン・リンクの後ろの進み具合）が
// 同じ5行を4回写していたので寄せました。
func appendHTML(parent *html.Node, innerHTML string) {
	nodes, err := htmldoc.ParseFragment(innerHTML)
	if err != nil {
		return
	}
	for _, n := range nodes {
		parent.AppendChild(n)
	}
}

// addRowChromeCells は表の各行の末尾へクロームのセルを1つ足します。
//
// 見出し行（最初の行）には空の `<th>`——⚠ **足さないと列がずれて見えます**。
// データ行には `inner(row, tr)` が返すHTML（空なら空のセル）。`row` は見出しを除いた
// 1始まりの番号で、画面が「何行目」を送るときの数え方と揃えてあります。
// ⚠ **`vocab-chrome` を付けるのは必須です**——付けないと、人が画面の表をコピーして
// 貼ったときに**本当の列として保存されます**。
func addRowChromeCells(table *html.Node, class string, inner func(row int, tr *html.Node) string) {
	for i, tr := range rowsOf(table) {
		cell := &html.Node{Type: html.ElementNode, Data: "td",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome " + class}}}
		if i == 0 {
			cell.Data = "th"
		} else if s := inner(i, tr); s != "" {
			appendHTML(cell, s)
		}
		tr.AppendChild(cell)
	}
}

// wrapInDiv は断片のノード列を1つの div の子にして返します（本文全体を1つの根から辿り、節を足せるように）。
// 書き戻すときは childrenOf(root) を描画します。2026-10-19 に寄せた（紙の本文を読む7か所が同じ4行を写していた）。
func wrapInDiv(nodes []*html.Node) *html.Node {
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	return root
}

// childrenOf は根の子を並べて返します（wrapInDiv で包んだ本文を書き戻すとき）。
func childrenOf(root *html.Node) []*html.Node {
	var out []*html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// headedSection は、見出し（h2）が heading の、まだ中身の無い節を作ります。
func headedSection(heading string) *html.Node {
	sec := &html.Node{Type: html.ElementNode, Data: "section"}
	h := &html.Node{Type: html.ElementNode, Data: "h2"}
	h.AppendChild(&html.Node{Type: html.TextNode, Data: heading})
	sec.AppendChild(h)
	return sec
}

// headedSectionIn は、根の下で見出しが heading の最初の節を返します（無ければ nil）。
func headedSectionIn(root *html.Node, heading string) *html.Node {
	return findElement([]*html.Node{root}, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == heading
	})
}

// noteSectionLines は、見出しが heading の節（見積書・部材見積依頼書の「備考」）の行を返します（節が無ければ nil）。
func noteSectionLines(root *html.Node, heading string) []string {
	if sec := headedSectionIn(root, heading); sec != nil {
		return noteLinesOf(sec)
	}
	return nil
}

// findElement は断片の中から、条件に合う最初の要素を文書順で返します（無ければ nil）。
func findElement(nodes []*html.Node, pred func(*html.Node) bool) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && pred(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return found
}

// spliceNodes は断片の中の target を repl で置き換え（keepTarget なら target の直後へ足し）、
// 描画し直した本文を返します。target が断片に無ければ (元の本文, false)。
//
// ⚠ **トップレベルの要素には `Parent` がありません**（`ParseFragment` は根の無い
// ノード列を返す）。**本文の直下に置かれた表がまさにそれ**なので、その場合は
// ノード列のほうを組み替えます——表の差し替え・表の削除・PDFのマーカーの挿入が
// 同じ罠を3回書いていたので寄せました。
func spliceNodes(nodes []*html.Node, target *html.Node, repl []*html.Node, keepTarget bool) (string, bool) {
	if target.Parent == nil {
		out := make([]*html.Node, 0, len(nodes)+len(repl))
		hit := false
		for _, nd := range nodes {
			if nd != target {
				out = append(out, nd)
				continue
			}
			hit = true
			if keepTarget {
				out = append(out, nd)
			}
			out = append(out, repl...)
		}
		if !hit {
			return htmldoc.Render(nodes), false
		}
		return htmldoc.Render(out), true
	}
	parent := target.Parent
	anchor := target.NextSibling
	if !keepTarget {
		parent.RemoveChild(target)
	}
	for _, r := range repl {
		parent.InsertBefore(r, anchor)
	}
	return htmldoc.Render(nodes), true
}

// fillVocabTable は形式 vocabType の表（キャプションは表示名）へ、機械キーごとの値の行を
// 入れます（2026-09-27）。列の見出しは宣言から引き、テンプレートの見出しの言葉で合わせます
// ——**列の並びはテンプレートが決め、行の数は機械がその場で決めます**。
func fillVocabTable(b cms.DraftBlock, vocabType string, rows []map[string]string) ([]*html.Node, error) {
	cols := columnsOf(vocabType)
	labels := make([]string, len(cols))
	for i, c := range cols {
		labels[i] = c.Label
	}
	vals := make([][]string, len(rows))
	for r, m := range rows {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = strings.TrimSpace(m[c.Field])
		}
		vals[r] = row
	}
	return b.FillTable(displayNameOf(vocabType), labels, vals)
}

// tableSig は表の中身の指紋です（足元〔tfoot〕を除く行の、セルの文字。nil なら空）。
//
// 画面の操作が「何番目の表・組」で場所を指すとき、描いたときの指紋を一緒に送ってもらい、書く直前に同じ表かを確かめます
// （2026-10-22 の見直し）——番号だけだと、ほかの人が表を足した・写した・消したあとで、別の表に効いていた。
// ⚠ 鏡が描いたときの本文（ファイル）から作ること。画面の DOM から作ると、飾りが混ざって合いません。
func tableSig(t *html.Node) string {
	if t == nil {
		return ""
	}
	h := sha256.New()
	for _, tr := range rowsOf(t) {
		if tr.Parent != nil && tr.Parent.Data == "tfoot" {
			continue
		}
		for _, c := range cellTexts(tr) {
			h.Write([]byte(c))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// rowSig は表の1行の中身の指紋です（画面の飾りのセル〔vocab-chrome〕は数えない）。行の先頭のチェックが持ち、操作の直前に
// サーバーが同じ行かを確かめます（2026-10-22 の見直し——行の番号だけだと、ほかの人が行を足す・消すと別の行に効いていた）。
func rowSig(tr *html.Node) string {
	h := sha256.New()
	for _, c := range cellsOf(tr) {
		if strings.Contains(" "+cms.Attr(c, "class")+" ", " vocab-chrome ") {
			continue
		}
		h.Write([]byte(strings.TrimSpace(textOf(c))))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// resolveRowsBySig は、表 table の行の番号（見出しを除いた何行目か・1始まり）と指紋の組を、いまの本文の行の番号へ引き直します
// （指紋が空なら番号のまま・pickBySig）。合う行が無い・2つの組が同じ行に当たるときは ok=false。
func resolveRowsBySig(table *html.Node, rows []int, sigs []string) ([]int, bool) {
	trs := rowsOf(table)
	var body []*html.Node
	for i, tr := range trs {
		if i == 0 || (tr.Parent != nil && tr.Parent.Data == "tfoot") {
			continue
		}
		body = append(body, tr)
	}
	out := make([]int, 0, len(rows))
	seen := map[int]bool{}
	for i, r := range rows {
		sig := ""
		if i < len(sigs) {
			sig = sigs[i]
		}
		k := pickBySig(len(body), func(j int) string { return rowSig(body[j]) }, r-1, sig)
		if k < 0 || seen[k+1] {
			return nil, false
		}
		seen[k+1] = true
		out = append(out, k+1)
	}
	return out, true
}

// pickBySig は、index 番目の指紋が sig と合えばそのまま、合わなければ同じ指紋のものがちょうど1つならその番号を返します
// （無い・2つ以上なら -1）。sig が空なら index のまま（指紋を送らない古い画面）。n は数、sigAt は i 番目の指紋。
func pickBySig(n int, sigAt func(int) string, index int, sig string) int {
	if sig == "" {
		if index < 0 || index >= n {
			return -1
		}
		return index
	}
	if index >= 0 && index < n && sigAt(index) == sig {
		return index
	}
	found := -1
	for i := 0; i < n; i++ {
		if sigAt(i) == sig {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	return found
}

// errTableMoved は、指紋の合う表が見つからないときの断りです。
const errTableMoved = errString("その表は、ほかの人が書き換えたか、足したり写したりして場所が変わりました。ページを開き直してください")
