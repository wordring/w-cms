package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページの作業（2026-10-21 夕・【考察】製造単位の部材発注 §7）
//
//	2026-10-21 利用者:「製造明細表を修正しましょう。この表はご法度のはずの一緒にボタンを入れています。」
//	「製造明細表を作業開始という見出しの下にいれ、行の先頭にチェックボックスを付け、選択した行について,」
//	「表の下にあるボタンを押すとその作業を開始するマナーにしたいです。部材、納品があります。部材を押すと必要部材を収集して表を作ります。
//	この表にもチェックボックスがあり、選択したものを表の下のボタンで作業開始します。ボタンは発注と見積もりがあるはずです。
//	納品を押すと納品表が作られます。この表を編集して、その下にあるボタンを押すと納品書が作成されます。」
//	「発注、見積もりなどの作業はそれぞれ見出しの下に置き開閉できるようにしておきましょう」
//	「発注作業、見積依頼作業などの見出しでも良いかもしれません柄」
//
//   - 製造ページの本文は、見出し（h2）ごとの作業に分ける: 作業開始（製造明細表）・部材作業（必要部材表・手配不要表）・
//     発注作業（発注部材表）・見積依頼作業（見積依頼部材表）・納品作業（納品表）。閲覧モードでは見出しを押して畳める（コア）。
//   - 表の中には本文の値と、行の先頭のチェックだけを置く。ボタンは表の外（表の下）に置く（鏡・保存しない）。
//   - 必要部材表は本文の表。「部材」を押すたびに、選んだ製造明細の行のぶんで作り直す（手配済みに数えない——押し直しても二重にならない）。
//     「発注」は選んだ行を推奨業者ごとの発注部材表へ移す。「見積もり」は見積依頼部材表へ写す（必要部材表には残す）。「不要」は手配不要表へ。
//   - 納品表は本文の表。「納品」を押すと、選んだ行のまだ納めていない数で作り直す。人が数量を直して「納品書を作る」を押すと
//     納品書ができ（/api/delivery/new）、納品表は消える。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 製造ページの作業の見出し（h2）です。
const (
	MfgHeadStart    = "作業開始"
	MfgHeadParts    = "部材作業"
	MfgHeadOrder    = "発注作業"
	MfgHeadQuote    = "見積依頼作業"
	MfgHeadDelivery = "納品作業"
	// MfgHeadCalc は見積計算書の見出し（2026-10-23・納品作業の下。利用者:「納品作業の下に見積もり計算作業の項目を追加します」）。
	MfgHeadCalc = "見積もり計算作業"
)

// MfgNeedsType は製造ページの必要部材表、MfgDeliveryDraftType は納品表の形式です。
const (
	MfgNeedsType         = "mfg-needs"
	MfgDeliveryDraftType = "mfg-delivery-draft"
)

// mfgNeedsColumns は必要部材表の列です——部材発注明細表の列から状態を除き、ロットと推奨業者を足したもの。
func mfgNeedsColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range orderItemColumns() {
		if c.Field == "status" {
			continue
		}
		out = append(out, c)
	}
	return append(withLotBeforeQuantity(out), cms.VocabColumn{Field: "vendor", Label: vendorLabel, Type: cms.ColText})
}

// mfgDeliveryDraftColumns は納品表の列です（数量は今回納める数——人が直す）。
func mfgDeliveryDraftColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
		{Field: "order", Label: "案件", Type: cms.ColRef},
		{Field: "item-id", Label: ItemNoTag, Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		// 単価は今回納める分の単価（2026-10-28）。初めは案件の単価で、タップ無し・塗装無しで先に納めるときなどは直して、備考にわけを書く。
		// 2026-10-28 利用者:「一部を納品して価格が変わる（タップ無しや塗装無しで早く出す）ことがあるので、定型作業として可能にしたいのです。」
		{Field: "price", Label: "単価", Type: cms.ColNumber},
		{Field: "note", Label: "備考", Type: cms.ColText},
	}
}

