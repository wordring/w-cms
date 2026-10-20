package comm

// ☎ 発信の飾り（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）——`電話番号` のタグの値の欄に、
// 「☎ かける」と「記録を作る」を足します（コアのタグの飾りの口・internal/cms/tag_decor.go）。
// それまでは通信の JS がタグを探して、本文の下に足していました。押したときの働き（tel: へ・発信の記録を作る）は ext/comm/assets/comm.js。
//
// 発信そのものはブラウザ任せ（素の <a href="tel:">）——JS で横取りすると、ソフトフォンへの受け渡しを壊しかねない。
// 「記録を作る」は既定でオン（「かけたのに記録が無い」ほうが後で困る）。

import (
	stdhtml "html"
	"regexp"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

var notDialable = regexp.MustCompile(`[^0-9+]`)

func init() {
	cms.RegisterTagDecorator(PhoneTag, phoneDecoration)
}

func phoneDecoration(ctx *cms.MirrorContext, value string) []*html.Node {
	dial := notDialable.ReplaceAllString(value, "")
	if dial == "" {
		return nil
	}
	nodes, err := htmldoc.ParseFragment(`<span class="vocab-chrome phone-chrome" contenteditable="false">` +
		`<a class="phone-call" href="tel:` + stdhtml.EscapeString(dial) + `" data-number="` + stdhtml.EscapeString(value) + `">☎ かける</a> ` +
		`<label class="phone-record"><input type="checkbox" checked> 記録を作る</label></span>`)
	if err != nil {
		return nil
	}
	return nodes
}
