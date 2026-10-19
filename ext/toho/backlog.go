package toho

// ─────────────────────────────────────────────────────────────────────────
// 受注残表——納期順・顧客×納期ごとに1枚（2026-09-21）
//
// ユーザー:「**受注フォルダのトップページには、納期順の受注残表が必要です。**
// 顧客、納期ごとに別の表として分けて、**ワンタッチで印刷**もできるとありがたい」。
//
// ⚠ **顧客×納期の組が、そのまま納品の単位になります**（ユーザー決定）。だから
// 1枚が1回の納品に対応し、**そのまま納品書の下敷き**になります。
//
// ⚠ **製造順はここで決まります。** ユーザー:「製造順なんですが、**基本的には納期順**
// なので、受注明細の納期で決まります。同じ納期の品物同士で順番は作業者が決めます」
// ——つまり**並べる軸のために新しいデータは1つも要りません**。受注明細表を納期で並べる
// だけで、同じ納期の中の順は**人がその場で決める**ので記録しません
// （[docs/考察/【考察】製造という軸.md] §2.1・§4）。
//
// ⚠ **残っている行だけ出します**（`数量 - 出荷済数量 > 0`）。全部出すと、納品済みの
// 行に埋もれて「あと何を作るか」が見えません——**受注残表の役目はそこだけ**です。
//
// ⚠ **集めるのは `ページ種類：顧客発注書` のページ全部です**（2026-10-16・page_kind.go）。
// **どこに置いても同じものが見えます**（利用者:「どこにタグを置いても場所に依存せずみられる必要があります」・
// 【考察】全体の設計 §4.3）。それまでは「このビューを置いたページの子孫」でした——受注ページを
// 記録の年月（`記録／年／月／依頼`）へ移すと、受注フォルダの受注残表が黙って空になるためです。
// **箱も木の場所も見ません**——整理を待って通信記録の下にある受注ページも並びます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// BacklogViewType は受注残表の形式名です。
const BacklogViewType = "order-backlog"

// StatusDone は受注明細表の `状態` の「完了」です。
//
// ⚠ **これだけは、数を上書きする人の宣言です**（2026-09-21 ユーザー:「受注トップ
// ページの受注残集計表には、**完了状態の行は入れない**ようにしましょう」）。
// 他の値が「どこまで進んだか」を表すのに対し、`完了` は「**もう受注残に出さない**」。
//
// ⚠ **`納品済` とは別物です。** `納品済` は出したという事実で、残数があればまだ
// 受注残に出ます——半分だけ納めた行は、残り半分を作る必要があるからです。
// 残数があるのに終わりにしたい（客先が残りを取りやめた・短めの納入で了解を得た）
// ときに押すのが `完了` です。**機械の数より人の判断が上。**
const StatusDone = "完了"

// MarkDone は手続きの印の「済」です（材料発注・製造完了・納品書発行・請求書発行）。
//
// ⚠ **空が「まだ」、`済` が「もう済んだ」**の2値です。`0`/`1` のような機械の値を
// 使わないのは、**本文は人が読む正本**だからです——表を開いた人が、辞書を引かずに
// 意味を取れる必要があります。
//
// ⚠ **導出が入った日も、この値は生き続けます**（「導出 または 人の印」で表示）。
// ユーザー:「実際には納品書を発行していないのに発行したことにしたいときもある」。
const MarkDone = "済"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type:        BacklogViewType,
		DisplayName: "案件残",
		Category:    "ビュー",
		Icon:        "📋",
		Element:     "section",
		View:        true,
	})
	cms.RegisterView(BacklogViewType, backlogViewHTML)
}

