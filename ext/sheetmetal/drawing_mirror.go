package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 古い図面に赤枠を出す——表示のときだけ（2026-09-03）
//
// ユーザー:「既存ページの図面の項目の先頭に配置してはどうでしょう？既存の図面は
// 古いとわかるように**赤枠で囲み**、ユーザーの判断で消します。（古い図面は旧版に
// 残っています）」
//
// **状態は持ちません。** 改定図面は先頭へ差し込まれるので（filing.go の
// mergeAsRevision）、**先頭以外が古い**——並びそのものが最新を表します。
// 「どれが古いか」を本文へ書き込むと、人が並べ替えたときに嘘になります。
//
// 印は**保存されません**。`class` はサニタイズで落ちるので（`ref-missing` の
// 薄赤と同じ作り）、閲覧のたびにここで付け直します。人が古い図面ブロックを
// 消せば、その時点で赤枠も消えます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

func init() {
	cms.RegisterMirror("drawing", cms.MirrorHandlerFunc(markSupersededDrawing))
}

// markSupersededDrawing は、前に別の図面ブロックがある図面へ印を付けます。
func markSupersededDrawing(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if hasEarlierDrawing(el) {
		addClass(el, "drawing-superseded")
	}
	return true, nil
}

// hasEarlierDrawing は、同じ親の中でこの要素より前に図面ブロックがあるかを返します。
func hasEarlierDrawing(el *html.Node) bool {
	for p := el.PrevSibling; p != nil; p = p.PrevSibling {
		if p.Type == html.ElementNode && p.Data == "section" && isDrawingSection(p) {
			return true
		}
	}
	return false
}

// isDrawingSection は section が図面ブロックかを見出しで判定します（機能見出し形・D-2）。
// **見出しの表示文字が正**——機械キーを本文へ書く属性はありません。
func isDrawingSection(sec *html.Node) bool {
	for c := sec.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "h2" {
			continue
		}
		return textOf(c) == "図面"
	}
	return false
}

// textOf は要素の中の文字を連結します。
func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	out := ""
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out += textOf(c)
	}
	return out
}

// addClass は class 属性へ1つ足します（既存があれば空白で連ねる）。
func addClass(el *html.Node, name string) {
	for i, a := range el.Attr {
		if a.Key == "class" {
			if a.Val == "" {
				el.Attr[i].Val = name
			} else {
				el.Attr[i].Val = a.Val + " " + name
			}
			return
		}
	}
	el.Attr = append(el.Attr, html.Attribute{Key: "class", Val: name})
}
