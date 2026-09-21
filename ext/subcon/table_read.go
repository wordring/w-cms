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
)

// headerIndex は見出し行の「表示文字 → 列の位置」を返します（同じ見出しが2つあれば後勝ち）。
//
// **項目の鍵は見出しの表示文字**なので、列の順番ではなくこの表で引きます。
func headerIndex(tr *html.Node) map[string]int {
	out := map[string]int{}
	for i, c := range cellsOf(tr) {
		out[strings.TrimSpace(textOf(c))] = i
	}
	return out
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
	foot := lastChild(table, "tfoot")
	if foot == nil {
		foot = &html.Node{Type: html.ElementNode, Data: "tfoot",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome"}}}
		table.AppendChild(foot)
	}
	td := &html.Node{Type: html.ElementNode, Data: "td",
		Attr: []html.Attribute{{Key: "colspan", Val: strconv.Itoa(span)}}}
	td.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	tr := &html.Node{Type: html.ElementNode, Data: "tr",
		Attr: []html.Attribute{{Key: "class", Val: trClass}}}
	tr.AppendChild(td)
	foot.AppendChild(tr)
}
