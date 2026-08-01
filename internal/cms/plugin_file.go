package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン: ファイル容器
//
//   <m-file src="PO-A100.pdf" name="顧客発注書.pdf" ext="pdf">
//     <m-client-order order-no="PO-A100" ...>...</m-client-order>
//   </m-file>
//
// <m-file> は「ここにファイルがある」だけを表す**純粋な容器**で、業務上の意味は
// 中身の要素（<m-client-order> 等）が持つ（【考察】通信記録処理.md §4.5）。
//
// かつては tag 属性の文字列（"顧客の発注書" 等）で意味を切り替え、業務属性
// （order-no・client-name・price…）も直に抱える過積載状態だった。プラグイン側も
// Attr(n,"tag") のスイッチで自分の担当かを判定していた。それらは廃止済み。
//
// 固有のテーブルは持たない（同期するデータが無い）が、
// 「カスタムタグはすべてプラグインが所有する」方針に従い語彙の所有者としてここに置く。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(filePlugin{})
}

type filePlugin struct{}

func (filePlugin) Name() string { return "file" }

func (filePlugin) Schema() []string { return nil }

func (filePlugin) Tables() []string { return nil }

func (filePlugin) Tags() []TagSpec {
	return []TagSpec{
		// 汎用ファイルメタのみ。業務属性は中身の要素が持つ。
		{Element: "m-file", Attributes: []string{"src", "name", "ext"}},
	}
}

func (filePlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error { return nil }
