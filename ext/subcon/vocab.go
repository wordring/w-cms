package subcon

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
	// ではなく、受注ページで進捗の一部として見られると良い」——加工製品ページは
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
		// ——加工製品ページはページIDで同一性を持つので、本来このタグは要りません。
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
	// ⚠ **ヘッダだけの形式は 2026-09-18 に全廃しました**（ユーザー決定:「素の定義リストは
	// DBから外しましょう」「受注ページを作成するときにも、出来る限りタグを使いたい」）。
	// 廃したのは `client-order`（顧客の発注書）・`our-order`（弊社の発注書）・
	// `our-estimate`（弊社の見積もり）・`supplier-estimate`（材料屋の見積もり）と、
	// 前日までの `drawing`（図面）です。**値は可変タグへ**（`page_tags`）。
	//
	// **残すのは行が並ぶ表と、ビューの器だけ**になりました——説明は
	// **「タグと表だけがDBに入る」**の1文です。⚠ **1文書＝1ページ**が規則
	// （ユーザー:「発注書は一ページ一発注書で問題ない」）——ヘッダがページのタグに
	// なるので、1ページに同じ文書を2つ置けません。明細の表は複数あって構いません。
	//
	// ⚠ **表は `data-type` を自分で名乗ります。** 節の `Items` 宣言を頼りに
	// 「節の中の素の表」として見つける仕掛けは、コアから消えました。
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
			// ⚠ **`code` です**（2026-09-20・実データで確認）。ユーザー:「南北スポーツ
			// マシーンは品番を入れてこないので、これまでは**図番を品番として入れて**
			// きました」——つまり実際にこの列へ入るのは**図面番号**です。
			// `図面番号` は設定で `code`（空白・ハイフン・長音・大小を畳む）なのに
			// `品番` が `text` だと、**同じ番号が置き場所によって別の鍵になります**
			// ——`P103-227-6` を `p103 227 6` と打っても品番では当たらない。
			// 品番そのものもハイフンや全角の揺れを持つので、`code` が素直です。
			// ⚠ **`弊社品番` が先頭です**（2026-09-20 ユーザー:「加工中に番号を使うので、
			// 受注の表に**弊社品番として製造製品のページIDを入れたい**です」）。
			//
			// **値は加工製品ページのページ番号**で、型は `ref`——押せば飛びます
			// （⚠ **表のセルの参照はまだリンクになりません**。`RenderReferenceLinks` が
			// `dd` しか見ないためで、**索引には入るので検索は効きます**）。
			//
			// ⚠ **`品番` とは別の列です。** `品番` は**顧客の言葉**（この客先では
			// 図面番号が入る）、`弊社品番` は**弊社の識別**。同じ列に混ぜると、
			// 顧客の品番で問い合わせが来たときに引けません。
			//
			// **なぜページ番号か**——2026-09-03 ユーザー:「別の製品の図面番号が一致して
			// しまう場合もあり…**社内で識別番号を割り当てるしかありません**」。
			// w-cms は6桁を必ず振るので、その条件は構造として満たされています。
			// ⚠ **結ぶのは人**です（「文脈から担当者が糸をくみ取るので自動化は無理」）。
			{Field: "our-item-id", Label: "弊社品番", Type: cms.ColRef},
			{Field: "item-id", Label: "品番", Type: cms.ColCode},
			{Field: "item-name", Label: "品名", Type: cms.ColText},
			// ⚠ **並びは実物の発注書に寄せました**（2026-09-20・実データで確認）。
			// 先方の表は `No. / 品名 / サイズ / 図面番号 / 数量 / 単位 / 単価 / 金額` で、
			// **数量のすぐ隣が単位**です。見比べる人の目が滑らない並びにします。
			{Field: "quantity", Label: "数量", Type: cms.ColNumber},
			// ⚠ **`単位` を落とすと、数量の意味が変わります**（2026-09-20 ユーザー:
			// 「単位は南北様くらいしか使っていないのですが、**残念ながら弊社の表に
			// 入れざるを得ません**」）。値はたいてい `個` ですが、**まれに `セット`**
			// ——`数量: 100` が100個なのか100セットなのか分からなくなります。
			// **まれだからこそ危ない**: ほとんどの行で問題が起きないので、
			// **セットの行だけが黙って間違います**。
			//
			// 選択肢は**縛りではなく見分けるための表**です（語彙モデル §5.1）——
			// ここに無い単位も書けて、画面が色で知らせるだけ。
			{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: []string{"個", "セット"}},
			{Field: "price", Label: "単価", Type: cms.ColNumber},
			// ⚠ **`備考` は `状態` の手前**（既存4表——構成部品・加工工程・購入部品・
			// 支給部品——と同じ並び）。ユーザー:「表には『備考』欄が必要です」。
			//
			// ⚠ **顧客の備考とは別物です**。先方が書いたもの（「材質変更」）と、こちらの
			// 申し送り（「在庫から手配」）は別。初期値として写し、人が直せる形にします。
			// そして**人が弊社品番を結ぶとき読む欄**でもあります——同じ品名・同じ図番で
			// 別の品物を見分ける手掛かりは、たいていここに書いてあります。
			{Field: "note", Label: "備考", Type: cms.ColText},
			{Field: "status", Label: "状態", Type: cms.ColEnum, Enum: []string{"未着手", "加工中", "検査中", "納品済"}},
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
		Type:        "required-materials",
		DisplayName: "手配状況リスト",
		Category:    "ビュー",
		Icon:        "📊",
		Element:     "section",
		View:        true,
	},
}

// clientOrderItemColumns は受注明細の列を宣言から返します（見出し行を組むため）。
//
// ⚠ **見出しを手で書かないための口**です（2026-09-20）。列を足したのに本文の
// `<th>` が古いままだと、**宣言と本文が黙ってずれます**——索引は見出しの表示文字で
// 引くので、ずれた列はどこからも読めません。エラーも出ません。
func clientOrderItemColumns() []cms.VocabColumn {
	def, ok := cms.VocabDefByType("client-order-items")
	if !ok {
		return nil
	}
	return def.Columns
}