func init() {
	cms.RegisterVocab(cms.VocabDef{
		// **必要部材表**（2026-10-21 夕・製造ページの「部材」で作る本文の表）。手配済みには数えない。
		Type: MfgNeedsType, DisplayName: "必要部材表", Category: "業務", Icon: "🧾", Element: "table", Hidden: true,
		Columns: mfgNeedsColumns(),
	})
	cms.RegisterVocab(cms.VocabDef{
		// **納品表**（2026-10-21 夕・製造ページの「納品」で作る本文の表）。「納品書を作る」で納品書になって消える。
		Type: MfgDeliveryDraftType, DisplayName: "納品表", Category: "業務", Icon: "🚚", Element: "table", Hidden: true,
		Columns: mfgDeliveryDraftColumns(),
	})
	cms.RegisterMirror(MfgNeedsType, cms.MirrorHandlerFunc(renderMfgNeeds))
	cms.RegisterMirror(MfgDeliveryDraftType, cms.MirrorHandlerFunc(renderMfgDeliveryDraft))
}

// ── 見出しの下に置く ─────────────────────────────────────────────────────

// headingText は h1〜h6 の文字です（見出しでなければ空）。
func headingLevel(n *html.Node) int {
	if n.Type != html.ElementNode || len(n.Data) != 2 || n.Data[0] != 'h' || n.Data[1] < '1' || n.Data[1] > '6' {
		return 0
	}
	return int(n.Data[1] - '0')
}

// placeUnderHeading は、本文の h2「heading」の節の末尾（次の h1・h2 の前）へ fragment を置きます。
// 見出しが無ければ、本文の末尾に h2 を足してから置きます。
func placeUnderHeading(body, heading, fragment string) string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body + "<h2>" + stdhtml.EscapeString(heading) + "</h2>" + fragment
	}
	repl, err := htmldoc.ParseFragment(fragment)
	if err != nil {
		return body
	}
	at := -1
	for i, n := range nodes {
		if headingLevel(n) == 2 && strings.TrimSpace(textOf(n)) == heading {
			at = i
			break
		}
	}
	var out []*html.Node
	if at < 0 {
		h := &html.Node{Type: html.ElementNode, Data: "h2"}
		h.AppendChild(&html.Node{Type: html.TextNode, Data: heading})
		out = append(append(append(out, nodes...), h), repl...)
		return htmldoc.Render(out)
	}
	end := len(nodes)
	for j := at + 1; j < len(nodes); j++ {
		if l := headingLevel(nodes[j]); l == 1 || l == 2 {
			end = j
			break
		}
	}
	out = append(out, nodes[:end]...)
	out = append(out, repl...)
	out = append(out, nodes[end:]...)
	return htmldoc.Render(out)
}

// dropTablesOfType は本文から vocabType の表を全部外します（表を包む節があれば節ごと）。
func dropTablesOfType(body, vocabType string) string {
	out, _ := dropTablesWhere(body, vocabType, func(*html.Node) bool { return true })
	return out
}

// dropTablesWhere は本文から vocabType の表のうち drop が真のものを外し（表を包む節があれば節ごと）、外した数を返します。
func dropTablesWhere(body, vocabType string, drop func(*html.Node) bool) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	var tables []*html.Node
	for _, t := range tablesOfType(nodes, vocabType) {
		if drop(t) {
			tables = append(tables, t)
		}
	}
	if len(tables) == 0 {
		return body, 0
	}
	root := wrapInDiv(nodes)
	for _, t := range tables {
		target := t
		if p := t.Parent; p != nil && p.Data == "section" && len(tablesOfType(childrenOf(p), vocabType)) == 1 && onlyTableIn(p) {
			target = p
		}
		if target.Parent != nil {
			target.Parent.RemoveChild(target)
		}
	}
	return htmldoc.Render(childrenOf(root)), len(tables)
}

// onlyTableIn は節 p の中身が表1つ（と空白）だけかです。
func onlyTableIn(p *html.Node) bool {
	n := 0
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		case c.Type == html.ElementNode && c.Data == "table":
			n++
		default:
			return false
		}
	}
	return n == 1
}

// startHeadingRe は見出し「作業開始」です。
var startHeadingRe = regexp.MustCompile(`<h2(?:\s[^>]*)?>\s*` + regexp.QuoteMeta(MfgHeadStart) + `\s*</h2>`)

