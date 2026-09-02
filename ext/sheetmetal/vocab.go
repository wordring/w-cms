package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 板金部の業務語彙——受発注・部材・見積の8形式
//
// コアが持つのは器と汎用の語彙（`tags`・`file`・計算ビュー・サンプルの検査記録）
// だけで、**業務の語彙はここが持ち込みます**。「語彙とプラグインは運用者のもの」
// （要件定義書 §1.1・§4.5）を、置き場でも成立させた形です。
//
// 他社へ配るときは `-tags minimal` でこのパッケージごと外れ、スラッシュメニューから
// 「顧客の発注書」も消えます。
// ─────────────────────────────────────────────────────────────────────────

import "w-cms/internal/cms"

func init() {
	cms.RegisterVocab(businessVocab...)
}

var businessVocab = []cms.VocabDef{
	{
		Type:        "part-materials",
		DisplayName: "部材定義",
		Category:    "業務",
		Icon:        "🔩",
		Element:     "table",
		// 部材の行そのものには部品番号が無く、ページ横断メタの「部品番号」タグが
		// ページ全体の鍵になる（集計は PagesByTag の逆引きでページを引き当てる）。
		RequiresTag: "部品番号",
		Columns: []cms.VocabColumn{
			{Field: "item-name", Label: "部材名", Type: cms.ColText},
			{Field: "cost", Label: "単価", Type: cms.ColNumber},
			{Field: "supplier-name", Label: "仕入先", Type: cms.ColText},
			{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		},
	},
	{
		Type:        "client-order",
		DisplayName: "顧客の発注書",
		Category:    "業務",
		Icon:        "📩",
		Element:     "section",
		Items:       "client-order-items",
		File:        true,
		Columns: []cms.VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: cms.ColText},
			{Field: "client-name", Label: "発注元", Type: cms.ColText},
			{Field: "ordered-at", Label: "発注日", Type: cms.ColDate},
		},
	},
	{
		Type:        "client-order-items",
		DisplayName: "受注明細",
		Category:    "業務",
		Icon:        "📩",
		Element:     "table",
		Hidden:      true,
		Columns: []cms.VocabColumn{
			{Field: "item-id", Label: "品番", Type: cms.ColText},
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			{Field: "price", Label: "単価", Type: cms.ColNumber},
			{Field: "quantity", Label: "数量", Type: cms.ColNumber},
			{Field: "status", Label: "状態", Type: cms.ColEnum, Enum: []string{"未着手", "加工中", "検査中", "納品済"}},
		},
	},
	{
		Type:        "our-order",
		DisplayName: "自社の発注書",
		Category:    "業務",
		Icon:        "📤",
		Element:     "section",
		Items:       "our-order-items",
		Columns: []cms.VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: cms.ColText},
			{Field: "supplier-name", Label: "発注先", Type: cms.ColText},
			{Field: "ordered-at", Label: "発注日", Type: cms.ColDate},
		},
	},
	{
		Type:        "our-order-items",
		DisplayName: "発注明細",
		Category:    "業務",
		Icon:        "📤",
		Element:     "table",
		Hidden:      true,
		Columns: []cms.VocabColumn{
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			{Field: "cost", Label: "単価", Type: cms.ColNumber},
			{Field: "quantity", Label: "数量", Type: cms.ColNumber},
			{Field: "status", Label: "状態", Type: cms.ColEnum, Enum: []string{"未納品", "納品済"}},
		},
	},
	{
		Type:        "our-estimate",
		DisplayName: "弊社の見積もり",
		Category:    "業務",
		Icon:        "💴",
		Element:     "dl",
		Columns: []cms.VocabColumn{
			{Field: "item-id", Label: "品番", Type: cms.ColText},
			{Field: "client-name", Label: "顧客", Type: cms.ColText},
			{Field: "price", Label: "見積金額", Type: cms.ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: cms.ColDate},
		},
	},
	{
		Type:        "supplier-estimate",
		DisplayName: "材料屋の見積もり",
		Category:    "業務",
		Icon:        "🏭",
		Element:     "dl",
		Columns: []cms.VocabColumn{
			{Field: "item-name", Label: "部材名", Type: cms.ColText},
			{Field: "supplier-name", Label: "仕入先", Type: cms.ColText},
			{Field: "cost", Label: "見積金額", Type: cms.ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: cms.ColDate},
		},
	},
	{
		Type:        "required-materials",
		DisplayName: "手配状況リスト",
		Category:    "ビュー",
		Icon:        "📊",
		Element:     "section",
		View:        true,
	},
}
