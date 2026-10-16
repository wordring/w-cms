package toho

// ─────────────────────────────────────────────────────────────────────────
// ページ種類のタグ（2026-10-16）
//
// 利用者:「「ページ種類」のタグを持たせます」「ほかの種類にも付けます」
// 「フォルダにもページ種類はつけて良いと思いますが、依存しすぎないよう注意したほうがよいと思います」
//
// ページが何の書類か（加工製品・顧客発注書・弊社見積書…）を、題や木の場所ではなく
// **タグ `ページ種類`** で名乗ります。ビューを置いた場所に依存させないためです
// （【考察】全体の設計 §4.3・§4.4）。
//
//   - 値の一覧は設定の語彙 `ページ種類` が持ちます（enum・コードに書き写さない）。
//     ここに置くのは、**機械が自分で書く値**だけです。
//   - 機械が作るページは、テンプレートがこのタグを持っているので、写すだけで付きます
//     （下書きの口 `cms.NewPageDraft` はテンプレートのタグを残したまま値を足す）。
//   - ⚠ **コアは知りません**——コアにとっては、ほかと同じ1つのタグです
//     （【要求】w-cmsとは §1「ページの種類をコードや DB に固定しません」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"w-cms/internal/cms"
	"w-cms/internal/database"
)

const (
	// PageKindTag はページの種類を名乗るタグの名前です。
	PageKindTag = "ページ種類"
	// PageKindCustomerOrder は受注ページ（客先から届いた発注書）の値です——受注残表が集めます（backlog.go）。
	PageKindCustomerOrder = "顧客発注書"
	// PageKindProduct は加工製品ページ（品物の台帳）の値です——加工製品の一覧が集めます（product_list.go）。
	PageKindProduct = "加工製品"
	// PageKindCompany・PageKindMachine は製造台帳の社名と装置名のフォルダの値です。
	// ⚠ **フォルダの種類には頼りすぎないこと**（利用者:「依存しすぎないよう注意したほうがよいと思います」）——
	// 加工製品の一覧が、行に添える社名・装置名を探すのに使うだけです。
	PageKindCompany = "社名"
	PageKindMachine = "装置"
	// PageKindProductOld は図面改定で子へ移った旧版の加工製品ページの値です
	// （いまは「親も加工製品なら旧版」と木の形で見分けている——`productListRows`）。
	PageKindProductOld = "加工製品の旧版"
	// 弊社・業者の書類の値です（題の頭を決めるのに使う——issuerTitle）。
	PageKindOurEstimate = "弊社見積書"
	PageKindOurRFQ      = "弊社部材見積依頼書"
	PageKindOurPurchase = "弊社部材発注書"
	PageKindVendorQuote = "業者見積書"
)

// issuerTitle は題の頭に、その紙を出した側（【顧客】【弊社】【業者】）を付けます（2026-10-16）。
//
// 利用者:「ページの見分けがつきやすいように【弊社】【業者】【顧客】という接頭辞を付けてはどうでしょう？」
// （【考察】全体の設計 §4.6）。出した側は `ページ種類` の値の頭の語です（`顧客発注書`・`弊社見積書`・`業者見積書`）。
//
//   - ⚠ **人のための目印です。** 機械は題で見分けません（引き継ぎの罠「題で判断しない」）——人が題を直して
//     接頭辞が外れても、機械は困りません。
//   - テンプレートの題には入れられません（テンプレートは題で探す——`cms.PageTemplateBody`）。だから、機械が題を
//     決めるところで付けます。
//   - もう付いていれば、二重に付けません。
func issuerTitle(kind, title string) string {
	for _, w := range []string{"顧客", "弊社", "業者"} {
		if strings.HasPrefix(kind, w) {
			p := "【" + w + "】"
			if strings.HasPrefix(title, p) {
				return title
			}
			return p + title
		}
	}
	return title
}

// pageKindOf はページの `ページ種類` の値を返します（無ければ空）。
func pageKindOf(id int) string {
	tags, err := cms.TagsOfPage(database.DB, id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cms.FirstTag(tags, PageKindTag))
}