// ensureStartHeading は、製造明細表の前に見出し「作業開始」が無ければ足します（新しい製造ページ・テンプレートに見出しが無いとき）。
func ensureStartHeading(body string) string {
	// 見出しの属性（data-id）と空白を許す（2026-10-26。それまでは詰めた形だけを見ていたので、人が保存したページ・機械が ID を
	// 入れたページでは見出しを見落とし、もう1つ足した）。
	if startHeadingRe.MatchString(body) {
		return body
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body
	}
	for i, n := range nodes {
		if n.Type == html.ElementNode && n.Data == "table" && isTableOfType(n, MfgItemsType) {
			h := &html.Node{Type: html.ElementNode, Data: "h2"}
			h.AppendChild(&html.Node{Type: html.TextNode, Data: MfgHeadStart})
			out := append(append(append([]*html.Node{}, nodes[:i]...), h), nodes[i:]...)
			return htmldoc.Render(out)
		}
	}
	return body
}

// ensureCalcHeading は、見出し「見積もり計算作業」が無ければ本文の末尾に足します（2026-10-23・新しい製造ページ・テンプレートに見出しが無いとき）。
//
// > 2026-10-23 利用者:「納品作業の下に見積もり計算作業の項目を追加します」
func ensureCalcHeading(body string) string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body
	}
	for _, n := range nodes { // 見出しは data-id を持つことがある（エディタで保存したテンプレート）ので、字で見る
		if headingLevel(n) == 2 && strings.TrimSpace(textOf(n)) == MfgHeadCalc {
			return body
		}
	}
	return body + "<h2>" + MfgHeadCalc + "</h2>"
}

// ── 表の先頭のチェックと、表の下の帯 ───────────────────────────────────────

// prependRowChromeCells は表の各行の先頭にセルを足します（見出し行は空の th・データ行は inner(row 0始まり, tr) の HTML）。
func prependRowChromeCells(table *html.Node, class string, inner func(n int, tr *html.Node) string) {
	for i, tr := range rowsOf(table) {
		cell := &html.Node{Type: html.ElementNode, Data: "td",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome " + class}, {Key: "contenteditable", Val: "false"}}}
		if i == 0 {
			cell.Data = "th"
		} else if s := inner(i-1, tr); s != "" {
			appendHTML(cell, s)
		}
		tr.InsertBefore(cell, tr.FirstChild)
	}
}

// appendBarAfter は表 el の直後に帯（鏡の div）を置きます。
func appendBarAfter(el *html.Node, class, innerHTML string) {
	if el.Parent == nil {
		return
	}
	div := &html.Node{Type: html.ElementNode, Data: "div",
		Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome w-no-print mfg-bar " + class}, {Key: "contenteditable", Val: "false"}}}
	appendHTML(div, innerHTML)
	el.Parent.InsertBefore(div, el.NextSibling)
}

// pickHTML は行の先頭のチェックです。
func pickHTML(class string, attrs map[string]string) string {
	var b strings.Builder
	b.WriteString(`<input type="checkbox" class="` + class + `" aria-label="この行を選ぶ"`)
	for _, k := range []string{"row", "sig", "item", "name", "order", "left", "made", "product"} {
		if v, ok := attrs[k]; ok {
			b.WriteString(` data-` + k + `="` + stdhtml.EscapeString(v) + `"`)
		}
	}
	b.WriteString(`>`)
	return b.String()
}

func barButton(class, label, title string) string {
	return `<button type="button" class="chip-btn ` + class + `" title="` + stdhtml.EscapeString(title) + `">` + label + `</button> `
}

// ── 必要部材表と納品表の鏡 ─────────────────────────────────────────────────

func renderMfgNeeds(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	if !writableMirror(ctx) || pageKindOf(ctx.PageID) != PageKindMfg {
		return false, nil
	}
	// 行の中身の指紋（data-sig）も添える——サーバーが同じ行かを確かめる（2026-10-26。番号だけだと、押し直しや、ほかの人の
	// 「🧾 部材作業」のあとに押すと、選んでいない行を発注作業・不要へ動かした）。
	prependRowChromeCells(el, "mfg-needs-pick-cell", func(n int, tr *html.Node) string {
		return pickHTML("mfg-needs-pick", map[string]string{"row": strconv.Itoa(n + 1), "sig": rowSig(tr)})
	})
	pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
	appendBarAfter(el, "mfg-needs-bar", `<span data-mfg="`+pid+`"></span>`+
		barButton("mfg-needs-order", "🛒 発注作業", "選んだ行を、推奨業者ごとの発注部材表へ移します（「発注作業」の見出しの下）")+
		barButton("mfg-needs-quote", "📨 見積依頼作業", "選んだ行を、見積依頼部材表へ写します（「見積依頼作業」の見出しの下・必要部材表には残す。数は手配済みを引かない、この製造で作るぶん）")+
		barButton("mfg-needs-skip", "🚫 不要", "選んだ行を、この製造では手配しません（理由と日付つきで手配不要表へ）")+
		barButton("mfg-needs-drop", "↩ 作業開始へ戻す", "選んだ行を必要部材表から外します（作業開始の「🧾 部材作業」でまた集められます）")+
		`<span class="mfg-bar-say"></span>`)
	return false, nil
}

func renderMfgDeliveryDraft(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	if !writableMirror(ctx) || pageKindOf(ctx.PageID) != PageKindMfg {
		return false, nil
	}
	pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
	appendBarAfter(el, "mfg-dlvdraft-bar", `<span data-mfg="`+pid+`"></span>`+
		barButton("mfg-dlvdraft-make", "🚚 納品書を作る", "この納品表（数量を直してから）で納品書を作ります。作ると納品表は消えます")+
		`<span class="mfg-bar-say"></span>`)
	return false, nil
}

// ── 製造明細の選んだ行 ─────────────────────────────────────────────────────

// mfgPick は画面で選んだ製造明細の行です（row は見出しを除いた0始まり・item は品番〔照合〕）。
type mfgPick struct {
	Row  int    `json:"row"`
	Item string `json:"item"`
}

// pickedMfgRows は、選んだ行を製造明細の索引の行番号の集合にします（品番が合わない行があれば理由）。
func pickedMfgRows(mfgID int, picks []mfgPick) (map[int]bool, string) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, "製造明細を読めません: " + err.Error()
	}
	if len(picks) == 0 {
		return nil, "製造明細表の行の先頭のチェックで、行を選んでください"
	}
	sel := map[int]bool{}
	for _, p := range picks {
		if p.Row < 0 || p.Row >= len(rows) || strings.TrimSpace(rows[p.Row].Values["item-id"]) != strings.TrimSpace(p.Item) {
			return nil, "選んだ行が見つかりません（ページを読み直してください）"
		}
		sel[p.Row] = true
	}
	return sel, ""
}

