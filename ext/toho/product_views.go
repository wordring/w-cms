package toho

// 加工製品ページの4つの欄を、本文の印で好きな場所に置けるようにする（2026-10-26）。
//
// 利用者:「これらの位置をタグで決めるのはどうでしょう？data-ナンチャラが貼ってある場所に出るみたいな」
// ——印は欄ごとに4つ・今ある加工製品ページには全部に書き足す・テンプレートはいまの自動と同じ場所（どれも利用者の答え）。
//
//   - 💴 見積回答・🧮 見積計算書の一覧・🧾 受注・見積の単価・⏱ 加工時間を、それぞれビュー（スラッシュメニューの「ビュー」・
//     本文の印 `<section data-mirror="…"></section>`）にする。中身は、表の下へ自動で出していたものと同じ関数で描く。
//   - ページに印があれば、その欄は表の下へ自動で出さない（二重に出さない——appendQuotesList・insertOrderPrices）。
//     印の無いページは、いままでどおり自動で出す。
//   - 利用者:「移行が完了して見積計算書が記録フォルダ以下に出来たら見積表は消していく予定です」——自動の置き場所は見積表に
//     ついて回るので、見積表を消すと欄が動く。印なら動かない。

import (
	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 加工製品ページの欄のビューの形式です。
const (
	QuotesViewType        = "product-quotes"
	EstimateCalcsViewType = "product-estimate-calcs"
	OrderPricesViewType   = "product-order-prices"
	WorkHoursViewType     = "product-work-hours"
)

func init() {
	for _, v := range []struct {
		typ, name, icon string
		render          func(*auth.User, int) string
	}{
		{QuotesViewType, "見積回答", "💴", func(u *auth.User, id int) string { return quotesFor(u, id) }},
		{EstimateCalcsViewType, "見積計算書の一覧", "🧮", func(u *auth.User, id int) string { return estimateCalcsHTML(id, estimateCalcsOf(u, id)) }},
		{OrderPricesViewType, "受注・見積の単価", "🧾", func(u *auth.User, id int) string {
			if pageKindOf(id) == PageKindOurEstimateCalc {
				return "" // 見積計算書のページには出さない（受注・見積の単価は品物の台帳の欄——insertOrderPrices と同じ）
			}
			return orderPricesHTML(orderPricesForProduct(database.DB, u, id))
		}},
		{WorkHoursViewType, "加工時間", "⏱", func(u *auth.User, id int) string { return workHoursHTML(workHoursForProduct(u, id)) }},
	} {
		v := v
		cms.RegisterVocab(cms.VocabDef{Type: v.typ, DisplayName: v.name, Category: "ビュー", Icon: v.icon, Element: "section", View: true})
		cms.RegisterView(v.typ, func(u *auth.User, id int) string {
			if cms.IsTemplateArea(page.FormatID(id)) {
				return "" // テンプレートの中では描かない（表の下の自動と同じ——写したページで描く）
			}
			return v.render(u, id)
		})
	}
}

// quotesFor は「💴 見積回答」の中身です（表の下の自動 appendQuotesList と印のビューが同じものを描く）。
func quotesFor(viewer *auth.User, productID int) string {
	// 「計算表へ」は、写す先（いちばん新しい見積計算書。無ければこのページ——estimateSourceOf・/api/estimate/rows と同じ）に
	// 見積計算表があって、そこへ書ける人にだけ出す。
	put := ""
	if viewer != nil {
		if src := estimateSourceOf(viewer, productID); canWritePage(viewer, src) && estimateTablesIn(src) > 0 {
			put = page.FormatID(productID) // 画面は加工製品ページの番号で /api/estimate/rows を引き、写す先はサーバーが決める
		}
	}
	return quotesListHTML(quotesForProduct(database.DB, viewer, productID), put)
}

// hasMirrorMarker は、n を含む本文に、形式 viewType の鏡の印（`<section data-mirror="表示名">`）があるかです。
func hasMirrorMarker(n *html.Node, viewType string) bool {
	def, ok := cms.VocabDefByType(viewType)
	if !ok || n == nil {
		return false
	}
	root := n
	for root.Parent != nil {
		root = root.Parent
	}
	var walk func(*html.Node) bool
	walk = func(x *html.Node) bool {
		if x.Type == html.ElementNode && x.Data == "section" && cms.Attr(x, cms.MirrorAttr) == def.DisplayName {
			return true
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(root)
}
