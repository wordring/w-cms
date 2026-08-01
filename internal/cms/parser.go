package cms

import (
	"strings"

	"golang.org/x/net/html"
)

// CorePage はHTML本文（＝ページの内容）から抽出される基本インデックス情報です。
// ページの属性（親ページID・作成/更新情報・権限）はHTMLではなくサイドカーが正本で、
// ここには含めません。カスタム要素（<m-*>）由来のデータは、それを所有する
// 各プラグインの Sync が抽出します（コアはカスタム要素の語彙を持ちません）。
type CorePage struct {
	Title string
}

// ParseCore はHTMLノード木から、ページ内容由来の基本情報（タイトル）を抽出します。
// 親ページID等の属性はサイドカー（<id>.meta.json）が正本のため、ここでは扱いません。
func ParseCore(root *html.Node) CorePage {
	core := CorePage{Title: "No Title"}

	WalkElements(root, func(n *html.Node) {
		// 最初の <h1> をタイトルとして採用する
		if n.Data == "h1" && core.Title == "No Title" {
			core.Title = extractText(n)
		}
	})

	return core
}

// extractText は指定されたHTMLノード配下にあるテキストをすべて抽出し、結合して返します。
func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var result string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		result += extractText(c)
	}
	return strings.TrimSpace(result)
}
