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
	// ── 構成部品（図面から人が抽出する）──────────────────────────────
	//
	// ユーザー:「構成部品は私たちが図面から抽出します。材料も抽出しますし、
	// 外注加工、購入部品も抽出します。**外注加工の時に構成部品の番号が効いてきます**」
	// 「ほかに支給部品という項目もあります」（2026-09-03）。種別ごとに表を分けるのは
	// ユーザーの選択——種別で要る列が違うため。
	//
	// **行の `data-id` が構成部品の番号**です。`ページID-行ID` が社内コードになり、
	// 外注加工に出す紙に載ります。だから**行は消しません**——廃版は `区分` の列で
	// 表します（ユーザー:「構成部品は図面の改定に伴って廃版になる場合があります」）。
	// 消すと、相手先に渡した紙の番号が指す先が無くなります。
	//
	// **単価と仕入先はここに持ちません**（2026-09-03 ユーザー:「仕入れ先は複数あります」
	// 「単価は外してよいと思います」）。1列では複数の仕入先を表せず、価格は都度変わる
	// ため——仕入先と価格の正本は「材料屋の見積もり」（何件でも作れる）と
	// 「自社の発注書」（実際に発注したもの）です。
	// ユーザー:「ただし、**最新の単価や価格推移を知りたいことはあります。この情報は
	// DBに入っているべきです。そして必要な時に提示されるべきです**」——見積もりと
	// 発注書が既にその履歴を持っているので、**提示する側**（計算ビュー）が残件です。
	//
	// **進捗（発注済・納品済）もここには置きません**。ユーザー:「これは部品のページ
	// ではなく、受注ページで進捗の一部として見られると良い」——部品ページは
	// 「何が要るか」の定義、進捗は受注ごとの実績です。列名を `状態` ではなく `区分`
	// にしてあるのは、受注明細の `状態`（未着手／加工中／納品済）と取り違えないため。
	{
		Type:        "part-materials",
		DisplayName: "材料",
		Category:    "業務",
		Icon:        "🔩",
		// 列はユーザーの実務どおり——**材料に「名前」は無く、材質・形状・寸法の
		// 3つで決まります**（`SS400／板 t3.2／1000×500`）。③計算はこの3つを
		// 繋いだものを名前として扱います（materials.go の materialNameOf）。
		// 個数は**一台当たり**です（受注数を掛けるのは③計算の仕事）。
		//
		// 部材の行そのものには部品番号が無く、ページ横断メタの「部品番号」タグが
		// ページ全体の鍵になる。**この鍵は参照追従JOINへ移す予定**（作業引き継ぎ）
		// ——部品ページはページIDで同一性を持つので、本来このタグは要りません。
		Element:     "table",
		RequiresTag: "部品番号",
		Columns: []cms.VocabColumn{
			{Field: "material", Label: "材質", Type: cms.ColText},
			{Field: "shape", Label: "形状", Type: cms.ColText},
			{Field: "size", Label: "寸法", Type: cms.ColText},
			{Field: "quantity", Label: "個数", Type: cms.ColNumber},
			{Field: "note", Label: "備考", Type: cms.ColText},
			{Field: "status", Label: "区分", Type: cms.ColEnum, Enum: []string{"現行", "廃版"}},
		},
	},
	{
		// 外注加工——**番号がいちばん効く表**。加工先へ渡す紙に社内コードを載せ、
		// 相手からの問い合わせもその番号で受けられます。
		//
		// `資料` はユーザーの要望（「外注加工の場合、**加工業者に渡す資料を入れる
		// 場所も必要**です」）。値は同じページに貼った添付への参照
		// （`ページID-ブロックID`）を書きます——**表のセルの参照はまだリンクになりません**
		// （参照リンクの描画はタグの dl だけが対象。ref_render.go）。押して飛べる
		// ようにするのは残件です。
		Type:        "part-outsourcing",
		DisplayName: "外注加工",
		Category:    "業務",
		Icon:        "🏭",
		Element:     "table",
		Columns: []cms.VocabColumn{
			{Field: "work", Label: "加工内容", Type: cms.ColText},
			{Field: "supplied", Label: "支給", Type: cms.ColText},
			{Field: "quantity", Label: "個数", Type: cms.ColNumber},
			{Field: "doc", Label: "資料", Type: cms.ColText},
			{Field: "note", Label: "備考", Type: cms.ColText},
			{Field: "status", Label: "区分", Type: cms.ColEnum, Enum: []string{"現行", "廃版"}},
		},
	},
	{
		// 購入部品——**列は暫定**です。ユーザー:「購入品の項目はいまのところ
		// はっきりしません」（2026-09-03）。実物を入れてみて決まったら直します。
		Type:        "part-purchased",
		DisplayName: "購入部品",
		Category:    "業務",
		Icon:        "📦",
		Element:     "table",
		Columns: []cms.VocabColumn{
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			{Field: "spec", Label: "仕様", Type: cms.ColText},
			{Field: "quantity", Label: "個数", Type: cms.ColNumber},
			{Field: "note", Label: "備考", Type: cms.ColText},
			{Field: "status", Label: "区分", Type: cms.ColEnum, Enum: []string{"現行", "廃版"}},
		},
	},
	{
		// 支給部品——**客先から支給されるもの**（ユーザー:「ほかに支給部品という
		// 項目もあります」）。買わないので手配の対象ではありませんが、構成には要り、
		// 番号でも指せる必要があります。
		Type:        "part-supplied",
		DisplayName: "支給部品",
		Category:    "業務",
		Icon:        "🎁",
		Element:     "table",
		Columns: []cms.VocabColumn{
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			{Field: "spec", Label: "仕様", Type: cms.ColText},
			{Field: "quantity", Label: "個数", Type: cms.ColNumber},
			{Field: "note", Label: "備考", Type: cms.ColText},
			{Field: "status", Label: "区分", Type: cms.ColEnum, Enum: []string{"現行", "廃版"}},
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
