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

// CorePage はどのページにも共通する基本インデックス情報です。
// ユースケース固有のデータ（発注書・部材など）は各プラグインが個別に抽出します。
type CorePage struct {
	Title    string
	ParentID string
	Tags     []PageTag
}

// ParseCore はHTMLノード木から、ページの基本情報（タイトル・親ページID・タグ）を抽出します。
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
			if name := Attr(n, "name"); name != "" {
				core.Tags = append(core.Tags, PageTag{Name: name, Value: Attr(n, "value")})
			}
		}
	})

	// 「親ページID」タグがあれば ParentID に採用する
	for _, tag := range core.Tags {
		if tag.Name == "親ページID" {
			core.ParentID = tag.Value
			break
		}
	}

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
