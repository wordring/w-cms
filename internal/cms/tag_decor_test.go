package cms

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// タグの飾り（tag_decor.go・2026-10-20）——登録した名前のタグの値の欄へ、飾りを足すことを固定します。
func TestTagDecoratorAppendsToValue(t *testing.T) {
	saved := tagDecorators
	tagDecorators = map[string][]TagDecorator{}
	t.Cleanup(func() { tagDecorators = saved })
	RegisterTagDecorator("試験の番号", func(ctx *MirrorContext, value string) []*html.Node {
		b := &html.Node{Type: html.ElementNode, Data: "button", Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome probe-btn"}, {Key: "data-v", Val: value}}}
		return []*html.Node{b}
	})
	body := `<h1>試験</h1><dl data-type="tags"><dt>試験の番号</dt><dd>123</dd><dt>ほかの名前</dt><dd>456</dd></dl>`
	got := RenderComputedViews(httptest.NewRequest("GET", "/000010", nil), 10, body)
	if !strings.Contains(got, `<dd>123<button class="vocab-chrome probe-btn" data-v="123"></button></dd>`) {
		t.Errorf("⚠ 登録した名前のタグの値に飾りが足されていません:\n%s", got)
	}
	if strings.Contains(got, `data-v="456"`) {
		t.Errorf("⚠ 登録していない名前のタグに飾りが足されています:\n%s", got)
	}
	// 飾りの名前が無いページは、パースしない（早道）。
	plain := `<h1>試験</h1><dl data-type="tags"><dt>ほかの名前</dt><dd>456</dd></dl>`
	if hasDecoratedTag(plain) {
		t.Errorf("飾りの無いページで早道が外れています")
	}
}
