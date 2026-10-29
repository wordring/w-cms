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
	// PageKindCustomerQuote は見積の依頼の案件ページの値です（2026-10-19・【考察】製造ページ §7 の答え）——案件残表に並ぶが、
	// まだ注文ではないので、受注残高・赤字・必要部材表・受注の単価には入らず、納品書も作れない（isQuoteCase）。
	// 2026-10-19 利用者:「見積もりは、見積もりを受注したということにして、受注ページに並べてそこから作るのはどうですか？」
	PageKindCustomerQuote = "顧客見積依頼"
	// PageKindOwnCase は自社案件の値です（2026-10-21・own_case.go）——消耗品・工具・社内用など、客先の注文に結び付かない部材の案件。
	// 案件残表に並び、製造へ入れられるが、売上ではないので受注残高・赤字・検算は出さず、納品書も作れない。
	PageKindOwnCase = "自社案件"
	// PageKindProduct は加工製品ページ（品物の台帳）の値です——加工製品の一覧が集めます（product_list.go）。
	PageKindProduct = "加工製品"
	// PageKindCompany・PageKindMachine は製造台帳の社名と装置名のフォルダの値です。
	// ⚠ **フォルダの種類には頼りすぎないこと**（利用者:「依存しすぎないよう注意したほうがよいと思います」）——
	// 加工製品の一覧が、行に添える社名・装置名を探すのに使うだけです。
	PageKindCompany = "社名"
	PageKindMachine = "装置"
	// PageKindProductOld は図面改定で子へ移った旧版の加工製品ページの値です（revision.go の createOldVersionPage が書く）。
	// 加工製品の一覧（`productListAll`）と同じ品番の確かめ（`sameCodeProducts`）はこの値で外します。置き場の下を探す
	// `productListRows` は、いまも「親も加工製品なら旧版」と木の形で見分けます。
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
	return kindOfTags(tags)
}

// kindOfTags は、ページのタグ（TagsOfPage）から `ページ種類` の値を返します（前後の空白は落とす）。
// 同じ式が8か所ほどに写されていた（2026-10-29 に寄せた）。
// caseKinds は、案件明細表を案件として読むページの種類です——案件残表（backlog.go の backlogScan）と手配の対象
// （procure_ledger.go の openOrderRows）が同じ線で選びます（2026-10-30 に揃えた。それまで手配の対象は、案件明細表を持つ
// ページ全部から見積の依頼を外していたので、ページ種類の無い古いページの行が手配にだけ出ることがありえた）。
// 2026-10-30 利用者:「揃えたいです」
var caseKinds = []string{PageKindCustomerOrder, PageKindCustomerQuote, PageKindOwnCase}

// isCaseKind は、ページ種類 kind が caseKinds の1つかを返します。
func isCaseKind(kind string) bool {
	for _, k := range caseKinds {
		if kind == k {
			return true
		}
	}
	return false
}

func kindOfTags(tags map[string][]string) string {
	return strings.TrimSpace(cms.FirstTag(tags, PageKindTag))
}
