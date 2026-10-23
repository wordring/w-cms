package toho

// 在庫表（2026-10-23・【考察】在庫の記録 §6）。
//
// トップ直下の置き場「在庫」に、種類ごとの在庫表を5つ置きます（材料・購入部品・支給部品・外注加工・加工製品）。
// 届いたもの・作ったものは、まず在庫表に入れ、同じときに作業を始めている分を引きます（§6.1）。
// ほとんどの物は、入れた数と引いた数が同じなので、行は残りません（在庫が 0 の行は消す・§7 問い11）。
// 表は「ただの表」です。人が棚卸しのたびに直してよく、機械は直された数から足し引きを続けます。
//
// 利用者（2026-10-23）:「届いたもの、作ったものを一度在庫表に入れて、在庫から出荷する形式にしてはどうでしょう？」
// 「納品された時に在庫が増えて、その時に作業が開始されている分が引かれる形ではどうでしょう？そして、ただの表ですから、時々棚卸で修正したら良いと思います。」

import "w-cms/internal/cms"

const (
	// StockBoxTitle は在庫表の置き場の題です（トップ直下・テンプレート「在庫」から作る）。
	StockBoxTitle = "在庫"
	// StockQtyLabel は在庫表の数の列です。定尺の材料は本数で数えます（長さは寸法に書く・§7 問い13）。
	StockQtyLabel = "在庫数"
	// StockCountedLabel は人が最後に数えた日（棚卸し）の列です。機械は書きません。
	StockCountedLabel = "棚卸し日"

	StockMaterialType  = "stock-material"
	StockPurchasedType = "stock-purchased"
	StockSuppliedType  = "stock-supplied"
	StockOutsourceType = "stock-outsource"
	StockProductType   = "stock-product"
)

// stockKind は在庫表1つの形です。Kind は部材発注の種類（設定の order_kinds の kind）で、加工製品は空です。
type stockKind struct {
	Type    string
	Caption string
	Kind    string
	// PerProduct は、同じ部材でも加工製品ごとに別の在庫として数えるか（外注加工した部品・加工製品）。
	// 材料・購入部品・支給部品は、どの加工製品でも同じ在庫です（§7 問い5「良いです」）。
	PerProduct bool
}

var stockKinds = []stockKind{
	{Type: StockMaterialType, Caption: "材料在庫表", Kind: "材料"},
	{Type: StockPurchasedType, Caption: "購入部品在庫表", Kind: "購入部品"},
	{Type: StockSuppliedType, Caption: "支給部品在庫表", Kind: "支給部品"},
	{Type: StockOutsourceType, Caption: "外注加工在庫表", Kind: "外注加工", PerProduct: true},
	{Type: StockProductType, Caption: "加工製品在庫表", PerProduct: true},
}

// stockKindOf は部材発注の種類 kind の在庫表を返します（無い種類は false）。
func stockKindOf(kind string) (stockKind, bool) {
	for _, k := range stockKinds {
		if k.Kind != "" && k.Kind == kind {
			return k, true
		}
	}
	return stockKind{}, false
}

// stockTail は、どの在庫表にもある右の列です。
func stockTail(unit bool) []cms.VocabColumn {
	out := []cms.VocabColumn{{Field: "stock", Label: StockQtyLabel, Type: cms.ColNumber}}
	if unit {
		out = append(out, cms.VocabColumn{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()})
	}
	return append(out,
		cms.VocabColumn{Field: "counted", Label: StockCountedLabel, Type: cms.ColDate},
		cms.VocabColumn{Field: "note", Label: "備考", Type: cms.ColText})
}

func stockColumns(typ string) []cms.VocabColumn {
	switch typ {
	case StockMaterialType:
		return append([]cms.VocabColumn{
			{Field: "material", Label: "材質", Type: cms.ColText},
			{Field: "shape", Label: "形状", Type: cms.ColText},
			{Field: "size", Label: "寸法", Type: cms.ColText},
		}, stockTail(true)...)
	case StockPurchasedType, StockSuppliedType:
		return append([]cms.VocabColumn{
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			{Field: "spec", Label: "仕様", Type: cms.ColText},
		}, stockTail(true)...)
	case StockOutsourceType:
		return append([]cms.VocabColumn{
			{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
			{Field: "no", Label: "番号", Type: cms.ColText},
			{Field: "work-part-no", Label: "加工部品番号", Type: cms.ColCode},
			{Field: "work-part-name", Label: "加工部品名", Type: cms.ColText},
			{Field: "work", Label: "加工内容", Type: cms.ColText},
		}, stockTail(false)...)
	case StockProductType:
		return append([]cms.VocabColumn{
			{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
			{Field: "item-id", Label: ItemNoTag, Type: cms.ColCode},
			{Field: "item-name", Label: "品名", Type: cms.ColText},
		}, stockTail(false)...)
	}
	return nil
}

func init() {
	for _, k := range stockKinds {
		// 置き場のテンプレートに入っている表なので、スラッシュメニューには出さない（行は人が足してよい）。
		cms.RegisterVocab(cms.VocabDef{
			Type: k.Type, DisplayName: k.Caption, Category: "業務", Icon: "📦", Element: "table", Hidden: true,
			Columns: stockColumns(k.Type),
		})
	}
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     StockBoxTitle,
		Extension: "toho",
		Why: "在庫の置き場です（2026-10-23）。種類ごとの在庫表（材料・購入部品・支給部品・外注加工・加工製品）を置きます。" +
			"届いたもの・作ったものは、ここに入れてから、作業を始めている分を引きます。人が棚卸しで直してよい表です。",
	})
}
