package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// 部材の節をまとめて畳む印（2026-10-04・drawing_mirror.go）——利用者:「材料、外注加工などの部材項目全体について開閉できるようにしたい」。
// 畳むのは画面（app.js）で、鏡は部材の節に `w-fold w-fold-parts` を付けるだけ。

// TestPartsSectionsCarryFoldMark は、⚠ **部材の4つの節に印が付き、ほかの節には付かない**こと、1つの節に部材の表が2枚あっても
// 印は1つずつであることを固定します。
func TestPartsSectionsCarryFoldMark(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 41, 0, "カバー", "root", "302", true)
	body := `<h1>カバー</h1>` +
		`<section><h2>図面</h2><p>図面の説明</p></section>` +
		`<section><h2>材料</h2><table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
		`<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr></tbody></table>` +
		`<table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
		`<tr><td>SUS304</td><td>丸棒</td><td>φ20</td><td>1</td></tr></tbody></table></section>` +
		`<section><h2>外注加工</h2><table><caption>外注加工</caption><tbody><tr><th>番号</th><th>加工内容</th></tr>` +
		`<tr><td>1</td><td>塗装</td></tr></tbody></table></section>` +
		`<section><h2>購入部品</h2><table><caption>購入部品</caption><tbody><tr><th>品名</th><th>個数</th></tr>` +
		`<tr><td>ボルト</td><td>4</td></tr></tbody></table></section>` +
		productWithSupplied[len(`<h1>カバー</h1>`):] +
		`<section><h2>メモ</h2><table><tbody><tr><th>何</th></tr><tr><td>素の表</td></tr></tbody></table></section>`
	req := auth.WithUser(httptest.NewRequest("GET", "/000041", nil), &auth.User{Username: "root", IsAdmin: true})
	got := cms.RenderComputedViews(req, 41, body)

	nodes, err := htmldoc.ParseFragment(got)
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]string{}
	classOf := func(n *html.Node) string {
		for _, a := range n.Attr {
			if a.Key == "class" {
				return a.Val
			}
		}
		return ""
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "section" {
			for h := n.FirstChild; h != nil; h = h.NextSibling {
				if h.Type == html.ElementNode && h.Data == "h2" {
					marked[textOf(h)] = classOf(n)
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	for _, name := range []string{"材料", "外注加工", "購入部品", "支給部品"} {
		if marked[name] != "w-fold "+partsFoldClass {
			t.Errorf("⚠ 部材の節「%s」の印が %q です（w-fold %s が要る——1つの節に表が2枚あっても1つずつ）:\n%s", name, marked[name], partsFoldClass, got)
		}
	}
	for _, name := range []string{"図面", "メモ"} {
		if strings.Contains(marked[name], "w-fold") {
			t.Errorf("⚠ 部材でない節「%s」に畳む印が付いています: %q", name, marked[name])
		}
	}
}
