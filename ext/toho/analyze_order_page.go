package toho

// 解析が作る案件ページの本文（2026-10-20 に analyze_pdf.go から分けた）——テンプレート「案件ページ」を埋める・顧客の発注書の読んだままの表。

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"

	"w-cms/internal/cms"
)

// OrderPageTemplate は受注ページ（2026-10-19 からの名前は案件ページ）を作るテンプレートの題です（2026-09-27）。
//
// ⚠ **受注ページの形はこのテンプレートが決めます**——タグの並び・原本PDFと写しの枠・
// 受注明細表の列の並び。機械が書くのは**題と値と行の数**だけです（利用者:「テンプレートには
// スラッシュメニューから表などの印を置き、コードはそれを埋めてはどうでしょう？」）。
// テンプレートが無ければ受注ページを作りません（`cms.ErrNoPageTemplate`）。
const OrderPageTemplate = "案件ページ"

// buildOrderPageHTML は受注ページの本文を、テンプレート tmpl を埋めて組みます。
//
// 埋めるもの（2026-09-27 からテンプレート駆動・形はテンプレートが持つ）:
//
//   - **タグ**（発注書番号・発注元・発注日・納期・小計・税・合計・由来）——`<dt>` の名前で
//     探して値を入れる。テンプレートに無いタグは、値があれば足す。
//   - **原本のPDF**——まだ配線されていないファイル表示の印へ（無ければ出さない）。
//   - **顧客の発注書（読んだまま）**——その名前の畳める枠（または見出しの節）の中へ表を
//     入れる。読めなければ枠ごと消す（無ければ出さない）。
//   - **案件明細表**（2026-10-19 までの名前は受注明細表）——キャプション `案件明細表` の表へ、見出しの言葉で列を合わせて明細の数だけ
//     行を作る。⚠ **表が無ければ作らない**（明細の行き場が無い）。
//
// 行の `状態` は「未着手」で始まります（進捗の起点）。
func buildOrderPageHTML(tmpl, hostPageID, attachID string, j *orderJudgment) (string, error) {
	title := "発注書 " + cms.NormalizeNameForIngest(j.OrderNo)
	if cms.NormalizeNameForIngest(j.OrderNo) == "" {
		if cms.NormalizeNameForIngest(j.Customer) != "" {
			title = "発注書（" + cms.NormalizeNameForIngest(j.Customer) + "）"
		} else {
			title = "発注書（番号不明）"
		}
	}

	// ── ヘッダは**可変タグ**、明細は**表**（2026-09-18 ユーザー決定）──
	//
	// ユーザー:「受注ページを作成するときにも、出来る限りタグを使いたいです。そのほうが
	// 編集者もDBに入っていると視覚的にわかるからです」「専用コードの全廃を前向きに
	// 検討してください」。
	//
	// **表とタグだけがDBに入る**——それが説明の全部になりました。機能見出しの節に
	// 素の定義リストを置く形（業務ブロックのヘッダ）はやめました。⚠ **1文書＝1ページ**が
	// 規則です（ユーザー:「発注書は一ページ一発注書で問題ない」）——ヘッダがページの
	// タグになるので、1ページに2つの発注書は置けません。
	d := cms.NewPageDraft(OrderPageTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindCustomerOrder, title)) // 題の頭に【顧客】（2026-10-16・page_kind.go）
	setHeaderTag(d.DraftBlock, OrderNoTag, cms.NormalizeNameForIngest(j.OrderNo))
	setHeaderTag(d.DraftBlock, OrderClientTag, cms.NormalizeNameForIngest(j.Customer))
	setHeaderTag(d.DraftBlock, OrderedAtTag, j.OrderDate)
	// ⚠ **納期は行ではなくここ**（2026-09-20・実データで確認）。1文書1ページなので、
	// 書面のヘッダにあるものはページのタグになります。
	//
	// ⚠ **日付とは限りません。** 実データの1通目が「**最短納期**」でした（ユーザー）。
	// 値はそのまま書きます——**取り込みは情報を捨てない**（D-3）。`納期` は辞書で
	// `date` だった（2026-09-23 から `text`）ので、日付として読めない値は**畳んだ値が付かないだけ**で、生の値は
	// 正本として残ります（語彙モデル §5.1「解釈できない値は併記しない。拒否もしない」）。
	//
	// ⚠ **`YYYY-MM-DD 形式で`とだけ頼んでいたころ、Gemini は空で返していました**
	// ——日付にできないので。プロンプトで「日付でない書き方は書かれているまま」と
	// 頼むまで、**書いてあるのに何も残らない**状態でした。
	setHeaderTag(d.DraftBlock, DueDateTag, j.DueDate)
	// ⚠ **検算の材料**（[checksum.go]）。空ならテンプレートの空欄のまま出ます——**書く場所が
	// 見えていれば人が埋められます**（発注書に書いてあるのに読めなかった場合、人が打てば
	// その場で検算が効きます。鏡型なので保存し直せば ⚠ が消えます）。
	setHeaderTag(d.DraftBlock, SubtotalTag, j.Subtotal)
	setHeaderTag(d.DraftBlock, TaxTag, j.Tax)
	setHeaderTag(d.DraftBlock, TotalTag, j.Total)
	// 由来参照（§9.1）——値は「元ページID-添付ID」。参照タグの文法（ref_render.go）に
	// 一致するのでリンクとして描画され、押すと元ページの該当ブロックへ飛ぶ。
	ref := hostPageID + "-" + attachID
	if attachID == "" {
		ref = hostPageID // メールの本文から作るとき（2026-10-01・analyze_mail.go）——由来はメールのページ全体
	}
	setHeaderTag(d.DraftBlock, SourceRefTag, ref)

	// ── 原本のPDF（2026-09-21 ユーザー決定）──
	//
	// ユーザー:「顧客の発注書（読んだまま）の上にPDFを表示できるようにします
	// （通常は折りたたむ）」。**写しは読み取りの結果で、PDFが原本そのもの**なので、
	// 並びは「原本 → 読んだまま → 弊社の明細」になります（並びはテンプレートが持つ）。
	//
	// 中身はコアが描きます（`internal/cms/file_view.go`）——**この拡張はPDFの
	// 出し方を知りません**。人が消せば出なくなり、`受信元` のタグ（出所の記録）は残ります。
	// ⚠ テンプレートに印が無ければ出しません（表示は飾りで、原本は元のページに在る）。
	if attachID != "" {
		d.SetFileView(ref)
	} else {
		d.DropFileView() // 原本のファイルが無い（メールの本文）——空の PDF の枠を残さない
	}

	// ── 顧客の発注書（読んだまま）──
	//
	// ⚠ **畳んで出します**（`<details>`・テンプレートの枠）。ユーザー:「顧客の表は、
	// 整理したあとも残します。**ボタンで畳めれば良い**と思います」。素のHTMLだけで畳める
	// ので、JS も `on*=` も要りません（CSP strict の下で動き、公開ページのゼロJSでも畳めます）。
	//
	// ⚠ **形式を登録していないので索引に載りません**（2026-09-20 の線引き）。
	// 原本は**証拠**であって、検索したいのは弊社の表のほうです——**二重計上も
	// 最初から起きません**。
	if box, ok := d.Container(sourceTableCaption); ok {
		if tbl := sourceTableHTML(j.SourceTable); tbl != "" {
			box.SetContent(tbl)
		} else {
			d.Remove(box.Node()) // ⚠ **読めなければ枠ごと出しません**（空の枠は誤解を生む）
		}
	}

	var rows []map[string]string
	for _, it := range j.Items {
		// ── 弊社の表は**正規形で書き起こします**（2026-09-21 ユーザー決定）──
		//
		// ユーザー:「品名が半角カナになっているので、正規化します。半角カナ以外も
		// 正規化します」。実データの品名が `ﾌﾞﾗｹｯﾄ` の形で届きました。
		//
		// ⚠ **半角カナのままだと、同じ品物が2つの綴りで並びます**——`ブラケット` と
		// `ﾌﾞﾗｹｯﾄ` は**画面では似て見えるのに、検索では別物**です。索引の畳んだ値
		// （`norm_value`）は NFKC を通るので引くほうは当たりますが、**人が読む値**
		// （`value`・表の見た目）は半角のまま残り、目で見比べる人が揺れます。
		//
		// 通すのは `NormalizeNameForIngest`（NFKC＋空白の詰め）で、`NormalizeCode`
		// ではありません——長音を潰すと `レーザー` が `レ-ザ-` になります。
		//
		// ⚠ **「機械は text を書き換えない」の例外です。** 理由は
		// [normalize_ingest.go] が図面名称で述べているのと同じで、**原本がページに
		// 出ているから**です——食い違えば、すぐ上の「読んだまま」の表と、その上の
		// PDF で見比べられます。原本の写しは**畳みません**（読んだままが正本）。
		//
		// 日付と数値は従来どおり正規形で書き起こします（D-3「正規化は取り込み時に
		// 行う」）。読めなければ生のまま入ります——取り込みは情報を捨てない。
		//
		// ⚠ **弊社品番・備考は空で出します。** 解析には決められません——
		// 弊社品番は人が文脈から結び、備考は発注書の様式しだいです（様式ページの
		// 対応表が入ったら、そこから埋まります）。**書く場所が見えていれば人が埋めます**
		// （図面ブロックで空欄の `客先` を出しているのと同じ理由）。
		rows = append(rows, map[string]string{
			"item-id":   cms.NormalizeNameForIngest(it.ItemNo),
			"item-name": cms.NormalizeNameForIngest(it.ItemName),
			"quantity":  cms.CanonicalForIngest("数量", it.Quantity),
			// ⚠ **単位も畳みます**——`ｾｯﾄ` のまま入ると選択肢（`個`／`セット`）の
			// どちらにも当たらず、**画面が「見慣れない単位」として色を付けます**。
			// まれにしか出ない単位ほど、揺れたまま気づかれません。
			"unit":  cms.NormalizeNameForIngest(it.Unit),
			"price": cms.CanonicalForIngest("単価", it.Price),
			// ⚠ **行の納期は、受注時はページの納期と同じ**（2026-09-20 ユーザー:
			// 「行の納期は、受注時にはタグの納期と同じです。**その後顧客の依頼や
			// 弊社の事情で個別に納期が変わることがあります**。すると行の納期を
			// 書き換えます」）。**配っておきます**——同じ日付をN回打たせる理由がなく、
			// 変わった行だけ直せば済みます。
			//
			// ページのタグは**先方が書いたこと**のまま残り、行は**弊社の予定**として
			// 動きます。⚠ 日付でない値（「最短納期」）もそのまま配ります——
			// 書いてあることを捨てないのが決まりです。
			// ⚠ **行の納期もページのタグと同じ作法を通します**（2026-09-21）。
			// それまでここだけ生のまま書いていたので、先方が `2026/10/15` と
			// 書いていると**ページのタグは `2026-10-15`、行は `2026/10/15`** と
			// 食い違いました。`date` として読めない値（「最短納期」）はそのまま残ります。
			"due": cms.CanonicalForIngest(DueDateTag, j.DueDate),
			// 出荷済数量は空が「まだ出していない」。⚠ **分納があるので要ります**
			// ——「100個のうち40個だけ出した」は `状態` だけでは表せません。
			// ⚠ **手続きの印は空で始めます**（材料発注・製造完了・納品書発行・請求書発行）。
			// 解析には分かりません——どれも**これから人がやること**です。
			"status": "未着手",
		})
	}
	// ⚠ **キャプションで名乗る表です**（2026-09-20 ユーザー:「弊社の受注表にも
	// キャプションが欲しいところです」）。原本の表と並ぶので、**どちらが何なのか
	// 見て分かる**必要があります。**列は見出しの言葉で合わせます**——テンプレートで列を
	// 並べ替えても・足しても崩れず、テンプレートに無い列は値があれば右端へ足します。
	if _, err := fillVocabTable(d.DraftBlock, clientOrderItemsType, rows); err != nil {
		return "", err
	}
	// ⚠ **言い換えたときは、そう書きます**（2026-09-21）。先方の「図面番号」を
	// 弊社の「品番」に入れたなら、その1行を表の手前に残します——**原本と見比べ
	// なくても気づける**のがここの目的です（0c の積み残し）。
	if note := itemNoSourceNote(j.Items); note != "" {
		if t, ok := d.Table(displayNameOf(clientOrderItemsType)); ok {
			d.InsertBefore(t, note)
		}
	}
	return d.HTML(), nil
}

