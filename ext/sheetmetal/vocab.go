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
		// 図面ブロック——1つの図面の表題欄です。**部品ページには複数並びます**
		// （改定図面が来たら新しいものを先頭に足す。古いものは赤枠で残し、
		// 消すのは人の判断——2026-09-03 ユーザー決定）。
		//
		// **図面番号で検索できることが要件**（ユーザー:「図面番号、図面名称など
		// 様々なタグがあります。のちのち、これらを検索できるようにしたいです」）。
		// 語彙に登録して初めて `vocab_index` に載るので、ここが検索の前提。
		//
		// 装置名称と客先を持つのは、**置き場所（顧客名／装置名称／図面名称）の
		// 推奨値**になるから。ただし置き場所を決めるのは人で、機械は提案するだけ
		// ——「顧客は適当」なので、いま決められないことがある（作業引き継ぎ）。
		Type:        "drawing",
		DisplayName: "図面",
		Category:    "業務",
		Icon:        "📐",
		Element:     "section",
		File:        true,
		Columns: []cms.VocabColumn{
			{Field: "drawing-no", Label: "図面番号", Type: cms.ColText},
			{Field: "drawing-name", Label: "図面名称", Type: cms.ColText},
			{Field: "machine-name", Label: "装置名称", Type: cms.ColText},
			{Field: "client-name", Label: "客先", Type: cms.ColText},
		},
	},
	{
		// 改訂履歴——**社内コードの指し先**（2026-09-03 ユーザー:「改訂履歴の項目を
		// 作り版にdata-idを割り当てれば良いのでは？」）。
		//
		// 図面ブロックそのものを指すと困ることがあります。古い図面は「赤枠で残し、
		// ユーザーの判断で消す」決まりなので、**消せるものを指し先にすると、
		// 紙に出た社内コード（作業指示・ラベル）が宙ぶらりんになります**。
		// 履歴の行は小さく、消す理由がありません——ここを指し先にすれば、
		// かさばる図面ブロックは自由に消せます。
		//
		// 1行が1つの版で、行の `data-id` が改定番号。`ページID-行ID` で飛べます
		// （アンカー合成は data-id を持つあらゆる要素に効く。anchor.go）。
		Type:        "drawing-revisions",
		DisplayName: "改訂履歴",
		Category:    "業務",
		Icon:        "🕐",
		Element:     "section",
		Items:       "drawing-revision-items",
		Columns:     []cms.VocabColumn{},
	},
	{
		Type:        "drawing-revision-items",
		DisplayName: "改訂明細",
		Category:    "業務",
		Icon:        "🕐",
		Element:     "table",
		Hidden:      true,
		Columns: []cms.VocabColumn{
			{Field: "revision", Label: "版", Type: cms.ColText},
			{Field: "drawing-no", Label: "図面番号", Type: cms.ColText},
			{Field: "received-at", Label: "受領日", Type: cms.ColDate},
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
