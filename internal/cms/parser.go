package cms

import (
	"strings"

	"golang.org/x/net/html"
)

// PageMeta はページ全体を表現する汎用的なメタデータ構造体です。
type PageMeta struct {
	ID      string
	Type    string
	Path    string // ★追加: 仮想階層 (例: "/製品/自社開発/ケース")
	Title   string
	Summary string
}

// ParseHTMLMaster はHTML5の仕様に従って文字列を解析し、検索に必要な情報を抽出します。
func ParseHTMLMaster(id string, htmlContent string) PageMeta {
	// ★追加: Pathのデフォルト値は、指定がなかった場合のルート階層として "/" を設定します。
	meta := PageMeta{ID: id, Type: "page", Path: "/", Title: "No Title", Summary: ""}

	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return meta
	}

	// DOMツリーを再帰的に巡回（トラバース）する関数
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// <m-page type="..." path="..."> タグの解析
			if n.Data == "m-page" {
				for _, attr := range n.Attr {
					if attr.Key == "type" {
						meta.Type = attr.Val
					}
					// ★追加: path属性を見つけたら、その値（仮想階層）を抽出する
					if attr.Key == "path" {
						meta.Path = attr.Val
					}
				}
			}

			// タイトル(h1)と要約(p)の抽出
			if n.Data == "h1" && meta.Title == "No Title" {
				meta.Title = extractText(n)
			}
			if n.Data == "p" && meta.Summary == "" {
				summaryText := extractText(n)
				runes := []rune(summaryText)
				if len(runes) > 50 {
					meta.Summary = string(runes[:50]) + "..."
				} else {
					meta.Summary = string(runes)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return meta
}

// extractText は指定されたHTMLノード配下にあるテキストをすべて抽出し、結合して返す関数です。
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
