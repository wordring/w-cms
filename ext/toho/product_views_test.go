package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestProductViewsGoWhereTheMarkersAre は、加工製品ページの4つの欄（💴 見積回答・🧮 見積計算書・🧾 受注・見積の単価・⏱ 加工時間）が、
//   - 本文に印（`<section data-mirror="…">`）があれば、その印の所に1度だけ出る（表の下へ自動では出さない）
//   - 印の無い欄は、いままでどおり支給部品表の下に自動で出る
//
// ことを固定します（2026-10-26・product_views.go——利用者:「これらの位置をタグで決めるのはどうでしょう？」）。
func TestProductViewsGoWhereTheMarkersAre(t *testing.T) {
	setupExtTest(t, "000880", page.PageMeta{Owner: "root", Mode: "330"})
	root := &auth.User{Username: "root", IsAdmin: true}
	addPage(t, 881, -1, "カバー", "root", "302", true)
	kind := `<dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>`
	supplied := productWithSupplied[len(`<h1>カバー</h1>`):]
	marker := func(viewType string) string { return cms.ViewMarkerHTML(viewType) }
	titles := map[string]string{
		QuotesViewType: "💴 見積回答", EstimateCalcsViewType: "🧮 見積計算書", OrderPricesViewType: "🧾 受注・見積の単価", WorkHoursViewType: "⏱ 加工時間",
	}
	render := func(body string) string {
		t.Helper()
		syncBody(t, 881, body)
		return cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000881", nil), root), 881, body)
	}
	at := func(out, s string) int { return strings.Index(out, s) }
	suppliedAt := func(out string) int { return at(out, "<caption>支給部品表</caption>") }

	// 1. 印が無い——4つとも支給部品表の下に、1度ずつ（いままでどおり）。
	out := render(`<h1>カバー</h1>` + kind + supplied)
	for typ, title := range titles {
		if n := strings.Count(out, title); n != 1 || at(out, title) < suppliedAt(out) {
			t.Errorf("⚠ 印の無いページで %s が支給部品表の下に1度ではありません（%d 度）:\n%s", typ, n, out)
		}
	}

	// 2. 4つとも印を、支給部品表より上に置く——印の所に1度ずつ出て、支給部品表の下には出ない。
	out = render(`<h1>カバー</h1>` + kind + marker(QuotesViewType) + marker(EstimateCalcsViewType) + marker(OrderPricesViewType) +
		marker(WorkHoursViewType) + supplied)
	last := -1
	for _, typ := range []string{QuotesViewType, EstimateCalcsViewType, OrderPricesViewType, WorkHoursViewType} {
		title := titles[typ]
		if n := strings.Count(out, title); n != 1 {
			t.Errorf("⚠ 印のあるページで %s が %d 度出ました（1度のはず）:\n%s", typ, n, out)
		}
		if p := at(out, title); p < 0 || p > suppliedAt(out) || p < last {
			t.Errorf("⚠ %s が印の所（支給部品表より上・印の順）に出ていません:\n%s", typ, out)
		} else {
			last = p
		}
	}

	// 3. 加工時間だけ印を置く——加工時間は印の所、ほかの3つは支給部品表の下。
	out = render(`<h1>カバー</h1>` + kind + marker(WorkHoursViewType) + supplied)
	if n, p := strings.Count(out, titles[WorkHoursViewType]), at(out, titles[WorkHoursViewType]); n != 1 || p > suppliedAt(out) {
		t.Errorf("⚠ 加工時間が印の所に1度ではありません（%d 度）:\n%s", n, out)
	}
	for _, typ := range []string{QuotesViewType, EstimateCalcsViewType, OrderPricesViewType} {
		if n, p := strings.Count(out, titles[typ]), at(out, titles[typ]); n != 1 || p < suppliedAt(out) {
			t.Errorf("⚠ 印の無い %s が支給部品表の下に1度ではありません（%d 度）:\n%s", typ, n, out)
		}
	}
}
