package toho

// ─────────────────────────────────────────────────────────────────────────
// 品番から製造製品ページを特定し、受注明細の `弊社品番` を埋める（2026-09-21）
//
// ユーザー:「**弊社品番を入れるために、品番から製造製品ページを特定する必要が
// あります**」「仮に製造製品ページがまだつくられていない場合、検索しても何も出て
// こないので埋められません。とはいえ、**注文が入っている以上、近日中に製造製品
// ページが出来るはず**です。そのタイミングで検索して埋めることになると思います。
// **できれば機械的にやってほしいです**」。
//
// **結ぶのは2つの時点だけです**（2026-10-01 に単純化——利用者:「単純化しましょう。受注ページを作った時に
// 弊社品番が無くリンクされなかった品物は、受注フォルダを開いたタイミングで、品番を検索しページ番号を
// 取得します。検索出来れば弊社品番がありますし、なければ依然として背景薄赤です」「受注フォルダを中心に
// 作業するので問題ないはずです」）:
//
//	受注ページを作ったとき … 🤖解析の直後（analyze_pdf.go）
//	受注フォルダを開いたとき … 受注残表に並ぶ行で空いているもの（backlog.go の linkBacklogRows）
//
// ⚠ それまでは**加工製品ページが置かれたとき**（図面の整理）・受注の整理・管理者の結び直しの口でも結んで
// いた——引き金が散らばり、二つ目の図面では条件の取り違えで走っていなかった。どちらの時点も**人の操作の
// 直後**で、**裏で走る仕事は作りません**（Gemini も解析も「人が押した直後だけ」という既存の流儀）。
//
// **照合はタグ名を問いません。** 加工製品ページ側の手掛かりは客先によって違います:
//
//	みなと商店のように図番で発注してくる客先 … ページの `図面番号` に当たる
//	品番で発注してくる客先               … ページの `品番` に当たる
//	図面の無い製品                       … `品番` しか無い
//
// 見る名前は設定（`extensions.toho.product_code_tags`）が正本です。
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
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

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
// 受注ページを開いたときは、空いている行と結び先の食い違いを**見せるだけ**で、**書きません**。
// ⚠ 書くのは**受注フォルダを開いたとき**だけ（backlog.go の `linkBacklogRows`・2026-10-01）。
// ⚠ もとは「読むだけの操作（GET）で本文を書き換えると、オートセーブと衝突しますし、『見ただけで変わる』は
// 追いにくい壊れ方」として、どこでも書かなかった。受注フォルダで書くのは利用者の決定で、その2つには
// **編集中の受注ページは飛ばす**・**監査に残す**（`order-item.linked`）で答えている。

// ItemNameTag は受注明細の品名の列です（食い違いの検査に使います）。
const ItemNameTag = "品名"

// orderLinkNotes は結びについての気づきを返します（無ければ空）。
//
// 見るのは2つで、**捕まえるものが違います**:
//
//	空いている行 … 結べる相手が居るのに空のまま（受注フォルダをまだ開いていない・その受注ページを誰かが編集していた）
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
	// 客先＋品番で引く（2026-10-01）——受注ページの発注元。
	customer := cms.TagValue(rootOf(table), OrderClientTag)

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
			cands := visibleOnly(viewer, productPagesForCustomer(db, customer, code))
			// ⚠ **同じ客先で同じ品番が2枚以上なら警告します**（2026-10-01 利用者:「同じ会社内の同一品番は警告し、
			// 人間が対応します」）——どれにも結ばない（機械には決められない）。どのページかを並べて、人が直せるように。
			if len(cands) > 1 {
				var ids []string
				for _, c := range cands {
					ids = append(ids, page.FormatID(c))
				}
				notes = append(notes, "⚠ "+label+"の品番 "+code+" の加工製品ページが同じ客先に "+
					strconv.Itoa(len(cands))+" 枚あります（"+strings.Join(ids, "・")+"）——結べません。どちらかの品番を直してください")
				continue
			}
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
// 埋めます（埋めた行数を返す）。
//
// ⚠ **こちらが本命です。** 引き金を整理（図面）だけに置いていたとき、
// **図面が先に届いて発注書が後から来る場合に一度も走りませんでした**——そして
// **返り注文は必ずこちらです**（図面は何か月も前に来ている）。実データで
// 「埋まりません」と分かりました（2026-09-21）。
//
//	図面が先 → 発注書が後 … 解析の直後にこれで結ぶ
//	発注書が先 → 図面が後 … 受注フォルダを開いたときにこれで結ぶ（backlog.go の linkBacklogRows）
//
// **どちらも人の操作の直後**です（🤖 解析・受注フォルダを開く）。裏で回る仕事は作りません。
// ⚠ **編集ロックと書き込みの権限は見ません**——呼ぶ側が確かめる（解析は作ったばかりのページ・受注フォルダは
// `linkBacklogRows` が確かめる）。
func LinkProductsToOrder(user *auth.User, orderPageID string) int {
	filled := 0
	for _, l := range linkProductsToOrder(user, orderPageID) {
		filled += l.Rows
	}
	return filled
}

// orderLink は受注の行を加工製品ページへ結んだ1件です（品番ごと）。
type orderLink struct {
	Order   string `json:"order"`   // 受注ページ
	Code    string `json:"code"`    // 受注明細の品番
	Product string `json:"product"` // 結んだ加工製品ページ
	Title   string `json:"title"`   // その題（人が「本当にこれか」を見るため——【旧】の付いたページなど）
	Rows    int    `json:"rows"`    // 埋めた行
}

// linkProductsToOrder は LinkProductsToOrder の本体で、何をどこへ結んだかを返します（書けなかったら空）。
func linkProductsToOrder(user *auth.User, orderPageID string) []orderLink {
	id, ok := page.NormalizeID(orderPageID)
	if !ok {
		return nil
	}
	body, err := cms.ReadPageBody(id)
	if err != nil {
		return nil
	}
	// ⚠ **客先＋品番で引きます**（2026-10-01・product_customer.go——会社が違えば同じ品番がありうる）。
	idInt, _ := strconv.Atoi(id)
	customer := cms.PageTagValue(database.DB, idInt, OrderClientTag)
	var links []orderLink
	for _, code := range orderItemNosOf(body) {
		cands := productPagesForCustomer(database.DB, customer, code)
		// ⚠ **1件でなければ触りません**（同じ客先で同じ品番が2枚——人が直す・鏡が ⚠ で知らせる）。
		if len(cands) != 1 {
			continue
		}
		target := page.FormatID(cands[0])
		fixed, n := fillOurItemNo(body, code, target)
		if n == 0 {
			continue
		}
		body = fixed
		links = append(links, orderLink{Order: id, Code: code, Product: target, Title: cms.PageTitleByID(cands[0]), Rows: n})
	}
	if len(links) == 0 {
		return nil
	}
	// ⚠ **書き込みは1回にまとめます**——行ごとに書くと版が7つ増えます。
	if err := cms.RewriteBody(id, user.Username, func(string) string { return body }); err != nil {
		return nil
	}
	// 監査は書けてから（書けなかった結びを「結んだ」と残さない）。
	for _, l := range links {
		auth.Audit(user.Username, "order-item.linked", id+" "+l.Code+" -> "+l.Product)
	}
	return links
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