// mfgStartedRows は、選んだ製造明細の行のうち、部材の作業をもう始めた行（部材の1つでも手配済み——部材発注明細表・発注部材表・
// 手配不要表に当たっている）を、行番号 → 品名で返します（2026-10-23）。部材の表に行の無い品物は、始めたとは数えません。
//
// > 2026-10-23 利用者:「製造明細表の下にある「追加の発注」ぼ異端は無くし、開始済みの部材を作業開始すると警告するようにします。」
func mfgStartedRows(user *auth.User, mfgID int, sel map[int]bool) (map[int]string, error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, err
	}
	lines, _, err := mfgPartLines(user, mfgID, sel)
	if err != nil {
		return nil, err
	}
	left := map[string]int{} // 案件|弊社品番 → まだ手配していない数の合計
	for _, l := range lines {
		if q, ok := estimateNumber(l.Quantity); ok {
			left[l.ForOrder+"|"+l.ProductID] += int(q)
		}
	}
	out := map[int]string{}
	for i, r := range rows {
		if sel != nil && !sel[i] {
			continue
		}
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		order, okO := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/"))
		qty, okQ := estimateNumber(r.Values["quantity"])
		// 読めない加工製品ページの部材は見ない（2026-10-26。それまでは、読めない加工製品に手配の残りがあるかが分かった）。
		if !ok || !okO || !okQ || qty <= 0 || !page.CanView(user, pageNum(pid)) {
			continue
		}
		need := 0
		for _, it := range productNeeds(database.DB, pageNum(pid), int(qty)) {
			need += it.Required
		}
		if need > 0 && left[order+"|"+pid] < need {
			out[i] = strings.TrimSpace(r.Values["item-name"])
		}
	}
	return out, nil
}

