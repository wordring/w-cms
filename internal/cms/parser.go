package cms

import (
	"strings"

	"golang.org/x/net/html"
)

// PageTag は <m-tag name="..." value="..."> から抽出される可変タグです。
type PageTag struct {
	Name  string
	Value string
}

// CorePage はHTML本文（＝ページの内容）から抽出される基本インデックス情報です。
// ページの属性（親ページID・作成/更新情報・権限）はHTMLではなくサイドカーが正本で、
// ここには含めません。ユースケース固有のデータは各プラグインが個別に抽出します。
type CorePage struct {
	Title string
	Tags  []PageTag
}

// ParseCore はHTMLノード木から、ページ内容由来の基本情報（タイトル・タグ）を抽出します。
// 親ページID等の属性はサイドカー（<id>.meta.json）が正本のため、ここでは扱いません。
// ユースケース固有の抽出は plugin_*.go の各 Sync が担当します。
func ParseCore(root *html.Node) CorePage {
	core := CorePage{Title: "No Title"}

	WalkElements(root, func(n *html.Node) {
		switch n.Data {
		case "h1":
			// 最初の <h1> をタイトルとして採用する
			if core.Title == "No Title" {
				core.Title = extractText(n)
			}
		case "m-tag":
			// 旧方式の「親ページID」タグはサイドカーへ移行済みのため、ユーザータグとしては扱わない。
			name := Attr(n, "name")
			if name != "" && name != "親ページID" {
				core.Tags = append(core.Tags, PageTag{Name: name, Value: Attr(n, "value")})
			}
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
