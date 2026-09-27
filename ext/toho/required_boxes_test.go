package toho

import (
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// TestBoxBodiesCarryViewMarkers は、拡張が登録する置き場の本文に**鏡の印があり、
// 見出しで名乗っている**ことを固定します（2026-09-27）。
//
// コアの `TestRequiredPageBodiesCarryWorkSurface` は、コアのパッケージでは本文を持つ置き場が
// 1つも登録されないので**何も確かめていませんでした**。拡張が入ったこの試験の中なら、
// 取引先・発注（と、読み込まれていれば通信箱・連絡帳）の本文が実際に並びます。
// ⚠ 名前の見えない `<section data-type=…>` の印は 2026-09-27 に廃止——書いてあれば落とします。
func TestBoxBodiesCarryViewMarkers(t *testing.T) {
	checked := 0
	for _, p := range cms.RequiredPages() {
		if p.Body == nil {
			continue
		}
		checked++
		body := p.Body()
		nodes, err := htmldoc.ParseFragment(body)
		if err != nil {
			t.Fatalf("%s: 本文を読めません: %v", p.Title, err)
		}
		views := 0
		for _, n := range nodes {
			cms.WalkElements(n, func(el *html.Node) {
				if def, ok := cms.VocabDefByType(cms.VocabTypeOf(el)); ok && def.View {
					views++
				}
				if def, ok := cms.VocabDefByType(cms.Attr(el, "data-type")); ok && def.View {
					t.Errorf("%s: 廃止した名前の見えない鏡の印があります: %s", p.Title, body)
				}
			})
		}
		if views == 0 {
			t.Errorf("%s: 本文に鏡の印がありません（見出しだけのページは行き止まり）: %s", p.Title, body)
		}
	}
	// 取引先と発注の2つは、この拡張が必ず登録します——0なら番人が空振りしています。
	if checked < 2 {
		t.Fatalf("本文を持つ置き場が %d 個しかありません（2つ以上のはず）", checked)
	}
}