// backlogRow は受注残の1行です。
type backlogRow struct {
	OrderPageID string // どの受注ページの行か（押せば飛べる）
	// RowNo は受注明細表の**データ行の番号**（見出しを除いて0から）。
	// ⚠ **書き戻しの照合に使います**（[order_edit.go]）——番号だけでは足りないので、
	// `ItemNo` と2つで当てます。
	RowNo   int
	OrderNo string
	// Machine は装置名です（2026-09-30・backlog_machine.go）——加工製品ページの置き場か、顧客の発注書の写しから。
	// MachineFromProduct は前者か（セルの説明に使う）。
	Machine            string
	MachineFromProduct bool
	OurItemNo          string
	ItemNo    string
	ItemName  string
	Quantity  int
	Shipped   int
	Remaining int
	// Price は単価です（2026-09-30・受注フォルダのトップの受注残高に使う・表には出さない）。HasPrice は読めたか。
	Price    float64
	HasPrice bool
	Status    string
	Note      string
	// 手続きの印（`済` か空）。⚠ **今日は人が押すだけです**——導出する元がまだ
	// ありません（[vocab.go] の宣言に経緯）。
	MaterialOrdered string
	Made            string // 製造完了（2026-10-17）
	DeliveryNote    string
	Invoice         string
	// Loss は赤字の見立ての1文です（2026-10-17・order_loss.go——受注の単価が見積の単価を下回る行だけ。空なら赤字でない）。
	Loss string
	// Quote は見積の依頼の案件（`ページ種類：顧客見積依頼`・2026-10-19）の行か——受注残高・赤字に入れず、納品できない。
	Quote bool
	// InMfg は製造ページに入れた数、MfgPages はその製造ページです（2026-10-19・mfg.go）。
	InMfg    int
	MfgPages []string
}

// backlogGroup は「顧客×納期」1組＝**1回の納品の単位**です。
type backlogGroup struct {
	Client string
	Due    string // 生の値（日付とは限らない——「最短納期」など）
	DueISO string // 日付として読めたときの並べ替え用（読めなければ空）
	Rows   []backlogRow
}

// backlogGroups は受注ページ（`ページ種類：顧客発注書`）から受注残を集め、顧客×納期で束ねて返します。
func backlogGroups(user *auth.User) []backlogGroup {
	gs, _ := backlogScan(user)
	return gs
}

