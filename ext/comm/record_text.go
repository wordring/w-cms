package comm

// ─────────────────────────────────────────────────────────────────────────
// 通信記録の本文を平文で読む（2026-10-01 に ext/comm/mail から上げた）
//
// 返信の引用（ext/comm/mail）と、メールの本文から受注ページを作る解析（ext/toho）が同じものを読むので、
// 記録の形（`本文` の節の `<pre>`）を知っているこの拡張に1つだけ置きます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// RecordBodyText は記録の本文（`本文` の節の `<pre>`）を平文で返します。
//
// ⚠ 節が無い古い記録は、本文の最初の `<pre>` を読みます（2026-09-05 から本文は `<pre>` 1つ）。
func RecordBodyText(bodyHTML string) string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return ""
	}
	var inSection, anyPre string
	for _, root := range nodes {
		cms.WalkElements(root, func(n *html.Node) {
			if n.Data == "section" && inSection == "" && sectionHeadingOf(n) == MailBodyHeading {
				cms.WalkElements(n, func(p *html.Node) {
					if p.Data == "pre" && inSection == "" {
						inSection = textWithBreaks(p)
					}
				})
			}
			if n.Data == "pre" && anyPre == "" {
				anyPre = textWithBreaks(n)
			}
		})
	}
	if inSection != "" {
		return inSection
	}
	return anyPre
}

// sectionHeadingOf は節の最初の見出し（h2〜h6）の文字です。
func sectionHeadingOf(section *html.Node) string {
	for c := section.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			switch c.Data {
			case "h2", "h3", "h4", "h5", "h6":
				return strings.TrimSpace(textWithBreaks(c))
			}
		}
	}
	return ""
}

// textWithBreaks は要素の中の文字をつなげて返します（`<br>` は改行）。
func textWithBreaks(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch {
		case x.Type == html.TextNode:
			sb.WriteString(x.Data)
		case x.Type == html.ElementNode && x.Data == "br":
			sb.WriteString("\n")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