// itemNoSourceNote は「弊社の `品番` を、先方のどの列から採ったか」の1行です。
//
// ⚠ **言い換えたときだけ書きます。** 先方にも「品番」の列があったなら、言い換えて
// いないので知らせることがありません——**毎回出る注意書きは読まれなくなります**
// （未処理一覧で一度学んだことです）。
//
// ⚠ **行ごとに違う列から採っていたら黙ります。** それは様式を読めていない印で、
// 「どの列から採ったか」を1行で言えません。**嘘を書くより黙るほうが正直**です
// ——そのときは原本の写しが手掛かりになります。
//
// ⚠ **本文に書きます（鏡型ではありません）。** 検算の ⚠ とは性格が違うためです:
// あちらは**いまの数字**についての判断なので、人が数字を直したら消えなければ
// なりません。こちらは**解析がそのとき何をしたか**という出来事で、あとから人が
// 品番を打ち直しても「解析はそこから採った」は真のままです。消したい人は消せます。
func itemNoSourceNote(items []orderPDFItem) string {
	src := ""
	for _, it := range items {
		s := cms.NormalizeNameForIngest(it.ItemNoSource)
		if s == "" {
			continue
		}
		if src == "" {
			src = s
			continue
		}
		if src != s {
			return "" // 行ごとに違う＝様式を読めていない
		}
	}
	if src == "" || src == itemNoLabel() {
		return "" // 知らせることが無い（言い換えていない・分からない）
	}
	return `<p>解析は、先方の「` + html.EscapeString(src) +
		`」を弊社の「` + html.EscapeString(itemNoLabel()) + `」に入れました。</p>`
}