// backlogScan は backlogGroups の本体です。飛ばした「移行中」の受注ページの数も返します。
//
// ⚠ **「移行中」の受注ページは数えません**（2026-09-29）——ワンノートとメールから過去の注文を
// 移すと、**納め済みの注文**も受注ページになります。確かめる前に受注残へ並ぶと、終わった仕事が
// 「残っている」に化けます。加工製品ページの「移行中」と同じく、**印が在るだけで止め**、人が
// 確かめて印を消したら並びます。⚠ **黙っては欠かさず、枚数を知らせます**（backlogViewHTML）。
//
// ⚠ **受注明細表を持っていても、`ページ種類：顧客発注書` の無いページは数えません**——種類はタグで名乗る
// 決まりです（2026-10-16）。今のページには道具で書き足しました（`デスクトップ\w-cms\道具\ページ種類_20261016\`）。
func backlogScan(user *auth.User) ([]backlogGroup, int) {
	db := database.DB
	// ⚠ **先に読み切ってから解釈します**——行を読みながら別のクエリを投げると、
	// `:memory:` では別の空DBに当たります（2026-09-03 に本番で踏んだ罠）。
	rows, err := db.Query(
		`SELECT DISTINCT v.page_id FROM vocab_index v
		  JOIN page_tags t ON t.page_id = v.page_id AND t.name = ? AND t.value IN (?, ?)
		 WHERE v.data_type = ?`, PageKindTag, PageKindCustomerOrder, PageKindCustomerQuote, clientOrderItemsType)
	if err != nil {
		return nil, 0
	}
	var pageIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			pageIDs = append(pageIDs, id)
		}
	}
	rows.Close()

	byKey := map[string]*backlogGroup{}
	migrating := 0
	machines := newMachineLookup()
	losses := newLossLookup(db, user) // 赤字の警告（2026-10-17・order_loss.go）
	// 出荷済数量は納品書から数える（2026-10-17・delivery_shipped.go——受注明細表の欄は読まない）。
	shippedBy := newShippedLookup(db)
	mfgBy := newMfgLookup() // 製造ページに入れた数（2026-10-19・mfg.go）
	quoted := newQuotedLookup(db) // 見積を出した見積の依頼の行（2026-10-19・quote_done.go）
	// 納品書発行・請求書発行の印は、発行済の納品書・請求書から数える（2026-10-19・issued_marks.go——人は書かない）。
	issuedDlv, issuedInv := newIssuedDeliveryLookup(db), newIssuedInvoiceLookup(db)
	for _, id := range pageIDs {
		// ⚠ **読めないページは黙って落とします**（見せ分け・C案）。
		if !page.CanView(user, id) {
			continue
		}
		tags, err := cms.TagsOfPage(db, id)
		if err != nil {
			continue
		}
		if _, ok := tags[MigratingTag]; ok {
			migrating++
			continue
		}
		client := cms.FirstTag(tags, OrderClientTag)
		quote := strings.TrimSpace(cms.FirstTag(tags, PageKindTag)) == PageKindCustomerQuote
		pageDue := cms.FirstTag(tags, DueDateTag)
		orderNo := cms.FirstTag(tags, OrderNoTag)

		items, err := cms.VocabTableRowsOf(db, id, clientOrderItemsType)
		if err != nil {
			continue
		}
		for _, r := range items {
			qty := r.Num("quantity")
			shipped := int(shippedBy.of(page.FormatID(id), r.Values["item-id"], r.Values["item-name"]))
			rem := qty - shipped
			// ⚠ **残っている行だけ**。`数量` が読めない行（0）も落ちます——
			// 数が無ければ「あと何個」が言えないので、残表には載せられません。
			if rem <= 0 {
				continue
			}
			// ⚠ **人が「完了」と言った行は、数が残っていても出しません**
			// （2026-09-21 ユーザー決定）。**機械の数より人の判断が上**という
			// 線引きは、このプロジェクトで一貫しています。
			if strings.TrimSpace(r.Values["status"]) == StatusDone {
				continue
			}
			// 見積の依頼は、見積書を出したら完了（2026-10-19 利用者:「見積依頼の案件は見積提出で完了です」）。
			if quote && quoted.of(page.FormatID(id), r.Values["item-id"], r.Values["item-name"]) > 0 {
				continue
			}
			due := strings.TrimSpace(r.Values["due"])
			if due == "" {
				// ⚠ **行に無ければページの納期**。受注時は同じ値が配られますが、
				// 人が行を空にすることもあるので、ページ側へ落ちます。
				due = pageDue
			}
			key := client + "\x00" + due
			g := byKey[key]
			if g == nil {
				iso, _ := cms.NormalizeValue(cms.ColDate, due)
				g = &backlogGroup{Client: client, Due: due, DueISO: iso}
				byKey[key] = g
			}
			inMfgF, mfgPages := mfgBy.of(page.FormatID(id), r.Values["item-id"], r.Values["item-name"])
			inMfg := int(inMfgF)
			machine, fromProduct := machines.machineOf(id, r.Values["our-item-id"], r.Values["item-id"], r.Values["item-name"])
			price, hasPrice := parseMoney(r.Values["price"])
			g.Rows = append(g.Rows, backlogRow{
				OrderPageID: page.FormatID(id),
				// ⚠ **索引の `RowNo` をそのまま使います。** 数え直すと、索引の側の
				// 数え方が変わった日にずれます（本文の行と1対1でなくなる）。
				RowNo:              r.RowNo,
				OrderNo:            orderNo,
				Machine:            machine,
				MachineFromProduct: fromProduct,
				OurItemNo:       strings.TrimSpace(r.Values["our-item-id"]),
				ItemNo:          strings.TrimSpace(r.Values["item-id"]),
				ItemName:        strings.TrimSpace(r.Values["item-name"]),
				Quantity:        qty,
				Shipped:         shipped,
				Remaining:       rem,
				Price:              price,
				HasPrice:           hasPrice,
				Status:          strings.TrimSpace(r.Values["status"]),
				Note:            strings.TrimSpace(r.Values["note"]),
				MaterialOrdered: strings.TrimSpace(r.Values["material-ordered"]),
				Made:            madeText(mfgBy.doneOf(page.FormatID(id), r.Values["item-id"], r.Values["item-name"]), qty), // 製造ページから（2026-10-19）
				DeliveryNote:    madeText(issuedDlv.of(page.FormatID(id), r.Values["item-id"], r.Values["item-name"]), qty),
				Invoice:         madeText(issuedInv.of(page.FormatID(id), r.Values["item-id"], r.Values["item-name"]), qty),
				Loss:            quoteless(quote, backlogLossText(losses, r.Values["our-item-id"], qty, price, hasPrice)),
				Quote:           quote,
				InMfg:           inMfg,
				MfgPages:        mfgPages,
			})
		}
	}

	out := make([]backlogGroup, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, *g)
	}
	sortBacklogGroups(out)
	return out, migrating
}

