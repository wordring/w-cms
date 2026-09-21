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
//	南北様のように図番で発注してくる客先 … ページの `図面番号` に当たる
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
	"sort"
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
	if strings.TrimSpace(value) == "" {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, name := range ProductCodeTags() {
		ids, err := cms.PagesByTagLoose(database.DB, name, value)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Ints(out)
	return out
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
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" && isOrderItemsTable(n) {
			filled += fillTable(n, want, productPageID)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if filled == 0 {
		return body, 0
	}
	return htmldoc.Render(nodes), filled
}

// isOrderItemsTable は受注明細の表かを見ます（属性でも caption でも）。
//
// ⚠ **両方見ます。** 形式の宣言は `data-type` から見える文字（`<caption>`）へ移る
// 途中で、しばらく併存します（§2.4）。片方しか見ないと、移した日に**黙って
// 埋まらなくなります**——エラーは出ません。
func isOrderItemsTable(t *html.Node) bool {
	if cms.Attr(t, "data-type") == clientOrderItemsType {
		return true
	}
	def, ok := cms.VocabDefByType(clientOrderItemsType)
	if !ok {
		return false
	}
	cap := lastChild(t, "caption")
	return cap != nil && strings.TrimSpace(textOf(cap)) == def.DisplayName
}

// fillTable は見出しから列を割り出し、当てはまる行を埋めます。
func fillTable(t *html.Node, wantNorm, productPageID string) int {
	rows := rowsOf(t)
	if len(rows) < 2 {
		return 0
	}
	ourCol, itemCol := -1, -1
	i := 0
	for c := rows[0].FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "th" && c.Data != "td") {
			continue
		}
		switch strings.TrimSpace(textOf(c)) {
		case OurItemNoTag:
			ourCol = i
		case ItemNoTag:
			itemCol = i
		}
		i++
	}
	if ourCol < 0 || itemCol < 0 {
		return 0
	}

	filled := 0
	for _, tr := range rows[1:] {
		var cells []*html.Node
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				cells = append(cells, c)
			}
		}
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
		for cells[ourCol].FirstChild != nil {
			cells[ourCol].RemoveChild(cells[ourCol].FirstChild)
		}
		cells[ourCol].AppendChild(&html.Node{Type: html.TextNode, Data: productPageID})
		filled++
	}
	return filled
}
