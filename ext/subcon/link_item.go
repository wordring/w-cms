package subcon

// ─────────────────────────────────────────────────────────────────────────
// 品番から製造製品ページを特定し、受注明細の `弊社品番` を埋める（2026-09-21）
//
// ユーザー:「**弊社品番を入れるために、品番から製造製品ページを特定する必要が
// あります**」「仮に製造製品ページがまだつくられていない場合、検索しても何も出て
// こないので埋められません。とはいえ、**注文が入っている以上、近日中に製造製品
// ページが出来るはず**です。そのタイミングで検索して埋めることになると思います。
// **できれば機械的にやってほしいです**」。
//
// ⚠ **解析の瞬間には結べません。** 発注書が先に届き、図面はあとから来る（あるいは
// 数か月前に来ている）。だから引き金は**加工製品ページが置かれたとき**に置きます
// ——整理の実行、つまり**人の操作の直後**です。**裏で走る仕事は作りません**
// （Gemini も解析も「人が押した直後だけ」という既存の流儀に揃えます）。
//
// **照合はタグ名を問いません。** 加工製品ページ側の手掛かりは客先によって違います:
//
//	みなと商店のように図番で発注してくる客先 … ページの `図面番号` に当たる
//	品番で発注してくる客先               … ページの `品番` に当たる
//	図面の無い製品                       … `品番` しか無い
//
// 見る名前は設定（`extensions.subcon.product_code_tags`）が正本です。
//
// ⚠ **歯止めが3つあります。** 誤って別の製品に結ぶと、**現場が別物を作ります**
// ——取り返しが利かない側の誤りなので、自動で埋める以上は必ず要ります:
//
//  1. **候補がちょうど1件のときだけ埋めます。** 2件以上は触りません——「同じ図番で
//     別の品物」が実在することは 2026-09-20 に確かめたとおりです。
//  2. **編集ロックの関門**（誰かがその受注ページを開いていたら、その1枚は飛ばす）。
//  3. **監査に残します**（`order-item.linked`）。何がいつ自動で埋まったか追えるように。
//
// ⚠ 3つ目の歯止めとして**品名の食い違いを ⚠ で知らせる**のは鏡の側の仕事です
// （[checksum_mirror.go] と同じ流儀・本文には書きません）。**1件だけ当たったが
// それは別物だった**、という最後に残る誤りはそこで気づきます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// productCodesOf は加工製品ページが持つ「相手が指す番号」を集めます。
func productCodesOf(pageIDInt int) []string {
	tags, err := cms.TagsOfPage(database.DB, pageIDInt)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range ProductCodeTags() {
		for _, v := range tags[name] {
			v = strings.TrimSpace(v)
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// ProductPagesByCode は、その番号を持つ加工製品ページを返します（タグ名は問わない）。
//
// ⚠ **畳んだ一致で引きます**（`PagesByTagLoose`）。`P103-227-6` を `P103 227 6` と
// 打っても当たる——`code` 型は空白・ハイフン・長音・大小を畳むためです。
func ProductPagesByCode(value string) []int {
	return productPagesByCodeDB(database.DB, value)
}

// orderPagesWithItemNo は、その品番の明細行を持つ受注ページを返します。
//
// ⚠ **索引を引きます**（`vocab_index`）。受注明細は登録済みの形式なので載っています。
// 畳み方は列の宣言（`品番` は `code`）と同じものを通すこと——別の畳み方で引くと、
// **エラーにならず0件**になります。
func orderPagesWithItemNo(value string) []int {
	norm, ok := cms.NormalizeValue(cms.ColCode, value)
	if !ok || norm == "" {
		return nil
	}
	rows, err := database.DB.Query(
		`SELECT DISTINCT page_id FROM vocab_index
		  WHERE data_type = ? AND field = ? AND norm_value = ?`,
		clientOrderItemsType, ItemNoTag, norm)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// LinkOrdersToProduct は、この加工製品ページを指すべき受注行の `弊社品番` を埋めます。
//
// 埋めた行数を返します（0 なら何も書いていません）。**引き金は整理の実行**——
// つまり人の操作の直後だけです。
func LinkOrdersToProduct(user *auth.User, productPageID string) int {
	id, ok := page.NormalizeID(productPageID)
	if !ok {
		return 0
	}
	productPageID = id
	productInt, err := strconv.Atoi(id)
	if err != nil {
		return 0
	}
	codes := productCodesOf(productInt)
	if len(codes) == 0 {
		return 0
	}

	filled := 0
	done := map[int]bool{}
	for _, code := range codes {
		// ⚠ **1件でなければ触りません。** 同じ番号の加工製品ページが2枚あるなら、
		// どちらを指すべきかは機械には決められません（2026-09-20 の結論）。
		if cands := ProductPagesByCode(code); len(cands) != 1 || cands[0] != productInt {
			continue
		}
		for _, orderInt := range orderPagesWithItemNo(code) {
			if done[orderInt] {
				continue
			}
			done[orderInt] = true
			orderID := page.FormatID(orderInt)
			// ⚠ **開かれているページは飛ばします**——オートセーブと黙って
			// 上書きし合うためです（2026-09-14 の決定）。次の整理で拾えます。
			if _, open := editlock.Locks.EditorOpen(orderInt); open {
				continue
			}
			body, err := cms.ReadPageBody(orderID)
			if err != nil {
				continue
			}
			fixed, n := fillOurItemNo(body, code, productPageID)
			if n == 0 {
				continue
			}
			if err := cms.RewriteBody(orderID, user.Username,
				func(string) string { return fixed }); err != nil {
				continue
			}
			auth.Audit(user.Username, "order-item.linked",
				orderID+" "+code+" -> "+productPageID)
			filled += n
		}
	}
	return filled
}

// fillOurItemNo は受注明細の空いている `弊社品番` を埋めます（埋めた行数を返す）。
//
// ⚠ **空いている行だけ**です。人が入れた値は上書きしません——機械の推測より
// 人の判断が上、という線引きはこのプロジェクトで一貫しています。
func fillOurItemNo(body, code, productPageID string) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	want, ok := cms.NormalizeValue(cms.ColCode, code)
	if !ok {
		return body, 0
	}
	filled := 0
	for _, t := range tablesOfType(nodes, clientOrderItemsType) {
		filled += fillTable(t, want, productPageID)
	}
	if filled == 0 {
		return body, 0
	}
	return htmldoc.Render(nodes), filled
}

// isOrderItemsTable は受注明細の表かを見ます（属性でも caption でも——`isTableOfType`）。
func isOrderItemsTable(t *html.Node) bool { return isTableOfType(t, clientOrderItemsType) }

// fillTable は見出しから列を割り出し、当てはまる行を埋めます。
func fillTable(t *html.Node, wantNorm, productPageID string) int {
	rows := rowsOf(t)
	if len(rows) < 2 {
		return 0
	}
	col := headerIndex(rows[0])
	ourCol, okOur := col[OurItemNoTag]
	itemCol, okItem := col[ItemNoTag]
	if !okOur || !okItem {
		return 0
	}

	filled := 0
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if ourCol >= len(cells) || itemCol >= len(cells) {
			continue
		}
		// ⚠ **入っている値は触りません**（人が結んだかもしれない）。
		if strings.TrimSpace(textOf(cells[ourCol])) != "" {
			continue
		}
		got, ok := cms.NormalizeValue(cms.ColCode, strings.TrimSpace(textOf(cells[itemCol])))
		if !ok || got != wantNorm {
			continue
		}
		setCellText(cells[ourCol], productPageID)
		filled++
	}
	return filled
}

// ── 引き（表示のときに気づかせる）────────────────────────────────────────
//
// ⚠ **押し出し（整理の直後に埋める）だけでは取りこぼします**。取りこぼすのは3つ:
//
//	・整理のとき、その受注ページを誰かが開いていた（ロックで飛ばした）
//	・加工製品ページを整理を通さず**手で作った**
//	・ページの `品番` を**あとから人が書いた**
//
// どれも**黙って埋まらないまま**になるので、開いたときに見せます。
// ⚠ **ここでは書きません**——読むだけの操作（GET）で本文を書き換えると、
// オートセーブと衝突しますし、「見ただけで変わる」は追いにくい壊れ方です。

// ItemNameTag は受注明細の品名の列です（食い違いの検査に使います）。
const ItemNameTag = "品名"

// orderLinkNotes は結びについての気づきを返します（無ければ空）。
//
// 見るのは2つで、**捕まえるものが違います**:
//
//	空いている行 … 結べる相手が居るのに空のまま（押し出しの取りこぼし）
//	埋まった行   … ⚠ **結び先の題と品名が食い違う**（誤って別の製品に結んだ疑い）
//
// ⚠ **2つ目が「1件だけ当たったが、それは別物だった」の最後の砦**です。機械が
// 黙って埋める以上、**当たったこと自体を疑う目**がどこかに要ります。
func orderLinkNotes(db cms.ReadOnlyDB, viewer *auth.User, table *html.Node) []string {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	ourCol, okOur := col[OurItemNoTag]
	itemCol, okItem := col[ItemNoTag]
	if !okOur || !okItem {
		return nil
	}
	nameCol, hasName := col[ItemNameTag]

	var notes []string
	for n, tr := range rows[1:] {
		cells := cellTexts(tr)
		at := func(i int, ok bool) string {
			if !ok || i < 0 || i >= len(cells) {
				return ""
			}
			return cells[i]
		}
		our, code := at(ourCol, true), at(itemCol, true)
		name := at(nameCol, hasName)
		label := strconv.Itoa(n+1) + "行目"
		if name != "" {
			label += "「" + name + "」"
		}

		if our == "" {
			if code == "" {
				continue
			}
			// ⚠ **1件のときだけ言います。** 2件以上あるなら機械には決められないので、
			// 中途半端に名指しするとかえって誤らせます。
			cands := visibleOnly(viewer, productPagesByCodeDB(db, code))
			if len(cands) != 1 {
				continue
			}
			notes = append(notes, label+"の弊社品番が空です（品番 "+code+" は "+
				page.FormatID(cands[0])+" "+cms.PageTitleByID(cands[0])+" と思われます）")
			continue
		}

		// ⚠ **埋まっている行は、結び先が本当にその品物かを見ます。**
		if !hasName || name == "" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(our))
		if err != nil || !page.CanView(viewer, id) {
			continue
		}
		title := cms.PageTitleByID(id)
		if title == "" || titleMentions(title, name) {
			continue
		}
		notes = append(notes, "⚠ "+label+"は "+page.FormatID(id)+" に結ばれていますが、"+
			"そのページの題は「"+title+"」です（別の製品に結んでいませんか）")
	}
	return notes
}

// titleMentions は加工製品ページの題が品名を含むかを見ます。
//
// ⚠ **完全一致では見ません。** 題は「図面番号 図面名称」の形なので
// （`K120-01-211 留めブラケット`）、品名はその一部にしか出ません。
// 畳んでから含むかどうかを見ます——揺れで毎回 ⚠ が出ると、狼少年になります。
func titleMentions(title, name string) bool {
	t := cms.NormalizeCode(title)
	n := cms.NormalizeCode(name)
	return n != "" && strings.Contains(t, n)
}

// productPagesByCodeDB は鏡の読み取り専用DBで引きます。
//
// ⚠ **鏡には書き込みTxを渡さない**という型の約束があるので（walk.go 冒頭）、
// 押し出しの側と口を分けています。見る名前は設定（`product_code_tags`）が正本です。
func productPagesByCodeDB(db cms.ReadOnlyDB, value string) []int {
	return pagesByAnyTag(db, ProductCodeTags(), value)
}

// visibleOnly は閲覧者が読めるページだけを残します（見せ分け・C案）。
func visibleOnly(viewer *auth.User, ids []int) []int {
	var out []int
	for _, id := range ids {
		if page.CanView(viewer, id) {
			out = append(out, id)
		}
	}
	return out
}


// LinkProductsToOrder は受注ページの空いている `弊社品番` を、いま在る加工製品ページで
// 埋めます（埋めた行数を返す）。**押し出しの逆向き**です。
//
// ⚠ **こちらが本命です。** 引き金を整理（図面）だけに置いていたとき、
// **図面が先に届いて発注書が後から来る場合に一度も走りませんでした**——そして
// **返り注文は必ずこちらです**（図面は何か月も前に来ている）。実データで
// 「埋まりません」と分かりました（2026-09-21）。
//
//	発注書が先 → 図面が後 … 図面の整理で走る（LinkOrdersToProduct）
//	図面が先 → 発注書が後 … こちらで走る（解析の直後・受注の整理）
//
// **どちらも人の操作の直後**です（🤖 解析・📁 整理）。裏で回る仕事は作りません。
// 歯止め（1件のときだけ・人の値は触らない）は押し出しと同じものを通ります。
func LinkProductsToOrder(user *auth.User, orderPageID string) int {
	id, ok := page.NormalizeID(orderPageID)
	if !ok {
		return 0
	}
	body, err := cms.ReadPageBody(id)
	if err != nil {
		return 0
	}
	filled := 0
	for _, code := range orderItemNosOf(body) {
		cands := ProductPagesByCode(code)
		// ⚠ **1件でなければ触りません**（押し出しと同じ線引き）。
		if len(cands) != 1 {
			continue
		}
		target := page.FormatID(cands[0])
		fixed, n := fillOurItemNo(body, code, target)
		if n == 0 {
			continue
		}
		body = fixed
		filled += n
		auth.Audit(user.Username, "order-item.linked", id+" "+code+" -> "+target)
	}
	if filled == 0 {
		return 0
	}
	// ⚠ **書き込みは1回にまとめます**——行ごとに書くと版が7つ増えます。
	if err := cms.RewriteBody(id, user.Username, func(string) string { return body }); err != nil {
		return 0
	}
	return filled
}

// orderItemNosOf は受注明細の `品番` の値を、空いている行だけ集めます。
func orderItemNosOf(body string) []string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range tablesOfType(nodes, clientOrderItemsType) {
		rows := rowsOf(t)
		if len(rows) < 2 {
			continue
		}
		col := headerIndex(rows[0])
		ourCol, okOur := col[OurItemNoTag]
		itemCol, okItem := col[ItemNoTag]
		if !okOur || !okItem {
			continue
		}
		for _, tr := range rows[1:] {
			cells := cellTexts(tr)
			if ourCol >= len(cells) || itemCol >= len(cells) {
				continue
			}
			// ⚠ **埋まっている行は集めません**（人の値を触らないため）。
			if cells[ourCol] != "" || cells[itemCol] == "" {
				continue
			}
			if !seen[cells[itemCol]] {
				seen[cells[itemCol]] = true
				out = append(out, cells[itemCol])
			}
		}
	}
	return out
}