// sortBacklogGroups は納期順に並べます。
//
// ⚠ **日付として読めない納期を、後ろへ埋めません。** 実データの1通目が「**最短納期**」
// でした——いちばん急ぐものを最後に置くと、**見落とします**。日付でないものは
// **先頭**にまとめ、画面で「日付として読めない納期」と断ります。
// 「都度」のように急がないものも混じりますが、**急ぎを埋もれさせるより、
// 急がないものが目に付くほうが安全**です。
func sortBacklogGroups(gs []backlogGroup) {
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if (a.DueISO == "") != (b.DueISO == "") {
			return a.DueISO == "" // 読めないものが先
		}
		if a.DueISO != b.DueISO {
			return a.DueISO < b.DueISO
		}
		return a.Client < b.Client
	})
}

// linkBacklogRows は、受注残表に並ぶ行のうち `弊社品番` の空いているものを、いま在る加工製品ページで埋めます
// （埋めた行数を返す）。
//
// 利用者（2026-10-01）:「単純化しましょう。受注ページを作った時に弊社品番が無くリンクされなかった品物は、
// 受注フォルダを開いたタイミングで、品番を検索しページ番号を取得します。検索出来れば弊社品番がありますし、
// なければ依然として背景薄赤です。ページがあれば、受注ページにリンクを書き込むことが出来ます」
// 「管理者メニューで直すのは編集者にとってきつそうです」「受注フォルダを中心に作業するので問題ないはずです」。
//
// 結ぶのは作ったときと同じ `linkProductsToOrder`——**候補がちょうど1件のときだけ・人の入れた値は触らない**。
// 見つからない・2件以上なら空のまま（受注残表のセルは薄赤のまま）。
//
//   - ⚠ **書けない人は書かない**（その受注ページへの write が無い）——次に書ける人が開いたときに結ぶ。
//   - ⚠ **誰かが編集中の受注ページは飛ばす**（オートセーブと黙って上書きし合う）——次に開いたときに結ぶ。
//     2026-10-12 からブラウザは編集権を取らないので、飛ばすのは道具（`/api/lock`）が握っているときだけ。
//     ⚠ 本文は錠の外で読んで（`linkProductsToOrder`）丸ごと書き戻すので、同じ瞬間の人の保存が消える窓が
//     残っています（【考察】文書の食い違い §3.3）。
//   - 監査に残る（`order-item.linked`）。何がいつ自動で埋まったか追える。
//   - ⚠ **表示（GET）で書く**のはここだけ。書く中身は「空いた欄を、ただ1つの候補で埋める」だけなので、
//     誰が何度開いても結果は同じ（2回目からは何も書かない）。
func linkBacklogRows(user *auth.User, groups []backlogGroup) int {
	if user == nil {
		return 0
	}
	seen := map[string]bool{}
	filled := 0
	for _, g := range groups {
		for _, r := range g.Rows {
			if r.OurItemNo != "" || r.ItemNo == "" || seen[r.OrderPageID] {
				continue
			}
			seen[r.OrderPageID] = true
			id, err := strconv.Atoi(r.OrderPageID)
			if err != nil || !canWritePage(user, id) {
				continue
			}
			if _, open := editlock.Locks.EditorOpen(id); open {
				continue
			}
			for _, l := range linkProductsToOrder(user, r.OrderPageID) {
				filled += l.Rows
			}
		}
	}
	return filled
}

