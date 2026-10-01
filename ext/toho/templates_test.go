package toho

import (
	"strings"
)

// 機械が作るページのテンプレート（2026-09-27〜）の、試験で使う写しです。
//
// 形は**職場のテンプレート**（テンプレート／東邦／{受注ページ・発注書・加工製品}）と同じに
// しています。`seedBoxTemplates` がこれをテンプレート置き場へ置き、本文を直に組む試験は
// 下の `testOrderPage` などを通します（テンプレートを埋める本物の口を通る）。

// sourcePDFCaption は受注ページのテンプレートで、原本PDFを畳む枠の見出しです
// （2026-09-27 まではコードの定数で、いまはテンプレートが持つ言葉）。
const sourcePDFCaption = "顧客の発注書（PDF）"

// emptyTagsDL は名前だけ並べた可変タグ（値は空欄）です。
func emptyTagsDL(names ...string) string {
	var b strings.Builder
	b.WriteString(`<dl data-type="tags">`)
	for _, n := range names {
		b.WriteString(`<dt>` + n + `</dt><dd><br/></dd>`)
	}
	b.WriteString(`</dl>`)
	return b.String()
}

// emptyVocabTable はキャプション・見出しの行・空の見本の行だけの表です（スラッシュメニューで挿す形）。
func emptyVocabTable(vocabType string) string {
	var row strings.Builder
	for range columnsOf(vocabType) {
		row.WriteString(`<td></td>`)
	}
	return `<table><caption>` + displayNameOf(vocabType) + `</caption><tbody>` +
		headerRowHTML(vocabType) + `<tr>` + row.String() + `</tr></tbody></table>`
}

func testOrderPageTemplate() string {
	return `<h1>` + OrderPageTemplate + `</h1>` +
		emptyTagsDL(OrderNoTag, OrderClientTag, OrderedAtTag, DueDateTag, SubtotalTag, TaxTag, TotalTag, SourceRefTag) +
		`<details><summary>` + sourcePDFCaption + `</summary><section data-type="file-view" data-ref=""></section></details>` +
		`<details><summary>` + sourceTableCaption + `</summary><p><br/></p></details>` +
		emptyVocabTable(clientOrderItemsType)
}

// testEstimateTemplate は見積書ページのテンプレートです（2026-10-01・職場のテンプレート／東邦／見積書と同じ形）。
func testEstimateTemplate() string {
	return `<h1>` + EstimateTemplate + `</h1>` +
		emptyTagsDL(EstimateNoTag, EstimateClientTag, EstimatePersonTag, EstimateDateTag, EstimateDueTag,
			EstimatePlaceTag, EstimateTradeTag, EstimateValidTag, EstimateSignerTag) +
		emptyVocabTable(EstimateItemsType) +
		`<section><h2>` + estimateNoteHeading + `</h2><p><br/></p></section>`
}

func testPurchaseOrderTemplate() string {
	return `<h1>` + PurchaseOrderTemplate + `</h1>` +
		emptyTagsDL(OrderNoTag, SupplierTag, OrderedAtTag, DueDateTag, OrderSignerTag) +
		emptyVocabTable(ourOrderItemsType) +
		`<section><h2>` + orderNoteHeading + `</h2><p><br/></p></section>`
}

// testProductTemplate は職場の「加工製品」テンプレートの形です——**エディタが字下げして
// 保存した形のまま**にしてあります（字下げが機械の作るページに残らないことも確かめるため）。
func testProductTemplate() string {
	return "<h1 data-id=\"0gg4\">加工製品</h1>\n" +
		"<dl data-id=\"vui2\" data-type=\"tags\">\n    <dt>品番</dt>\n    <dd><br/></dd>\n</dl>\n" +
		"<section data-id=\"oyut\">\n    <h2>図面</h2>\n    <dl data-id=\"gt6x\" data-type=\"tags\">\n" +
		"        <dt>図面番号</dt>\n        <dd><br/></dd>\n        <dt>図面名称</dt>\n        <dd><br/></dd>\n" +
		"        <dt>装置名称</dt>\n        <dd><br/></dd>\n        <dt>客先</dt>\n        <dd><br/></dd>\n    </dl>\n" +
		"    <section data-id=\"jcan\" data-ref=\"\" data-type=\"file-view\"></section>\n</section>\n" +
		"<table data-id=\"yux2\"><caption>改訂明細</caption>\n        <tbody>\n            <tr>\n" +
		"                <th>版</th>\n                <th>図面番号</th>\n                <th>受領日</th>\n            </tr>\n" +
		"            <tr data-id=\"1mz9\">\n                <td>1</td>\n                <td></td>\n                <td></td>\n" +
		"            </tr>\n        </tbody>\n    </table>\n" +
		"<section data-id=\"hm1t\">\n    <h2>材料</h2>\n    <table data-id=\"z184\">\n        <caption>材料</caption>\n" +
		"        <tbody>\n            <tr>\n                <th>材質</th>\n                <th>形状</th>\n" +
		"                <th>寸法</th>\n                <th>個数</th>\n                <th>備考</th>\n                <th>区分</th>\n" +
		"            </tr>\n            <tr data-id=\"j5rd\">\n                <td></td>\n                <td></td>\n" +
		"                <td></td>\n                <td></td>\n                <td></td>\n                <td></td>\n" +
		"            </tr>\n        </tbody>\n    </table>\n</section>"
}

// testOrderPage は試験用のテンプレートで受注ページの本文を組みます（組めなければ panic）。
func testOrderPage(hostPageID, attachID string, j *orderJudgment) string {
	body, err := buildOrderPageHTML(testOrderPageTemplate(), hostPageID, attachID, j)
	if err != nil {
		panic(err)
	}
	return body
}

// testProductPage は試験用のテンプレートで加工製品ページの本文を組みます。
func testProductPage(hostPageID, attachID string, j *orderJudgment, matches []matchedDXF) string {
	body, err := buildProductPageHTML(testProductTemplate(), hostPageID, attachID, j, matches)
	if err != nil {
		panic(err)
	}
	return body
}

// testOurOrder は試験用のテンプレートで発注書ページの本文を組みます。
func testOurOrder(pageID, supplier, orderAt, due, note, signerID string, lines []ourOrderLine) string {
	body, err := buildOurOrderHTML(testPurchaseOrderTemplate(), pageID, supplier, orderAt, due, note, signerID, lines)
	if err != nil {
		panic(err)
	}
	return body
}
