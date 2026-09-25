package subcon

// ─────────────────────────────────────────────────────────────────────────
// 臨時部材表（2026-09-25）
//
// 要求（【要求】発注フォルダ「臨時部材表」）:
//   加工製品ページに無い材料（消耗品・工具・社内用など）は、この表に手で書きます。
//   書いた行は、下の必要部材表に並びます（客先の欄に「臨時部材」と出ます）。
//   必要部材表でチェックして発注部材表へ入れると、この表から消えます。
//   弊社品番の無い行を発注部材表や発注書から戻すと、この表へ戻ってきます。
//   行が無いときも、空の表を表示しておきます。
//
// ⚠ **行が在る＝まだ発注していない**。発注済みの計算（引き算）には入れません
// ——**行そのものを移します**。だから発注済みの割り出しは今のまま変わりません
// （ユーザー:「発注済の割り出しを複雑化させないためには、加工製品ページに無い
// 材料をどこかに記録して発注を開始しないといけない」）。
//
// ⚠ **なぜ要るか**: 必要部材表は**表示のたびに計算**する鏡で、どこにも保存して
// いません。弊社品番の無い行は計算の鎖（受注明細→弊社品番→材料表）に乗らないので、
// 発注書から戻すと**消えるだけ**でした（2026-09-24 ユーザー報告:「必要部材表へ戻すを
// 押すと発注明細から消えますが、必要部材表へは表示されません」）。
//
// ⚠ **置き場は発注フォルダ（トップ直下の「発注」）1か所**です。発注書ページ
// （`発注／年／月`）から戻す行も、そこへ書きます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	stdhtml "html"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// TempPartsType は臨時部材表の形式名です。
const TempPartsType = "temp-parts"

// tempPartsClient は必要部材表で、臨時部材表から来た行の「客先」の欄に出す言葉です。
const tempPartsClient = "臨時部材"

// tempPartsColumns は臨時部材表の列です——発注部材表の列から `弊社品番` と `状態` を除いたもの。
//
// ⚠ `弊社品番` を除くのは、弊社品番のある部材は**計算の側**（必要部材表）に出るから。
// ⚠ `状態` を除くのは、この表に**在ること自体**が「まだ発注していない」だから。
// ⚠ **同じ列の宣言から作ります**——写して書くと、列を足した日に移すときに落ちます。
func tempPartsColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range orderItemColumns() {
		if c.Field == "our-item-id" || c.Field == "status" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// tempPartsTableHTML は臨時部材表を組みます。⚠ **行が0なら空の1行を置きます**
// ——見出しだけの表は、エディタで行を足す取っ掛かりがありません。
func tempPartsTableHTML(lines []ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<table><caption>` + stdhtml.EscapeString(displayNameOf(TempPartsType)) +
		`</caption><tbody>` + headerRowHTML(TempPartsType))
	for _, ln := range lines {
		b.WriteString(tempPartsRowHTML(ln))
	}
	if len(lines) == 0 {
		b.WriteString(emptyRowHTML(TempPartsType))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func tempPartsRowHTML(ln ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<tr>`)
	for _, c := range columnsOf(TempPartsType) {
		b.WriteString(`<td>` + stdhtml.EscapeString(orderLineValue(ln, c.Field)) + `</td>`)
	}
	b.WriteString(`</tr>`)
	return b.String()
}

func emptyRowHTML(vocabType string) string {
	return `<tr>` + strings.Repeat(`<td></td>`, len(columnsOf(vocabType))) + `</tr>`
}

// lineOfRow は表の1行を、見出しの表示文字で読んで1行ぶんにします。
//
// ⚠ **鍵は見出しの表示文字**です（列の並びは表ごとに違ってよい）。
// 発注書・発注部材表・臨時部材表のどれから読んでも同じ形になります。
func lineOfRow(head, tr *html.Node) ourOrderLine {
	idx := headerIndex(head)
	cells := cellTexts(tr)
	at := func(label string) string {
		if i, ok := idx[label]; ok && i < len(cells) {
			return strings.TrimSpace(cells[i])
		}
		return ""
	}
	return ourOrderLine{
		ProductID: at("弊社品番"), ItemID: at("品番"), ItemName: at("品名"),
		Material: at("材質"), Shape: at("形状"), Size: at("寸法"), Color: at("表面"),
		Quantity: at("数量"), Unit: at("単位"), Cost: at("単価"), Note: at("備考"),
	}
}

// lineIsEmpty は「書き足す取っ掛かりとして置いた空の行」かを返します。
func lineIsEmpty(ln ourOrderLine) bool {
	return strings.TrimSpace(ln.ItemID+ln.ItemName+ln.Material+ln.Shape+ln.Size+
		ln.Color+ln.Quantity+ln.Unit+ln.Cost+ln.Note) == ""
}

// tempPart は臨時部材表の1行です。Row は**見出しを除いた何行目か**（1始まり）。
type tempPart struct {
	Row  int
	Line ourOrderLine
}

