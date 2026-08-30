package cms

import (
	"strings"

	"golang.org/x/net/html"
)

// PageTitle はHTML本文から表題（最初の <h1> の表示文字）を返します。無ければ "No Title"。
//
// ページ内容から抽出する基本情報はこれだけです——親ページID・作成/更新情報・権限は
// サイドカーが正本（page パッケージ）、マーカー付き標準HTML（data-type 付きの
// table/dl/section）由来のデータは各プラグインと汎用索引が抽出します。
// かつては CorePage 構造体で複数フィールドを運んでいましたが、正本の分担が
// 進んでタイトルだけが残ったため関数1つに畳みました（2026-08-30）。
func PageTitle(root *html.Node) string {
	title := "No Title"
	WalkElements(root, func(n *html.Node) {
		if n.Data == "h1" && title == "No Title" {
			title = strings.TrimSpace(nodeText(n))
		}
	})
	return title
}
