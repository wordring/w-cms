package subcon

// ─────────────────────────────────────────────────────────────────────────
// 本文の表を読み書きする小さな口（2026-09-22 に寄せた）
//
// 09-21 に一日で書かれた4本（受注残の書き戻し・弊社品番の埋め・発注書のPDF・
// 手配状況）が、**同じ数行を4回ずつ写していました**——見出しの表示文字から列の
// 位置を引く・行のセルの文字を集める・セルの中身を差し替える・「その形式の表か」を
// 属性と caption の両方で見る。ここへ寄せ、以後はここを通します。
//
// ⚠ **`cellsOf`・`rowsOf`・`textOf` は drawing_mirror.go**（表を読む鏡が最初に
// 置いた口）。ここはその上に載る1段です。
// ─────────────────────────────────────────────────────────────────────────

import (
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
// 途中で、しばらく併存します（[docs/【考察】発注書から受注明細へ.md] §2.4）。
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
// 置くために要ります——発注部材表の足元の「発注書を作る」（2026-09-22）。
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