// itemNoLabel は案件明細表（2026-10-19 までの名前は受注明細表）の `品番` 列の見出しを返します（宣言と同じ綴りを使うため）。
//
// ⚠ **生の文字列で持つと、列を改名した日にこの注意書きが常に出ます**——
// 「先方の『品番』を弊社の『品番』に入れました」という、意味の無い1行になります。
//
// ⚠ **変数ではなく関数です。** パッケージ変数の初期化は `init()` より**先**に
// 走るので、`var` で持つと語彙が登録される前に引くことになり、**常に空**に
// なります——空だと上の比較が素通りして、言い換えていないときも1行出ます。
func itemNoLabel() string { return labelOf(clientOrderItemsType, "item-id") }

// sourceTableHTML は顧客の発注書の写しの表を組みます（空なら何も出しません）。
// 畳める枠（`<details>`）はテンプレートが持ちます（2026-09-27）——ここは枠の中身だけ。
//
// ⚠ **`data-type` を付けません。** 形式を登録していない表は索引に載らない決まりなので
// （2026-09-20）、原本はそのまま「見せるだけ」になります。
//
// ⚠ **caption も付けません**（2026-09-28・利用者の選択）。それまでは「畳んだときに何の表か分かる
// ように」付けていましたが、**表の写し（`data/tables.db`・2026-09-25）はキャプションのある表を全部
// DB に入れる**ので、証拠の写しまで DB に入り、顧客の見出し（`No.` など）が保存のたびに「SQL で引くとき
// 引用符が要る」と告げられていました。何の表かは**外側の折りたたみの題**（テンプレートの
// 「顧客の発注書（読んだまま）」）が言います。検算はその枠の中の表を読みます（`sourceTableIn`）。
func sourceTableHTML(t orderSourceTable) string {
	if len(t.Headers) == 0 || len(t.Rows) == 0 {
		return "" // ⚠ **読めなければ出しません**（空の枠だけ出しても誤解を生む）
	}
	var b strings.Builder
	b.WriteString(`<table><tbody><tr>`)
	for _, h := range t.Headers {
		b.WriteString("<th>" + html.EscapeString(h) + "</th>")
	}
	b.WriteString("</tr>")
	for _, row := range t.Rows {
		b.WriteString("<tr>")
		for i := range t.Headers {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			// ⚠ **足りない列は空で埋めます**——列数が揃っていないと、見出しと値の
			// 対応が1つずつずれて**別の列の値に見えます**。
			b.WriteString("<td>" + html.EscapeString(v) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// sourceTableCaption は原本の写しを入れる枠（テンプレートの折りたたみ・見出しの節）の題です。
//
// ⚠ **語彙に登録しません**（登録すると索引に載り、弊社の明細と二重になります）。
// 人が読むための名前です。⚠ 2026-09-28 から表の caption には書きません（`sourceTableHTML`）——
// 名前の定数に "Caption" が残っているのは、それより前の本文の caption も読むためです（`sourceTableIn`）。
const sourceTableCaption = "顧客の発注書（読んだまま）"

// parseSourceTable は原本の写しを**寛容に**解きます（解けなければ空）。
//
// ⚠ **行の形が2通りあります。** 頼んでいるのは `[["1","品名",…]]`（配列の配列）ですが、
// **Gemini は見出しを鍵にした object で返すことがよくあります**
// （`[{"No.":"1","品名":"…"}]`）。どちらでも受けます。
//
// ⚠ **ここで失敗しても解析は止めません。** 構造に直接当てていたころは、**形が合わない
// だけで発注書番号も明細も丸ごと失われて**いました（実データの2通目で「解析に失敗
// しました」が出た原因）。**おまけのために本題を落とさない。**
func parseSourceTable(raw json.RawMessage) orderSourceTable {
	if len(raw) == 0 {
		return orderSourceTable{}
	}
	// ① 頼んだとおりの形（見出しの配列＋行の配列）。
	var direct struct {
		Headers []string          `json:"headers"`
		Rows    []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return orderSourceTable{}
	}
	out := orderSourceTable{Headers: direct.Headers}
	for _, r := range direct.Rows {
		// ②-a 行が配列（頼んだ形）。
		// 値は文字のほか、数・真偽・null で来ることがある（[1,"ブラケット",100,1200]）——1つずつ文字にする
		// （文字の配列としてしか読まず、数の混じった行が黙って消えていた・2026-10-22 の見直し）。
		var anyCells []any
		if err := json.Unmarshal(r, &anyCells); err == nil {
			cells := make([]string, len(anyCells))
			for i, c := range anyCells {
				cells[i] = jsonCellText(c)
			}
			out.Rows = append(out.Rows, cells)
			continue
		}
		// ②-b 行が object（見出しを鍵にしている）。**見出しの順に並べ直します**。
		var byKey map[string]any
		if err := json.Unmarshal(r, &byKey); err != nil {
			continue // この行だけ飛ばす（⚠ 1行のために表ごと捨てない）
		}
		row := make([]string, len(out.Headers))
		for i, h := range out.Headers {
			row[i] = jsonCellText(byKey[h])
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Headers) == 0 || len(out.Rows) == 0 {
		return orderSourceTable{}
	}
	return out
}

// jsonCellText は JSON の値をセルの文字へ直します（数も文字として扱う——原本の写しなので、
// 書かれていたとおりの見た目を残すのが目的）。
func jsonCellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		// ⚠ **整数は小数点を付けません**（`100` が `100.000000` になると原本と違う）。
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
