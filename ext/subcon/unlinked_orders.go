package subcon

// ─────────────────────────────────────────────────────────────────────────
// 加工製品ページに結べない受注明細を知らせる（2026-09-25・引き継ぎの P1）
//
// ユーザー:「受注したのに加工製品のページが無い場合、それは作れないわけですから、
// **何かが間違っています**。おそらくページを作る順番を間違っているだけで、受注ページを
// 作った後に加工製品ページが作られるのでしょう。したがって、**加工製品ページが無い
// ことを警告しましょう**」「弊社品番の背景を**薄赤**にしても良いかもしれません」。
//
// それまで必要部材表は「必要部材はありません（⚠ 受注明細に弊社品番が無い行は、
// ここに出ません）」と**一般論しか言わず**、受注明細が7行あるのに空の表を
// 「受注ページを作っても出ない＝壊れた」と読まれました（2026-09-24〜25・自宅の
// データ・7行とも弊社品番が空で、加工製品ページが1枚も無かった）。
//
// 出す場所は2つです:
//
//	受注ページ … 結べない行の弊社品番を薄赤＋⚠ にし、表の足元に理由を出す
//	必要部材表 … 結べない行が何行あるかを、受注ページへのリンクつきで出す
//
// ⚠ **判定は必要部材表と同じ関数を通します**（`productOfOrderRow`）——
// **薄赤の行 ⇔ 必要部材表に材料が出ない行**を崩さないためです。別の規則で
// 判定すると、「赤くないのに出ない」「赤いのに出る」行が生まれます。
//
// ⚠ **空の弊社品番をすべて赤くはしません。** `品番` が加工製品ページの
// 図面番号・品番タグに1枚だけ当たれば結べて（`productByCode`）、材料も出ます
// ——そういう行は既存の気づき（「弊社品番が空です（…と思われます）」・
// link_item.go）が埋め方を教えます。コアの ref_render.go が空欄を赤くしないのは
// 「まだ決めていない」を赤くしないためで、ここで赤くするのは**受注しているのに
// 作れない**ときだけです。
//
// ⚠ **黙るもの**: 完了した行（手配の対象ではない・受注残表と同じ線引き）／
// 読めないページに結ばれた行（見せ分け・C案——結べてはいるので赤くもしない）／
// 空の行。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// unlinkedWhy は受注明細の1行が加工製品ページに結べない理由です。
type unlinkedWhy int

const (
	whyLinked      unlinkedWhy = iota // 結べる（読めないページに結ばれている場合も含む）
	whyBlank                          // 弊社品番も品番も空
	whyNoProduct                      // 品番に当たる加工製品ページが無い
	whyAmbiguous                      // 品番が2枚以上の加工製品ページに当たる
	whyMissingPage                    // 弊社品番の指すページが無い（形が違う値も含む）
)

// orderRowLinkState は受注明細の1行が加工製品ページに結べるかを見ます。
//
// ⚠ **結べるかどうかは `productOfOrderRow` が決めます**（必要部材表と同じ）。
// ここが足すのは、結べなかったときの**理由**と、結べたはずのページが
// **消えている**場合だけです（必要部材表は `canView` で黙って落とします）。
func orderRowLinkState(db cms.ReadOnlyDB, r cms.VocabRow) unlinkedWhy {
	our := strings.TrimSpace(r.Values["our-item-id"])
	code := strings.TrimSpace(r.Values["item-id"])
	if pid, ok := productOfOrderRow(db, r); ok {
		if !pageExistsIn(db, pid) {
			return whyMissingPage
		}
		return whyLinked
	}
	if code == "" {
		if our == "" {
			return whyBlank
		}
		return whyMissingPage // ページ番号の形でない弊社品番
	}
	if len(productCandidatesByCode(db, code)) >= 2 {
		return whyAmbiguous
	}
	if our != "" {
		return whyMissingPage // 弊社品番が読めず、品番も当たらない
	}
	return whyNoProduct
}

