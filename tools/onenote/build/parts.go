package main

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// partsSink は ■材料・■外注加工・■購入品・■支給品 の節の中身のうち、テンプレートの表へ入らないもの（見出しの違う表・
// 表の外の文・写真とファイル）を置く所です（2026-10-15）。
//
//   - テンプレートに見出しの節（「材料」など）があれば、その節の中（2026-10-14 までのテンプレート）。
//   - 無ければ（2026-10-15 からのテンプレート——見出し「部材」の下に表が並ぶ・【要求】東邦拡張 §21）、その表のすぐ後ろ。
//     ■外注加工 の写真・ファイル（外注先へ渡す資料）は、小見出し「外注加工の資料」の下へ（無ければ表のすぐ後ろ）。
//
// それまでは見出しの節だけを探していたので、いまのテンプレートでは表が全部「節がありません」で落ちていた。
type partsSink struct {
	box   *html.Node // 見出しの節（中の末尾へ足す）。nil なら after の後ろへ
	after *html.Node // 表（または、その後ろに最後に足したもの）
	docs  *html.Node // 資料を足す位置（小見出し、または最後に足した資料）。nil なら after と同じ
}

// docsHeading は外注加工の資料を置く小見出しの文字です（テンプレートの見出し）。
const docsHeading = "外注加工の資料"

func partsSinkFor(d *cms.PageDraft, name string) (*partsSink, bool) {
	m := tableMap[name]
	if box, ok := d.Container(m.Section); ok {
		return &partsSink{box: box.Node()}, true
	}
	t, ok := d.Table(m.Caption)
	if !ok || t.Parent == nil {
		return nil, false
	}
	s := &partsSink{after: t}
	if name == "外注加工" {
		s.docs = findHeading(d.Node(), docsHeading)
	}
	return s, true
}

// add は表の外のもの（文・見出しの違う表）を置きます。
func (s *partsSink) add(frag string) {
	if s.box != nil {
		appendHTML(s.box, frag)
		return
	}
	s.after = insertFragAfter(s.after, frag)
}

// addDoc は写真・ファイルを置きます（外注加工なら資料の小見出しの下）。
func (s *partsSink) addDoc(frag string) {
	if s.box != nil || s.docs == nil {
		s.add(frag)
		return
	}
	s.docs = insertFragAfter(s.docs, frag)
}

// anchor は、あとに続く節を並べる目印です。
func (s *partsSink) anchor() *html.Node {
	if s.box != nil {
		return s.box
	}
	return s.after
}

// insertFragAfter は ref のすぐ後ろに frag の要素を順に置き、最後に置いたものを返します。
func insertFragAfter(ref *html.Node, frag string) *html.Node {
	nodes, err := htmldoc.ParseFragment(frag)
	if err != nil {
		return ref
	}
	for _, n := range nodes {
		insertAfter(ref, n)
		ref = n
	}
	return ref
}

// findHeading は見出し（h2〜h4）のうち、文字が text のものを文書順で探します。
func findHeading(root *html.Node, text string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "h2" || n.Data == "h3" || n.Data == "h4") && strings.TrimSpace(nodeTextOf(n)) == text {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

func nodeTextOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeTextOf(c))
	}
	return b.String()
}