// backlogViewHTML は受注残表を組みます（サーバー事前描画）。
//
// ⚠ **置いたページは見ません**（2026-10-16）——どこに置いても同じ受注残が出ます。
func backlogViewHTML(user *auth.User, _ int) string {
	groups, migrating := backlogScan(user)
	// 空いている `弊社品番` を、いま在る加工製品ページで埋めてから描く（linkBacklogRows）。
	// 書いたら読み直す——索引は書いたときに直っている（RewriteBody → SyncIndex）。
	if linkBacklogRows(user, groups) > 0 {
		groups, migrating = backlogScan(user)
	}
	// ⚠ **移行中で飛ばした受注ページの枚数を言います**——言わないと「移した注文が出てこない」を
	//    集計の壊れと見分けられません。
	migNote := ""
	if migrating > 0 {
		migNote = `<p class="materials-empty">※「` + MigratingTag + `」の案件ページ ` + strconv.Itoa(migrating) +
			` 枚は入れていません（確かめたら、そのページの「` + MigratingTag + `」のタグを消すと並びます）。</p>`
	}
	if len(groups) == 0 {
		return `<p class="child-list-empty">案件残はありません（案件ページ〔` + PageKindTag + `：` + PageKindCustomerOrder +
			`〕に、まだ出していない明細がありません）。</p>` + migNote
	}
	var b strings.Builder
	b.WriteString(backlogTotalHTML(backlogTotalOf(groups)))
	b.WriteString(migNote)
	// 選んだ行で作業を始める帯。紙には出さない（no-print——案件残表は1枚ずつ印刷する作業の紙）。配線は assets/app.js。
	// ⚠ 「☑ 選んだ行を部材見積依頼へ」（2026-10-04〜）は 2026-10-19 に外した——部材見積依頼は製造ページから（mfg_more.go）。
	// 2026-10-19 利用者:「案件残表の「部材見積依頼」の欄を外して、部材見積依頼の置き場を消してください」
	b.WriteString(`<div class="backlog-rfq-bar no-print">` +
		// 選んだ行で納品書を作る（2026-10-17・delivery.go）——押すと、選んだ行と今回納める数（残りが初めから入っていて直せる）の欄が開く。
		`<button type="button" class="backlog-dlv-go">🚚 選んだ行で納品書を作る</button> <span class="backlog-dlv-say"></span>` +
		// 選んだ行で製造を始める（2026-10-19・mfg.go）——押すと、選んだ行と今回作る数（まだ製造に入っていない数が初めから入っていて直せる）の欄が開く。
		` <button type="button" class="backlog-mfg-go">🏭 選んだ行で製造開始</button> <span class="backlog-mfg-say"></span>` +
		`<div class="backlog-dlv-panel" hidden></div><div class="backlog-mfg-panel" hidden></div></div>`)
	for i, g := range groups {
		b.WriteString(`<section class="backlog-sheet" data-backlog="` + strconv.Itoa(i) + `">`)
		title := g.Client
		if title == "" {
			title = "（発注元なし）"
		}
		due := g.Due
		if due == "" {
			due = "（納期なし）"
		}
		b.WriteString(`<h3 class="backlog-title">` + stdhtml.EscapeString(title) +
			`　納期 ` + stdhtml.EscapeString(due))
		if g.DueISO == "" {
			// ⚠ **黙って並べない。** 日付として読めなかったことを言います。
			b.WriteString(`<span class="backlog-warn">⚠ 日付として読めない納期</span>`)
		}
		b.WriteString(`</h3>`)
		// 印刷ボタンは**クローム**なので保存されません。配線は assets/app.js。
		b.WriteString(`<button type="button" class="backlog-print" ` +
			`data-backlog-print="` + strconv.Itoa(i) + `">🖨 この表を印刷</button>`)
		// ⚠ **見出しも同じ印を付けます**——付け忘れると、紙で**見出しと値が1つずつ
		// ずれます**（列が消えるのは値の側だけなので、いちばん気づきにくい壊れ方）。
		// 装置名は左端（2026-09-30 利用者:「受注残の表に装置名も入れたい」）。紙にも出す（同日夜 利用者:「装置名は紙にも出して
		// 欲しいです」——紙の列は下の注記の6つ＋装置名の7つ）。
		b.WriteString(`<table class="backlog-table"><tbody>` +
			`<tr><th>装置名</th><th>弊社品番</th><th>品番</th><th>品名</th><th>残</th>` +
			`<th class="no-print">数量</th><th class="no-print">出荷済数量</th>` +
			`<th>状態</th>` +
			`<th class="no-print">材料発注</th><th class="no-print">製造完了</th><th class="no-print">納品書発行</th>` +
			`<th class="no-print">請求書発行</th>` +
			`<th>備考</th><th class="no-print">まだ無いもの</th><th class="no-print">案件</th><th class="no-print">製造</th><th class="no-print">納品</th></tr>`)
		for _, r := range g.Rows {
			// ⚠ **折り返しの印はサーバーが付けます。** 本文の表は `app.js` の
			// `validateTypedTables` が付けますが、**あれはサーバー所有の表を意図的に
			// 飛ばします**（クロームは殻の持ち物）。ここで付けないと、受注残表だけ
			// 全列が折り返します（2026-09-21 ユーザー:「備考以外は折り返さない
			// ようにしましょう。はみ出した部分はスクロールできるように」）。
			//
			// ⚠ **class 名は本文の表と同じものを使います**（`cell-atomic`／`cell-wrap`）
			// ——見た目の規則を2つ持つと、片方だけ直した日にずれます。横スクロールは
			// `#w-editor-content table` が全表に効かせています。
			// ⚠ **紙に出す列は6つ**（2026-09-21 ユーザー:「受注残表の印刷は、
			// **品番、品名、残、状態、備考だけ**で良いです」→ 同日**`弊社品番` を足しました**:
			// 「**品番はあいまいさがあるうえ、顧客の番号であるため、こちらで修正できません。
			// そこで弊社品番を併記してあいまいさなく作業できるようにします**」）。
			// ⚠ **これは作業の紙です**——手元であいまいなく引ける番号が要ります。
			// 画面では全部見せ、**紙でだけ落とします**——`no-print` の印を付け、
			// 隠すのは印刷用CSSの仕事です。
			// ⚠ 列を落とすのをサーバーで分岐させない（画面と紙で2つのHTMLを持つと、
			// 片方だけ直した日にずれます）。
			// ⚠ **ここから受注ページの本文を書き換えます**（2026-09-21 ユーザー:
			// 「受注残集計表を鏡にして受注ページの受注明細表が編集されるように」）。
			// 部品には**どの受注のどの行か**を持たせます——照合は行番号と `品番` の
			// 2つで、いまの値も一緒に送ります（[order_edit.go]・配線は assets/app.js）。
			//
			// ⚠ **鍵の列と計算の列は部品にしません**（`弊社品番`・`品番`・`品名`・`残`）。
			// 鍵を書き換えると**次の書き込みが隣の行に当たります**。
			st := statusOptionsHTML(r.Status)
			// 赤字の行は薄赤と ⚠（2026-10-17・利用者:「2か所で良いと思います。」——受注ページの明細と、この受注残表）。
			b.WriteString(`<tr` + rowAttrs(r) + lossRowAttr(r) + `>` +
				machineCellHTML(r) +
				ourItemCellHTML(r.OurItemNo) +
				atomicCell(stdhtml.EscapeString(r.ItemNo)) +
				atomicCell(stdhtml.EscapeString(r.ItemName)+lossNoteHTML(r)) +
				`<td class="cell-atomic backlog-remaining">` + strconv.Itoa(r.Remaining) + `</td>` +
				noPrintCell(strconv.Itoa(r.Quantity)) +
				// 出荷済数量は納品書から数えるので、ここでは書けない（2026-10-17）。
				`<td class="cell-atomic no-print" title="納品書から数えた数">` + shippedCell(r.Shipped) + `</td>` +
				`<td class="cell-atomic">` + st + `</td>` +
				noPrintCell(checkFieldHTML("材料発注", r.MaterialOrdered)) +
				`<td class="cell-atomic no-print" title="製造ページの製造完了から数えた">` + stdhtml.EscapeString(r.Made) + `</td>` + // 2026-10-19 から鏡（製造ページで付ける）
				// 2026-10-19 から鏡（発行済の納品書・請求書から数える・issued_marks.go）。
				`<td class="cell-atomic no-print" title="発行済の納品書から数えた">` + stdhtml.EscapeString(r.DeliveryNote) + `</td>` +
				`<td class="cell-atomic no-print" title="発行済の請求書から数えた">` + stdhtml.EscapeString(r.Invoice) + `</td>` +
				// ⚠ **備考だけ折り返します**（自由文なので1行に保つと表が果てしなく伸びる）。
				`<td class="cell-wrap">` + textFieldHTML("備考", r.Note) + `</td>` +
				// まだ埋まっていない段（2026-10-17・直す順の10番）——知らせるだけで、止めない。
				`<td class="cell-atomic no-print backlog-gap">` + stdhtml.EscapeString(strings.Join(backlogGaps(r), "・")) + `</td>` +
				noPrintCell(`<a href="/`+stdhtml.EscapeString(r.OrderPageID)+`">`+
					stdhtml.EscapeString(orderLabel(r))+`</a>`) +
				noPrintCell(mfgCellHTML(r, g.Client)) +
				noPrintCell(deliveryPickHTML(r, g.Client)) +
				`</tr>`)
		}
		b.WriteString(`</tbody></table></section>`)
	}
	return b.String()
}