// tempPartsOf は本文の臨時部材表から、空でない行を読みます。
func tempPartsOf(body string) []tempPart {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	tables := tablesOfType(nodes, TempPartsType)
	if len(tables) == 0 {
		return nil
	}
	rows := rowsOf(tables[0])
	if len(rows) < 2 {
		return nil
	}
	var out []tempPart
	for i, tr := range rows[1:] {
		ln := lineOfRow(rows[0], tr)
		if lineIsEmpty(ln) {
			continue
		}
		out = append(out, tempPart{Row: i + 1, Line: ln})
	}
	return out
}

// addTempParts は臨時部材表へ行を足します。**表が無ければ作ります**
// （必要部材表の目印の直前・無ければ本文の末尾）。
//
// ⚠ **空の取っ掛かりの行は、実の行を足すときに取り除きます**。
func addTempParts(body string, lines []ourOrderLine) (string, bool) {
	var real []ourOrderLine
	for _, ln := range lines {
		ln.ProductID = "" // 臨時部材表は弊社品番を持たない
		if !lineIsEmpty(ln) {
			real = append(real, ln)
		}
	}
	if len(real) == 0 {
		return body, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, TempPartsType)
	if len(tables) == 0 {
		return insertTempPartsTable(nodes, tempPartsTableHTML(real)), true
	}
	table := tables[0]
	rows := rowsOf(table)
	for _, tr := range rows[1:] {
		if lineIsEmpty(lineOfRow(rows[0], tr)) {
			tr.Parent.RemoveChild(tr)
		}
	}
	tbody := lastChild(table, "tbody")
	if tbody == nil {
		tbody = table
	}
	for _, ln := range real {
		tbody.AppendChild(tempRowNode(ln))
	}
	return htmldoc.Render(nodes), true
}

// insertTempPartsTable は表を必要部材表の目印の直前へ差し込みます。
func insertTempPartsTable(nodes []*html.Node, tableHTML string) string {
	repl, err := htmldoc.ParseFragment(tableHTML)
	if err != nil || len(repl) == 0 {
		return htmldoc.Render(nodes)
	}
	marker := findElement(nodes, isRequiredPartsMarker)
	if marker == nil {
		return htmldoc.Render(append(nodes, repl...))
	}
	if marker.Parent != nil {
		for _, r := range repl {
			marker.Parent.InsertBefore(r, marker)
		}
		return htmldoc.Render(nodes)
	}
	out := make([]*html.Node, 0, len(nodes)+len(repl))
	for _, n := range nodes {
		if n == marker {
			out = append(out, repl...)
		}
		out = append(out, n)
	}
	return htmldoc.Render(out)
}

// isRequiredPartsMarker は必要部材表の目印（属性形・見出し形）かを返します。
func isRequiredPartsMarker(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "section" {
		return false
	}
	return cms.VocabTypeOf(n) == UnorderedViewType
}