// MfgPartsAPIHandler は POST /api/mfg/parts です。入力: {page_id, items:[{row, item}], check, extra}（製造ページ）。
// 選んだ製造明細の行の、まだ手配していない部材を集めて、「部材作業」の見出しの下に必要部材表を作り直します。
//   - check なら何も書かず、部材の作業をもう始めた行（mfgStartedRows）を返します（画面が「🧾 部材作業」の前に確かめて警告する・2026-10-23）。
//   - extra なら、もう始めた行は手配済みを引かずに全部を備考「追加」で、まだの行はいつもどおり手配していない分を集めます
//     （追加の発注・order_unsend.go。2026-10-23 までは「➕ 追加の発注」のボタンが、選んだ行を全部「追加」で必要部材表へ足していた）。
func MfgPartsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string    `json:"page_id"`
		Items  []mfgPick `json:"items"`
		Check  bool      `json:"check"`
		Extra  bool      `json:"extra"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID || !requireMfgPage(w, pageID) {
		return
	}
	sel, why := pickedMfgRows(pageNum(pageID), req.Items)
	if why != "" {
		cms.JSONFail(w, http.StatusBadRequest, why)
		return
	}
	started, err := mfgStartedRows(user, pageNum(pageID), sel)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "部材を数えられません: "+err.Error())
		return
	}
	if req.Check {
		type row struct {
			Row  int    `json:"row"`
			Name string `json:"name"`
		}
		list := []row{}
		for i, n := range started {
			list = append(list, row{Row: i, Name: n})
		}
		sort.Slice(list, func(a, b int) bool { return list[a].Row < list[b].Row })
		cms.WriteJSON(w, map[string]any{"success": true, "started": list})
		return
	}
	var lines []ourOrderLine
	var skipped int
	if req.Extra {
		// もう始めた行は全部を「追加」で、まだの行は手配していない分を。
		again, rest := map[int]bool{}, map[int]bool{}
		for i := range sel {
			if _, ok := started[i]; ok {
				again[i] = true
			} else {
				rest[i] = true
			}
		}
		var more []ourOrderLine
		var s1, s2 int
		if len(rest) > 0 {
			lines, s1, err = mfgPartLines(user, pageNum(pageID), rest)
		}
		if err == nil && len(again) > 0 {
			more, s2, err = mfgExtraLines(user, pageNum(pageID), again)
		}
		lines, skipped = append(lines, more...), s1+s2
	} else {
		lines, skipped, err = mfgPartLines(user, pageNum(pageID), sel)
	}
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "部材を数えられません: "+err.Error())
		return
	}
	if len(lines) == 0 {
		msg := "選んだ行で手配する部材はありません（手配済みか、加工製品の部材の表に行がありません）"
		if skipped > 0 {
			msg += "。弊社品番の無い行が " + strconv.Itoa(skipped) + " 行あります"
		}
		cms.JSONFail(w, http.StatusBadRequest, msg)
		return
	}
	lines = mergeOrderLines(lines)
	// 必要部材表は、選んだ行のぶんで作り直す（追加の発注のときも——2026-10-23 から「🧾 部材作業」の確かめの中で選ぶので、同じ表の作り方にそろえた）。
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		cur = dropTablesOfType(cur, MfgNeedsType)
		return placeUnderHeading(cur, MfgHeadParts, tableOfLinesHTML(MfgNeedsType, lines, false))
	}) {
		return
	}
	what := "mfg.parts"
	if req.Extra {
		what = "mfg.extra"
	}
	auth.Audit(user.Username, what, pageID+" "+strconv.Itoa(len(lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": len(lines), "skipped": skipped})
}

// MfgNeedsAPIHandler は POST /api/mfg/needs です。入力: {page_id, rows:[1始まり], action, reason}。
// action は order（発注部材表へ移す）・quote（見積依頼部材表へ写す）・skip（手配不要表へ移す）・drop（外す）。
func MfgNeedsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string   `json:"page_id"`
		Rows   []int    `json:"rows"`
		Sigs   []string `json:"sigs"` // 行の中身の指紋（rows と同じ並び・2026-10-26）
		Action string   `json:"action"`
		Reason string   `json:"reason"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID || !requireMfgPage(w, pageID) {
		return
	}
	if len(req.Rows) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "必要部材表の行の先頭のチェックで、行を選んでください")
		return
	}
	switch req.Action {
	case "order", "quote", "skip", "drop":
	default:
		cms.JSONFail(w, http.StatusBadRequest, "作業が分かりません: "+req.Action)
		return
	}
	n := 0
	done := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		keep := req.Action == "quote" // 見積もりは写すだけ（必要部材表に残す）
		rows, okR := resolveRowsIfSigned(cur, MfgNeedsType, 1, req.Rows, req.Sigs)
		if !okR {
			return cur
		}
		out, lines, ok := takeTableRows(cur, MfgNeedsType, 1, sortedInts(rows), true)
		if !ok || len(lines) == 0 {
			return cur
		}
		if keep {
			out = cur
		}
		switch req.Action {
		case "order":
			for _, g := range splitByVendor(lines) {
				out = placeUnderHeading(out, MfgHeadOrder, orderDraftHTML(g))
			}
		case "quote":
			lines = grossQuoteLines(user, pageNum(pageID), lines)
			if merged, _, ok := appendLinesToTable(out, RFQDraftType, 1, lines); ok {
				out = merged
			} else {
				out = placeUnderHeading(out, MfgHeadQuote, tableOfLinesHTML(RFQDraftType, lines, true))
			}
		case "skip":
			reason := strings.Join(strings.Fields(req.Reason), " ")
			out, _ = addSkipRows(out, lines, reason, time.Now().Format("2006-01-02"))
		}
		n, done = len(lines), true
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict, "選んだ行が見つからないか、ほかの人が変えました（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "mfg.needs."+req.Action, pageID+" "+strconv.Itoa(n)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": n})
}

