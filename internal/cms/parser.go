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

	// CreatedAt / CreatedBy / UpdatedAt は文書先頭の <m-page-info> から抽出される
	// ページ属性です（作成日時・作成者・更新日時）。いずれもサーバーが書き込む権限を
	// 持ち、保存APIがHTMLへ注入する。HTMLに記録するためDB再構築でも失われない。
	CreatedAt string
	CreatedBy string
	UpdatedAt string
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
		case "m-page-info":
			// ページ属性ブロック。作成日時・作成者をHTMLから読み取る。
			// 親ページIDは内包する <m-tag name="親ページID"> 経由で別途同期される。
			core.CreatedAt = Attr(n, "created-at")
			core.CreatedBy = Attr(n, "created-by")
			core.UpdatedAt = Attr(n, "updated-at")
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
