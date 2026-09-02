package cms

// テスト用の語彙（作り物）。
//
// 業務語彙は 2026-09-03 に ext/sheetmetal へ出た（コンパイル時に選べるように
// するため）ので、コアのテストからは見えない。だがコアの機構——サニタイザ・
// 配送係・ブロック・テンプレート・索引——のテストは、**現実的な形の語彙**を
// 道具として必要とする。そこで同じ形をここで登録する。
//
// **これは ext の宣言の写しであって、正本ではない。** ずれても構わない
// （試しているのはコアの機構であって、板金部の列定義ではない）。ext 側の
// 宣言そのものは ext のテストが検証する。

func init() {
	RegisterVocab(testFixtureVocab...)
}

var testFixtureVocab = []VocabDef{
	{
		Type:        "part-materials",
		DisplayName: "部材定義",
		Category:    "業務",
		Icon:        "🔩",
		Element:     "table",
		// 部材の行そのものには部品番号が無く、ページ横断メタの「部品番号」タグが
		// ページ全体の鍵になる（集計は PagesByTag の逆引きでページを引き当てる）。
		RequiresTag: "部品番号",
		Columns: []VocabColumn{
			{Field: "item-name", Label: "部材名", Type: ColText},
			{Field: "cost", Label: "単価", Type: ColNumber},
			{Field: "supplier-name", Label: "仕入先", Type: ColText},
			{Field: "quantity", Label: "数量", Type: ColNumber},
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
		Columns: []VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: ColText},
			{Field: "client-name", Label: "発注元", Type: ColText},
			{Field: "ordered-at", Label: "発注日", Type: ColDate},
		},
	},
	{
		Type:        "client-order-items",
		DisplayName: "受注明細",
		Category:    "業務",
		Icon:        "📩",
		Element:     "table",
		Hidden:      true,
		Columns: []VocabColumn{
			{Field: "item-id", Label: "品番", Type: ColText},
			{Field: "item-name", Label: "品名", Type: ColText},
			{Field: "price", Label: "単価", Type: ColNumber},
			{Field: "quantity", Label: "数量", Type: ColNumber},
			{Field: "status", Label: "状態", Type: ColEnum, Enum: []string{"未着手", "加工中", "検査中", "納品済"}},
		},
	},
	{
		Type:        "our-order",
		DisplayName: "自社の発注書",
		Category:    "業務",
		Icon:        "📤",
		Element:     "section",
		Items:       "our-order-items",
		Columns: []VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: ColText},
			{Field: "supplier-name", Label: "発注先", Type: ColText},
			{Field: "ordered-at", Label: "発注日", Type: ColDate},
		},
	},
	{
		Type:        "our-order-items",
		DisplayName: "発注明細",
		Category:    "業務",
		Icon:        "📤",
		Element:     "table",
		Hidden:      true,
		Columns: []VocabColumn{
			{Field: "item-name", Label: "品名", Type: ColText},
			{Field: "cost", Label: "単価", Type: ColNumber},
			{Field: "quantity", Label: "数量", Type: ColNumber},
			{Field: "status", Label: "状態", Type: ColEnum, Enum: []string{"未納品", "納品済"}},
		},
	},
	{
		Type:        "our-estimate",
		DisplayName: "弊社の見積もり",
		Category:    "業務",
		Icon:        "💴",
		Element:     "dl",
		Columns: []VocabColumn{
			{Field: "item-id", Label: "品番", Type: ColText},
			{Field: "client-name", Label: "顧客", Type: ColText},
			{Field: "price", Label: "見積金額", Type: ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: ColDate},
		},
	},
	{
		Type:        "supplier-estimate",
		DisplayName: "材料屋の見積もり",
		Category:    "業務",
		Icon:        "🏭",
		Element:     "dl",
		Columns: []VocabColumn{
			{Field: "item-name", Label: "部材名", Type: ColText},
			{Field: "supplier-name", Label: "仕入先", Type: ColText},
			{Field: "cost", Label: "見積金額", Type: ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: ColDate},
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