// grossQuoteLines は、見積もりへ写す行の数を「手配済みを引かない数」に直します（2026-10-21 夜）。
//
// > 2026-10-21 利用者:「手配済みをひきません」（部材見積依頼の数。2026-10-02 の「機械は、依頼済みを数えません」と同じ向き）
//
// 必要部材表の数は、発注のために手配済みを引いた残りです。見積もりでは、行の内訳（無ければ案件と弊社品番）の案件×加工製品ごとに、
// 「この製造で作る数 × 1個あたりの数」（mfgExtraLines と同じ数え方）を足し直します。部材が読めない行（人が材質や寸法を直した行など）は、
// 表の数のまま写します。備考の「追加」は、見積もりでは意味が無いので外します。
func grossQuoteLines(user *auth.User, mfgID int, lines []ourOrderLine) []ourOrderLine {
	gross, _, err := mfgExtraLines(user, mfgID, nil)
	if err != nil {
		return lines
	}
	// 品名は itemNameOf で揃える（材料の行は品名を持たない——必要部材表の行と、加工製品の材料表から読んだ行で
	// 品名の有無が違い、合わずに手配済みを引いた数のまま写っていた・2026-10-22 の見直し）。
	partKey := func(ln ourOrderLine) string {
		parts := []string{ln.Kind, ln.No, ln.ItemID, itemNameOf(ln), ln.Work, ln.Material, ln.Shape, ln.Size, ln.Color, ln.Spec, ln.Supplied}
		for i, p := range parts {
			parts[i] = cms.NormalizeText(strings.TrimSpace(p))
		}
		return strings.Join(parts, "\x1f")
	}
	need := map[string]int{}
	for _, g := range gross {
		q, err := strconv.Atoi(strings.TrimSpace(g.Quantity))
		if err != nil {
			continue
		}
		need[partKey(g)+"|"+g.ForOrder+"|"+g.ProductID] += q
	}
	out := make([]ourOrderLine, 0, len(lines))
	seen := map[string]bool{} // ふだんの行と「追加」の行を両方選ぶと、どちらも作る数の全部になる——1つだけ写す
	for _, ln := range lines {
		if strings.TrimSpace(ln.Note) == ExtraNote {
			ln.Note = ""
		}
		es, ok := entriesOf(ln)
		if !ok {
			out = append(out, ln)
			continue
		}
		total, found := 0, true
		next := make([]breakdownEntry, 0, len(es))
		for _, e := range es {
			q, has := need[partKey(ln)+"|"+e.Order+"|"+e.Product]
			if !has {
				found = false
				break
			}
			total += q
			next = append(next, breakdownEntry{Order: e.Order, Product: e.Product, Qty: q})
		}
		if found {
			k := partKey(ln) + "|" + formatBreakdown(next)
			if seen[k] {
				continue
			}
			seen[k] = true
			ln.Quantity = strconv.Itoa(total)
			if strings.TrimSpace(ln.Breakdown) != "" {
				ln.Breakdown = formatBreakdown(next)
			}
		}
		out = append(out, ln)
	}
	return out
}

