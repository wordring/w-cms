package cms

import (
	"errors"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// ─────────────────────────────────────────────────────────────────────────
// ブロック単位の差し替え
//
// 本文の1ブロックだけを更新するための、正本HTMLへの差し込み処理です。
// ブロックは `data-id`（BlockIDAttr）で識別しますが、**この属性は任意**です。
// 手で書いたHTMLのようにIDを持たない本文もあるため、対象が見つからない場合は
// エラーを返し、呼び出し側（保存API）は全文保存へフォールバックします。
//
// なお正本はファイル単位で書き直すため、この仕組みで減るのは**送信量と
// エコーバックの粒度**であって、ディスク書き込みやDB同期の量ではありません
// （プラグインの同期はページ単位の洗い替えのままです）。
// ─────────────────────────────────────────────────────────────────────────

// ErrBlockNotFound は指定IDのブロックが本文に1つも無い場合に返されます。
var ErrBlockNotFound = errors.New("指定されたブロックが見つかりません")

// trimEdgeWhitespace はノード列の前後にある空白だけのテキストノードを取り除きます。
func trimEdgeWhitespace(nodes []*html.Node) []*html.Node {
	isBlank := func(n *html.Node) bool {
		return n.Type == html.TextNode && strings.TrimSpace(n.Data) == ""
	}
	start, end := 0, len(nodes)
	for start < end && isBlank(nodes[start]) {
		start++
	}
	for end > start && isBlank(nodes[end-1]) {
		end--
	}
	return nodes[start:end]
}

// ErrBlockAmbiguous は同じIDのブロックが複数ある場合に返されます。
// ページ間でブロックをコピーするとIDが重複しうるため、安全側に倒して
// 差し替えを拒否し、全文保存にフォールバックさせます。
var ErrBlockAmbiguous = errors.New("同じIDのブロックが複数あります")

// ReplaceBlock は本文 pageHTML のうち、blockID を持つトップレベルのブロックを
// blockHTML で差し替えた本文全体を返します。
//
// blockHTML は呼び出し側でサニタイズ済みであること（この関数は構造の差し替えだけを行う）。
func ReplaceBlock(pageHTML, blockID, blockHTML string) (string, error) {
	if blockID == "" {
		return "", ErrBlockNotFound
	}

	nodes, err := htmldoc.ParseFragment(pageHTML)
	if err != nil {
		return "", err
	}

	// 差し替え対象はトップレベルのブロックに限る（明細など入れ子の要素は
	// 親ブロックごと送られてくるため、ここで探すのは最上位だけでよい）。
	idx := -1
	for i, n := range nodes {
		if n.Type != html.ElementNode || Attr(n, htmldoc.BlockIDAttr) != blockID {
			continue
		}
		if idx >= 0 {
			return "", ErrBlockAmbiguous
		}
		idx = i
	}
	if idx < 0 {
		return "", ErrBlockNotFound
	}

	replacement, err := htmldoc.ParseFragment(blockHTML)
	if err != nil {
		return "", err
	}
	// ブロック間の改行・インデントは元の本文側が既に持っている。差し込む断片にも
	// 前後の空白テキストが付いていると、保存のたびに空行が1つずつ増えていくため落とす。
	replacement = trimEdgeWhitespace(replacement)

	if len(replacement) == 0 {
		return "", ErrBlockNotFound
	}

	var sb strings.Builder
	for i, n := range nodes {
		if i != idx {
			html.Render(&sb, n)
			continue
		}
		for _, r := range replacement {
			html.Render(&sb, r)
		}
	}
	return sb.String(), nil
}
