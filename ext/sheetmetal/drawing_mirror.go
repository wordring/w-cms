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
	"strings"

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

func init() {
	// 廃版の構成部品を薄く見せる（表示のときだけ）。**行は消しません**
	// ——外注加工に出した紙に社内コードが載っているので、消すと指し先が消えます
	// （ユーザー:「構成部品は図面の改定に伴って廃版になる場合があります」）。
	for _, t := range []string{"part-materials", "part-outsourcing", "part-purchased"} {
		cms.RegisterMirror(t, cms.MirrorHandlerFunc(markObsoleteRows))
	}
}

// markObsoleteRows は 状態＝廃版 の行へ印を付けます（見た目は CSS が担う）。
func markObsoleteRows(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	col := statusColumnIndex(el)
	if col < 0 {
		return true, nil
	}
	for _, tr := range rowsOf(el) {
		if cellText(tr, col) == "廃版" {
			addClass(tr, "row-obsolete")
		}
	}
	return true, nil
}

// statusColumnIndex は見出し行から「状態」列の位置を返します（無ければ -1）。
// **見出しの表示文字が鍵**——機械キーを本文へ書く属性はありません。
func statusColumnIndex(table *html.Node) int {
	for _, tr := range rowsOf(table) {
		i := 0
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "th" && strings.TrimSpace(textOf(c)) == "状態" {
				return i
			}
			if c.Data == "th" || c.Data == "td" {
				i++
			}
		}
		return -1 // 最初の行が見出し行（語彙モデル §5.1）。無ければ諦める
	}
	return -1
}

// rowsOf は表の行を（tbody を挟んでいても）集めます。
func rowsOf(table *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "tr" {
				out = append(out, c)
				continue
			}
			walk(c)
		}
	}
	walk(table)
	return out
}

// cellText は行の i 番目のセルの文字を返します。
func cellText(tr *html.Node, i int) string {
	n := 0
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
			continue
		}
		if n == i {
			return strings.TrimSpace(textOf(c))
		}
		n++
	}
	return ""
}