// atomicCell は折り返さないセルを組みます（中身は組み済みのHTML）。
func atomicCell(inner string) string { return `<td class="cell-atomic">` + inner + `</td>` }

// noPrintCell は**画面には出すが紙には出さない**セルです。
//
// ⚠ **サーバーで列を分岐させません。** 画面用と紙用に2つのHTMLを組むと、片方だけ
// 直した日にずれます。**同じHTMLに印を付け、隠すのは印刷用CSSの仕事**にします。
func noPrintCell(inner string) string {
	return `<td class="cell-atomic no-print">` + inner + `</td>`
}

// ourItemCellHTML は弊社品番のセルです。⚠ **空なら薄赤**にします（2026-09-25）。
//
// ユーザー:「受注したのに加工製品のページが無い場合、それは作れないわけですから、
// 何かが間違っています」「**弊社品番なしの警告は受注フォルダページの納期別の表の
// 弊社品番セルの背景を薄赤くする程度で十分**」。⚠ **警告はここだけ**です——
// 受注ページの足元や必要部材表に文を足す案は、同日に取り下げました。
//
// ⚠ 判定は「空かどうか」だけです（利用者の言葉どおり「弊社品番なし」）。`品番` が
// 加工製品ページに当たれば必要部材表には出ますが、この表は**作業の紙**で、
// 手元であいまいなく引ける番号が要る（下の印刷の注記）——空は空として知らせます。
func ourItemCellHTML(id string) string {
	if strings.TrimSpace(id) == "" {
		return `<td class="cell-atomic backlog-no-item" title="弊社品番が空です` +
			`（加工製品ページがまだ無いか、結んでいません）"></td>`
	}
	return `<td class="cell-atomic">` + refCellHTML(id) + `</td>`
}

