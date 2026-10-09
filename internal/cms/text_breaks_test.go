package cms

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestTextWithBreaks は、要素の中の文字を入れ子まで拾い、`<br>` を改行にすることを確かめます（表のセル・署名・
// 通信記録の見出し・メールの添付の名札が使う口——2026-10-09 に4つの写しを寄せた口の番人）。
func TestTextWithBreaks(t *testing.T) {
	// ⚠ td は表の中に書く（表の外の td は HTML の解析が捨てる）。
	doc, err := html.Parse(strings.NewReader(`<table><tr><td>株式会社<br>みなと<b>商店<br/>営業部</b> 山田</td></tr></table>`))
	if err != nil {
		t.Fatal(err)
	}
	var td *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "td" {
			td = n
		}
		for c := n.FirstChild; c != nil && td == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if td == nil {
		t.Fatal("td が見つかりません")
	}
	if got, want := TextWithBreaks(td), "株式会社\nみなと商店\n営業部 山田"; got != want {
		t.Errorf("TextWithBreaks = %q（%q のはず）", got, want)
	}
}