// removeTempPartRows は臨時部材表から rows の行（見出しを除いた何行目か）を消し、
// 消した数を返します。⚠ **全部消えても表は残し、空の1行を置きます**（要求:「行が無い
// ときも、空の表を表示しておきます」）。
func removeTempPartRows(body string, rows []int) (string, int) {
	if len(rows) == 0 {
		return body, 0
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	tables := tablesOfType(nodes, TempPartsType)
	if len(tables) == 0 {
		return body, 0
	}
	table := tables[0]
	trs := rowsOf(table)
	want := map[int]bool{}
	for _, r := range rows {
		want[r] = true
	}
	removed := 0
	for i, tr := range trs[1:] {
		if want[i+1] && tr.Parent != nil {
			tr.Parent.RemoveChild(tr)
			removed++
		}
	}
	if removed == 0 {
		return body, 0
	}
	if len(rowsOf(table)) <= 1 {
		tbody := lastChild(table, "tbody")
		if tbody == nil {
			tbody = table
		}
		tbody.AppendChild(tempRowNode(ourOrderLine{}))
	}
	return htmldoc.Render(nodes), removed
}

// purchaseOrderBoxID は発注フォルダ（トップ直下の「発注」）のページIDです（作りません）。
func purchaseOrderBoxID() (string, bool) {
	return cms.TopLevelPageByTitle(PurchaseOrderBoxTitle)
}

// errBoxBusy は「発注フォルダを誰かが編集中」です。
var errBoxBusy = errors.New("発注フォルダを誰かが編集中です（編集が終わってから、もう一度押してください）")

// returnToTempParts は行を発注フォルダの臨時部材表へ書きます（発注書ページから戻すとき）。
//
// ⚠ **発注フォルダを誰かが編集中なら断ります**——オートセーブと黙って上書きし合います。
// ⚠ 発注フォルダが無ければ作ります（置き場の箱・`EnsureTopLevelBox`）。
func returnToTempParts(user *auth.User, ln ourOrderLine) error {
	boxID, err := cms.EnsureTopLevelBox(PurchaseOrderBoxTitle, user.Username)
	if err != nil {
		return err
	}
	if _, open := editlock.Locks.EditorOpen(pageNum(boxID)); open {
		return errBoxBusy
	}
	return cms.RewriteBody(boxID, user.Username, func(cur string) string {
		out, _ := addTempParts(cur, []ourOrderLine{ln})
		return out
	})
}

// TempPartItems は臨時部材表の行を、必要部材表に並べる形にします。
//
// ⚠ **客先の欄は「臨時部材」**——どこから来た行かが見えないと、計算の行と混ざります。
// ⚠ **数量が空なら 1**（手で書く表で「1個」をいちいち書かせない・`VocabQuantity` と同じ）。
func TempPartItems(user *auth.User, prices map[string]materialPrice) []UnorderedItem {
	boxID, ok := purchaseOrderBoxID()
	if !ok {
		return nil
	}
	id := pageNum(boxID)
	if !viewCheck(user)(id) {
		return nil
	}
	body, err := cms.ReadPageBody(boxID)
	if err != nil {
		return nil
	}
	var out []UnorderedItem
	for _, tp := range tempPartsOf(body) {
		ln := tp.Line
		qty := cms.VocabNumber(ln.Quantity)
		if strings.TrimSpace(ln.Quantity) == "" {
			qty = 1
		}
		name := strings.TrimSpace(strings.Join(nonEmpty(ln.Material, ln.Shape, ln.Size), " "))
		if name == "" {
			name = ln.ItemName
		}
		if name == "" {
			name = ln.ItemID
		}
		if ln.Color != "" {
			name += "（" + ln.Color + "）"
		}
		u := UnorderedItem{
			Client: tempPartsClient, Name: name,
			Material: ln.Material, Shape: ln.Shape, Size: ln.Size,
			Remaining: qty, Cost: cms.VocabNumber(ln.Cost),
			TempPage: boxID, TempRow: tp.Row,
			ItemID: ln.ItemID, ItemName: ln.ItemName, Color: ln.Color,
			Unit: ln.Unit, Note: ln.Note, CostRaw: ln.Cost,
		}
		if u.Cost <= 0 {
			if p, ok := prices[materialKeyOf(ln.Material, ln.Shape, ln.Size)]; ok {
				u.Cost, u.Supplier = p.Cost, p.Supplier
			}
		}
		out = append(out, u)
	}
	return out
}

func nonEmpty(vs ...string) []string {
	var out []string
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// takeTempParts は発注部材表へ入れた行を、臨時部材表から消します（移す）。
//
// lines のうち TempRow を持つものだけを、ページごとにまとめて消します。
// ⚠ **本文を書いているページ（curPage）と同じなら、書き換え関数の中で消せる**よう
// 行番号だけ返します。**別のページなら**ここで書き換えます（編集中なら断る）。
func tempRowsOn(lines []ourOrderLine, pageID string) []int {
	var out []int
	for _, ln := range lines {
		if ln.TempRow > 0 && samePage(ln.TempPage, pageID) {
			out = append(out, ln.TempRow)
		}
	}
	return out
}

func samePage(a, b string) bool {
	na, oka := page.NormalizeID(strings.TrimSpace(a))
	nb, okb := page.NormalizeID(strings.TrimSpace(b))
	return oka && okb && na == nb
}

// tempRowsElsewhere は curPage 以外のページにある臨時部材の行を、ページごとに返します。
func tempRowsElsewhere(lines []ourOrderLine, curPage string) map[string][]int {
	out := map[string][]int{}
	for _, ln := range lines {
		if ln.TempRow <= 0 || strings.TrimSpace(ln.TempPage) == "" || samePage(ln.TempPage, curPage) {
			continue
		}
		p, ok := page.NormalizeID(strings.TrimSpace(ln.TempPage))
		if !ok {
			continue
		}
		out[p] = append(out[p], ln.TempRow)
	}
	return out
}

// takeTempRowsElsewhere は別のページの臨時部材表から行を消します。
func takeTempRowsElsewhere(user *auth.User, lines []ourOrderLine, curPage string) string {
	for p, rows := range tempRowsElsewhere(lines, curPage) {
		if _, open := editlock.Locks.EditorOpen(pageNum(p)); open {
			return "⚠ 臨時部材表から行を消せませんでした（発注フォルダを誰かが編集中です）。手で消してください"
		}
		if err := cms.RewriteBody(p, user.Username, func(cur string) string {
			out, _ := removeTempPartRows(cur, rows)
			return out
		}); err != nil {
			return "⚠ 臨時部材表から行を消せませんでした: " + err.Error()
		}
	}
	return ""
}


// tempRowNode は臨時部材表の1行をノードで組みます（空の行なら空のセル）。
//
// ⚠ **`<tr>` を文字列で組んで断片として解析しないこと**——表の外の文脈で解析すると
// `<tr>`・`<td>` が捨てられ、**セルの文字だけが繋がって表に残ります**（2026-09-25 に
// 番人が捕まえた・「ボルト4個」）。発注部材表へ足す `appendToDraft` と同じ組み方です。
func tempRowNode(ln ourOrderLine) *html.Node {
	tr := &html.Node{Type: html.ElementNode, Data: "tr"}
	for _, c := range columnsOf(TempPartsType) {
		td := &html.Node{Type: html.ElementNode, Data: "td"}
		if v := orderLineValue(ln, c.Field); v != "" {
			td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
		}
		tr.AppendChild(td)
	}
	return tr
}