// refCellHTML は弊社品番を押せる形にします（空なら空欄のまま）。
//
// ⚠ **`RenderReferenceLinks` は通りません**——あれは本文の表だけを見るので、
// クロームの中には掛かりません。ここで自分でリンクにします（サニタイズの**後**に
// HTMLを足す関数は、自分でエスケープの責任を負う、という既存の規律のとおり）。
func refCellHTML(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return `<a href="/` + stdhtml.EscapeString(id) + `">` + stdhtml.EscapeString(id) + `</a>`
}

// shippedCell は出荷済数量を出します（0 は空欄——「まだ出していない」の見た目を揃える）。
func shippedCell(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// backlogGaps は、その行でまだ埋まっていない段を返します（2026-10-17・【考察】全体の設計 §3.3・§3.4・直す順の10番）。
//
// 2026-10-16 利用者:「段の順を強制しない。欠けた段は、あとから埋められる。価格は、決まった所に書く。で良いです。」
//
//   - 発注書番号 … 受注ページの `発注書番号` が空（発注書なしの製造依頼。あとから発注書が来れば書く）
//   - 単価       … 受注明細の `単価` が空か読めない（価格は、決まったときに書く）
//
// ⚠ **知らせるだけです。** 色も赤にしません（赤は「このままでは困る」——弊社品番の空き・赤字）。
// 弊社品番の空きは、弊社品番のセルの薄赤がすでに知らせているので、ここには並べません。
// 見積の無い注文・納品書の無い行は、ふつうに起きる形なので出しません（見積なしの注文がある・受注残の行はまだ納めていない）。
func backlogGaps(r backlogRow) []string {
	var gaps []string
	if strings.TrimSpace(r.OrderNo) == "" {
		gaps = append(gaps, OrderNoTag)
	}
	if !r.HasPrice {
		gaps = append(gaps, "単価")
	}
	return gaps
}

// orderLabel は受注の見出しです（発注書番号があればそれ、無ければページID）。
func orderLabel(r backlogRow) string {
	label := r.OrderPageID
	if r.OrderNo != "" {
		label = r.OrderNo
	}
	if r.Quote {
		return "見積 " + label // 見積の依頼の案件（2026-10-19）
	}
	return label
}

// ── 編集できる部品（2026-09-21）──────────────────────────────────────────
//
// ⚠ **クロームなので保存されません。** `<input>` や `<select>` を本文へ書くと
// サニタイズで落ちますが、ここはサーバーが描くクロームなので置けます——
// そして**シリアライザが保存しない**ので、本文は綺麗なままです。
//
// ⚠ **部品には「どの受注のどの行か」と「いまの値」を持たせます。** 送るときに
// 一緒に出して、サーバーが**行番号・品番・いまの値の3つ**で照合します
// （[order_edit.go]）。配線は assets/app.js。

// rowAttrs は行に「どの受注のどの行か」を書きます。
func rowAttrs(r backlogRow) string {
	return ` data-order-page="` + stdhtml.EscapeString(r.OrderPageID) + `"` +
		` data-order-row="` + strconv.Itoa(r.RowNo) + `"` +
		` data-order-item="` + stdhtml.EscapeString(r.ItemNo) + `"`
}

// fieldAttrs は部品に「どの列か」と「いまの値」を書きます。
//
// ⚠ **`data-old` が compare-and-swap の片割れ**です。これが無いと、二人が同じ表を
// 見ているとき**後から押したほうが黙って勝ちます**。
func fieldAttrs(field, cur string) string {
	return ` data-field="` + stdhtml.EscapeString(field) + `"` +
		` data-old="` + stdhtml.EscapeString(cur) + `"`
}

// checkFieldHTML は手続きの印のチェックボックスです（`済` か空の2値）。
func checkFieldHTML(field, cur string) string {
	checked := ""
	if strings.TrimSpace(cur) == MarkDone {
		checked = " checked"
	}
	return `<input type="checkbox" class="order-edit order-check"` +
		fieldAttrs(field, cur) + checked + ` aria-label="` + stdhtml.EscapeString(field) + `">`
}

// statusOptionsHTML は状態の選択肢です（⚠ **選択肢は宣言から**——画面に書き写すと、
// 語を足した日に片方が古くなります）。
func statusOptionsHTML(cur string) string {
	var b strings.Builder
	b.WriteString(`<select class="order-edit order-status"` + fieldAttrs("状態", cur) + `>`)
	seen := false
	for _, v := range statusEnum() {
		sel := ""
		if v == cur {
			sel, seen = " selected", true
		}
		b.WriteString(`<option` + sel + `>` + stdhtml.EscapeString(v) + `</option>`)
	}
	// ⚠ **一覧に無い値も残します**（語彙は入力補助であって検査ではない・§7.1）。
	// 落とすと、人が打った値が開いた瞬間に消えます。
	if !seen && strings.TrimSpace(cur) != "" {
		b.WriteString(`<option selected>` + stdhtml.EscapeString(cur) + `</option>`)
	}
	if strings.TrimSpace(cur) == "" {
		b.WriteString(`<option selected value=""></option>`)
	}
	b.WriteString(`</select>`)
	return b.String()
}

// statusEnum は `状態` の選択肢を宣言から返します。
func statusEnum() []string {
	def, ok := cms.VocabDefByType(clientOrderItemsType)
	if !ok {
		return nil
	}
	for _, c := range def.Columns {
		if c.Label == "状態" {
			return c.Enum
		}
	}
	return nil
}

// numberFieldHTML は数を打つ欄です（出荷済数量）。
func numberFieldHTML(field, cur string) string {
	return `<input type="text" inputmode="numeric" class="order-edit order-num"` +
		fieldAttrs(field, cur) + ` value="` + stdhtml.EscapeString(cur) + `"` +
		` aria-label="` + stdhtml.EscapeString(field) + `">`
}

// textFieldHTML は自由文の欄です（備考）。
func textFieldHTML(field, cur string) string {
	return `<input type="text" class="order-edit order-text"` +
		fieldAttrs(field, cur) + ` value="` + stdhtml.EscapeString(cur) + `"` +
		` aria-label="` + stdhtml.EscapeString(field) + `">`
}

// quoteless は見積の依頼の行なら赤字の見立てを消します（まだ注文ではないので赤字ではない・2026-10-19）。
func quoteless(quote bool, loss string) string {
	if quote {
		return ""
	}
	return loss
}