// pageExistsIn はそのページが索引に在るかを返します（認可はしない）。
func pageExistsIn(db cms.ReadOnlyDB, idInt int) bool {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = ?`, idInt).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// orderRowIsEmpty は行の値がすべて空かを返します（打ちかけの空行は数えない）。
func orderRowIsEmpty(values map[string]string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// ── 受注ページ（鏡）──────────────────────────────────────────────────────

// unlinkedCellClass は結べない行の弊社品番のセルに付ける印です（見た目は app.css）。
//
// ⚠ **本文には残りません**——`class` はサニタイズで落ちます（`row-obsolete` と
// 同じ作り）。毎回数え直すので、**加工製品ページを作れば次に開いたとき消えます**。
const unlinkedCellClass = "order-unlinked"

// markUnlinkedRows は受注明細の表で、加工製品ページに結べない行の弊社品番を薄赤にし、
// 表の足元に出す理由の文を返します（無ければ空）。
//
// ⚠ **足元の行を足す前に呼ぶこと**——足元（`<tfoot>`）の行は横いっぱいのセル1つで、
// 空の行に見えます。念のためクロームの行は飛ばします。
func markUnlinkedRows(db cms.ReadOnlyDB, table *html.Node) []string {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return nil
	}
	col := headerIndex(rows[0])
	// ⚠ **弊社品番の列が無い表でも黙りません**（2026-09-25 に職場のデータで踏んだ）。
	//    塗る場所は無くても、必要部材表の警告はその行を数えます——ここで黙ると、
	//    **発注フォルダが指した受注ページを開いても何も書いていない**ことになります。
	ourCol, okOur := col[OurItemNoTag]
	itemCol, okItem := col[ItemNoTag]
	statusCol, okStatus := col["状態"]
	nameCol, okName := col[ItemNameTag]

	var blank, noProduct []int
	var notes []string
	for n, tr := range rows[1:] {
		if inChrome(tr) {
			continue
		}
		cells := cellsOf(tr)
		texts := cellTexts(tr)
		at := func(i int, ok bool) string {
			if !ok || i < 0 || i >= len(texts) {
				return ""
			}
			return texts[i]
		}
		if allBlank(texts) || at(statusCol, okStatus) == StatusDone {
			continue
		}
		r := cms.VocabRow{Values: map[string]string{
			"our-item-id": at(ourCol, okOur),
			"item-id":     at(itemCol, okItem),
		}}
		why := orderRowLinkState(db, r)
		if why == whyLinked {
			continue
		}
		if okOur && ourCol < len(cells) {
			addClass(cells[ourCol], unlinkedCellClass)
		}
		rowNo := n + 1
		label := strconv.Itoa(rowNo) + "行目"
		if name := at(nameCol, okName); name != "" {
			label += "「" + name + "」"
		}
		switch why {
		case whyBlank:
			blank = append(blank, rowNo)
		case whyNoProduct:
			noProduct = append(noProduct, rowNo)
		case whyAmbiguous:
			// ⚠ **候補を名指ししません**（link_item.go と同じ——中途半端に名指しすると
			//    かえって誤らせる）。枚数も言いません（読めないページを数えるため）。
			notes = append(notes, "⚠ "+label+"の品番 "+r.Values["item-id"]+
				" は、加工製品ページの複数に当たります。弊社品番でどれか選ぶまで、"+
				"必要部材表に材料が出ません")
		case whyMissingPage:
			notes = append(notes, "⚠ "+label+"の弊社品番 "+r.Values["our-item-id"]+
				" のページがありません。必要部材表に材料が出ません")
		}
	}
	// ⚠ **同じ理由の行は1行にまとめます**——7行が同じ理由で結べないとき、
	//    足元に7行並ぶと、表より注意書きのほうが長くなります。
	var head []string
	if len(noProduct) > 0 {
		head = append(head, "⚠ "+rowList(noProduct)+"の加工製品ページがありません"+
			"（品番に当たるページが無い）。加工製品ページを作るまで、必要部材表に材料が出ません")
	}
	if len(blank) > 0 {
		head = append(head, "⚠ "+rowList(blank)+"は弊社品番も品番も空です。"+
			"どの加工製品か分からないので、必要部材表に材料が出ません")
	}
	out := append(head, notes...)
	if !okOur && len(out) > 0 {
		out = append(out, "⚠ この受注明細には弊社品番の列がありません。"+
			"列を足すと、結ぶ加工製品ページの番号を書けます")
	}
	return out
}

// rowList は行番号を「1・2・4行目」の形にします。
func rowList(rows []int) string {
	s := make([]string, len(rows))
	for i, r := range rows {
		s[i] = strconv.Itoa(r)
	}
	return strings.Join(s, "・") + "行目"
}

// allBlank はセルの文字がすべて空かを返します。
func allBlank(texts []string) bool {
	for _, t := range texts {
		if t != "" {
			return false
		}
	}
	return true
}

// inChrome はクローム（`.vocab-chrome`・サーバーが足したもの）の中かを返します。
func inChrome(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		for _, c := range strings.Fields(cms.Attr(p, "class")) {
			if c == "vocab-chrome" {
				return true
			}
		}
	}
	return false
}

// ── 必要部材表（受注横断）────────────────────────────────────────────────

// UnlinkedOrder は、加工製品ページに結べない行を持つ受注ページ1枚です。
type UnlinkedOrder struct {
	PageID int
	Title  string
	Rows   int // 結べない行の数
}

// unlinkedOrdersLimit は必要部材表に並べる受注ページの上限です。
// **選べない長さの一覧は選択肢ではありません**（unlinked.go と同じ考え）。
const unlinkedOrdersLimit = 20

// UnlinkedOrders は、加工製品ページに結べない受注明細を受注ページごとに数えます。
//
// ⚠ **絞り方は `UnorderedItems` と同じ**です——読めない受注ページ・完了した行は
// 数えません。数える行 ＝ 必要部材表に材料が出ない行、になるように。
func UnlinkedOrders(user *auth.User) []UnlinkedOrder {
	db := database.DB
	orders, err := cms.VocabRowsOfType(db, clientOrderItemsType)
	if err != nil {
		return nil
	}
	canView := viewCheck(user)
	var out []UnlinkedOrder
	at := map[int]int{}
	for _, o := range orders {
		if !canView(o.PageID) {
			continue
		}
		if strings.TrimSpace(o.Values["status"]) == StatusDone || orderRowIsEmpty(o.Values) {
			continue
		}
		if orderRowLinkState(db, o) == whyLinked {
			continue
		}
		i, ok := at[o.PageID]
		if !ok {
			i = len(out)
			at[o.PageID] = i
			out = append(out, UnlinkedOrder{PageID: o.PageID, Title: cms.PageTitleByID(o.PageID)})
		}
		out[i].Rows++
	}
	return out
}

// unlinkedOrdersHTML は必要部材表の上に出す警告です（結べない行が無ければ空）。
func unlinkedOrdersHTML(list []UnlinkedOrder) string {
	if len(list) == 0 {
		return ""
	}
	total := 0
	for _, u := range list {
		total += u.Rows
	}
	var b strings.Builder
	b.WriteString(`<div class="unorder-unlinked">`)
	b.WriteString(`<p>⚠ 加工製品ページに結べない受注明細が <strong>` + strconv.Itoa(total) +
		`行</strong>あります——その材料はここに出ません。` +
		`受注ページの<strong>薄赤の弊社品番</strong>の行です。` +
		`加工製品ページを作るか、弊社品番にそのページ番号を書いてください。</p><ul>`)
	for i, u := range list {
		if i >= unlinkedOrdersLimit {
			b.WriteString(`<li>ほか ` + strconv.Itoa(len(list)-i) + `枚</li>`)
			break
		}
		id := page.FormatID(u.PageID)
		title := u.Title
		if title == "" {
			title = id
		}
		b.WriteString(`<li><a href="/` + id + `">` + stdhtml.EscapeString(title) + `</a>` +
			`（` + strconv.Itoa(u.Rows) + `行）</li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}