// MfgDeliveryDraftAPIHandler は POST /api/mfg/delivery-draft です。入力: {page_id, items:[{row, item}]}（製造ページ）。
// 選んだ製造明細の行の、まだ納めていない数で、「納品作業」の見出しの下に納品表を作り直します。
func MfgDeliveryDraftAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string    `json:"page_id"`
		Items  []mfgPick `json:"items"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID || !requireMfgPage(w, pageID) {
		return
	}
	sel, why := pickedMfgRows(pageNum(pageID), req.Items)
	if why != "" {
		cms.JSONFail(w, http.StatusBadRequest, why)
		return
	}
	rows, _ := cms.VocabTableRowsOf(database.DB, pageNum(pageID), MfgItemsType)
	delivered := deliveredUnder(pageNum(pageID))
	var data []map[string]string
	for i, r := range rows {
		if !sel[i] {
			continue
		}
		v := r.Values
		if pageKindOf(pageNumOrZero(v["order"])) != PageKindCustomerOrder {
			continue // 見積の依頼・自社案件は納品しない
		}
		qty, okQ := estimateNumber(v["quantity"])
		if !okQ {
			continue
		}
		left := qty - delivered[shippedKey(v["order"], v["item-id"], v["item-name"])]
		if left <= 0 {
			continue
		}
		data = append(data, map[string]string{
			"our-item-id": v["our-item-id"], "order": v["order"], "item-id": v["item-id"], "item-name": v["item-name"],
			"quantity": fmtQty(left), "unit": v["unit"], "price": caseUnitPrice(user, v["order"], v["item-id"], v["item-name"]),
		})
	}
	if len(data) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "選んだ行に、まだ納めていない数がありません（見積の依頼・自社案件の行は納品しません）")
		return
	}
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		cur = dropTablesOfType(cur, MfgDeliveryDraftType)
		return placeUnderHeading(cur, MfgHeadDelivery, deliveryDraftHTML(data))
	}) {
		return
	}
	auth.Audit(user.Username, "mfg.delivery-draft", pageID+" "+strconv.Itoa(len(data))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": len(data)})
}

// caseUnitPrice は案件ページ order の、品番 itemNo（空なら品名 itemName）の行の単価です（無い・読めなければ空——user が読めない案件も空）。
func caseUnitPrice(user *auth.User, order, itemNo, itemName string) string {
	pid := pageNumOrZero(order)
	if pid == 0 || !page.CanView(user, pid) {
		return ""
	}
	rows, err := cms.VocabTableRowsOf(database.DB, pid, clientOrderItemsType)
	if err != nil {
		return ""
	}
	want := shippedKey(order, itemNo, itemName)
	for _, r := range rows {
		if shippedKey(order, r.Values["item-id"], r.Values["item-name"]) == want {
			return strings.TrimSpace(r.Values["price"])
		}
	}
	return ""
}

// deliveryDraftHTML は納品表の HTML です（見出し行は宣言から）。
func deliveryDraftHTML(data []map[string]string) string {
	var b strings.Builder
	b.WriteString(`<table><caption>` + stdhtml.EscapeString(displayNameOf(MfgDeliveryDraftType)) + `</caption><tbody>` + headerRowHTML(MfgDeliveryDraftType))
	for _, v := range data {
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(MfgDeliveryDraftType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(v[c.Field]) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// pageNumOrZero は参照の値をページ番号にします（読めなければ 0）。
func pageNumOrZero(v string) int {
	id, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(v), "/"))
	if !ok {
		return 0
	}
	return pageNum(id)
}
