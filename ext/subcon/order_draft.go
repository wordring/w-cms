package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注部材表——人が足し引きする候補の表（2026-09-22）
//
// ユーザー:「候補の表は**発注ページの未発注の表の下**で良いのでは？未発注の表の
// チェックボックスをクリックして（**必要なければ何もクリックしなくても**）**表作成
// ボタン**をクリックすると**発注部材表**が開き、そこを編集して**発注書作成**を
// クリックすると、**発注書ページ**ができ…」
//
//	発注（ページ）
//	  ├ 未発注の表     … 鏡（毎回計算・読み取り専用）
//	  │    ↓ チェック（何もクリックしなくてもよい）→ [表作成]
//	  ├ 発注部材表     … **本文の表**（人が足し引きする）  ← ここ
//	  │    ↓ 編集して → [発注書作成]
//	  └ 2026年／09月／発注 みなと商店
//
// ── なぜ本文に書いてよいのか ───────────────────────────────────────────
//
// ⚠ **人が足し引きした結果は「推測」ではなく「事実」だからです。**
// 「**今回は**自社加工する」「この消耗品も一緒に頼む」は**機械には導けません**——
// だから「印と事実を二重に持たない」（[【考察】製造という軸.md] §9）とぶつかりません。
//
// ⚠ **未手配の一覧（鏡）とは役割が違います。** あちらは受注明細と発注明細から
// **毎回計算される候補**で、こちらは**人が決めた結果**です。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// NewOrderDraftAPIHandler は POST /api/our-order/draft です。
//
// 発注ページの本文へ**発注部材表を1つ**足します（入力: {page_id, lines}）。
//
// ⚠ **行が0でも作ります**（ユーザー:「必要なければ何もクリックしなくても」）——
// **空の表から始められる**ことが、加工製品ページに無い部材（消耗品・治具）だけを
// 買うときの道です。
func NewOrderDraftAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string         `json:"page_id"`
		Lines  []ourOrderLine `json:"lines"`
		// Into は足す先の発注部材表（**何枚目か**・1始まり）。空なら**新しく作ります**。
		//
		// ⚠ **既定が「新しく作る」なのは、黙って混ぜるほうが危ないから**です——
		// 発注書は**1枚に1社**なので、別の業者の行が混ざると**紙にしてから気づきます**。
		Into string `json:"into"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := page.NormalizeID(req.PageID)
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// 本文へ表を足すので write が要ります。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// ⚠ **機械が既存ページの本文を書き換えるときの関門**（`RewriteBody` は読んで・
	//    変えて・書くので、エディタが開いているとオートセーブと上書きし合います）。
	//    ⚠ **自分が開いていても断ります。**
	if !editlock.RefuseWhileEditing(w, pageID) {
		return
	}

	// ⚠ **発注部材表は何枚あってもかまいません**（2026-09-22 ユーザー訂正）。
	//    **1枚＝1社**なので、**業者ごとに同時に進める**のが普通の形です——
	//    ⚠ **最初の実装は2枚目を 409 で断っていました。実務が回りません。**
	//
	//    だから**新しく作るのか、どれかに足すのか**を人が選びます。
	into := strings.TrimSpace(req.Into)
	rows := 0
	found := into == "" // 新規なら「行き先が見つかった」扱い
	werr := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		if into == "" {
			rows = len(req.Lines)
			return cur + orderDraftHTML(req.Lines)
		}
		merged, n, ok := appendToDraft(cur, into, req.Lines)
		found = ok
		if !ok {
			return cur
		}
		rows = n
		return merged
	})
	if werr != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+werr.Error())
		return
	}
	if !found {
		cms.JSONFail(w, http.StatusConflict,
			"足す先の発注部材表が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "order-draft.create",
		pageID+" "+itoa(len(req.Lines))+"行 into="+into)
	cms.WriteJSON(w, map[string]any{
		"success": true, "page_id": pageID, "rows": rows, "into": into})
}

// appendToDraft は本文の `into` 枚目の発注部材表へ行を足します。
//
// ⚠ **表は木で数えます**——`data-type` でも `<caption>` でも名乗れるので、
// 文字列で探すと**キャプションで名乗った表を見落とします**。
func appendToDraft(body, into string, lines []ourOrderLine) (out string, added int, ok bool) {
	n, err := strconv.Atoi(into)
	if err != nil || n < 1 {
		return body, 0, false
	}
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return body, 0, false
	}
	tables := tablesOfType(nodes, OrderDraftType)
	if n > len(tables) {
		return body, 0, false
	}
	tbody := lastChild(tables[n-1], "tbody")
	if tbody == nil {
		tbody = tables[n-1]
	}
	for _, ln := range lines {
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for _, c := range columnsOf(OrderDraftType) {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			if v := orderLineValue(ln, c.Field); v != "" {
				td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
			}
			tr.AppendChild(td)
		}
		tbody.AppendChild(tr)
		added++
	}
	return htmldoc.Render(nodes), added, true
}

// CountOrderDrafts はページの本文にある発注部材表の数を返します。
//
// ⚠ **画面が「新しく作る／1枚目へ足す／2枚目へ足す」を出すため**に要ります。
func CountOrderDrafts(pageID int) int {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return 0
	}
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return 0
	}
	return len(tablesOfType(nodes, OrderDraftType))
}

// orderDraftHTML は発注部材表のHTMLを組みます。
//
// ⚠ **見出し行は宣言から組みます**（`headerRowHTML`）——手書きに戻すと、列を足した日に
// **足した列がどこからも読めません**。
//
// ⚠ **行が0でも表の骨は出します**——人がそこへ書き足せるように。**空の表は
// 「作れなかった」ではなく「これから書く」**です。
func orderDraftHTML(lines []ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<table data-type="` + OrderDraftType + `"><caption>` +
		stdhtml.EscapeString(displayNameOf(OrderDraftType)) + `</caption><tbody>`)
	b.WriteString(headerRowHTML(OrderDraftType))
	for _, ln := range lines {
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(OrderDraftType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(orderLineValue(ln, c.Field)) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	if len(lines) == 0 {
		// ⚠ **空の行を1つ置きます**——見出しだけの表は、エディタで行を足す取っ掛かりが
		//    ありません（人が「ここへ書く」と分かる形にする）。
		b.WriteString(`<tr>`)
		for range columnsOf(OrderDraftType) {
			b.WriteString(`<td></td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// orderLineValue は画面から来た1行から、列の値を取り出します。
//
// ⚠ **`buildOurOrderHTML` と同じ規則**を通します——材料の行に `品名` を書かない・
// 単価 `0` は書かない。**発注部材表と発注書で値が違うと、人はどちらを信じてよいか
// 分かりません。**
func orderLineValue(ln ourOrderLine, field string) string {
	switch field {
	case "our-item-id":
		return strings.TrimSpace(ln.ProductID)
	case "item-name":
		return itemNameOf(ln)
	case "material":
		return strings.TrimSpace(ln.Material)
	case "shape":
		return strings.TrimSpace(ln.Shape)
	case "size":
		return strings.TrimSpace(ln.Size)
	case "quantity":
		return strings.TrimSpace(ln.Quantity)
	case "unit":
		return strings.TrimSpace(ln.Unit)
	case "cost":
		return moneyOrEmpty(ln.Cost)
	case "note":
		return strings.TrimSpace(ln.Note)
	case "status":
		// ⚠ **発注部材表には「状態」を書きません**——まだ発注していないので、
		//    `未納品` と書くと**もう注文したように見えます**。
		return ""
	}
	return ""
}

// isTableOfTypeInBody は本文にその形式の表があるかを返します。
//
// ⚠ **文字列では探しません**——表は `data-type` でも `<caption>` でも名乗れるので
// （2026-09-20）、`strings.Contains` だと**キャプションで名乗った表を見落とします**。
func isTableOfTypeInBody(body, vocabType string) bool {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	return len(tablesOfType(nodes, vocabType)) > 0
}

// itoa は小さな数を文字列にします（監査の文のため）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// replaceDraftWithLink は、元になった発注部材表を発注書ページへのリンクに置き換えます。
//
// ユーザー（2026-09-22）:「**発注書ページが出来て、実際に発注するまで発注ページに
// 発注書ページへのリンクが残れば良いのでは？**」
//
// ⚠ **表は消して、リンクを残します**——中身は発注書ページへ移ったので、**残すと
// 古い写しになり、次の発注のときに混ざります**。⚠ **リンクは残します**：
// **まだ発注していないもの**が発注ページの上で一目で分かるように。
//
// ⚠ **うまくいかなくても発注書は取り消しません**——**紙のほうが重い**ので、
// 「表が残ってしまった」は人が消せば済みます。返すのは**添える一文**だけです。
func replaceDraftWithLink(user *auth.User, draftPage, draftIndex, orderID, supplier string,
	rows int) string {
	if strings.TrimSpace(draftPage) == "" || strings.TrimSpace(draftIndex) == "" {
		return "" // どの表から作ったか分からない（画面が古いときなど）
	}
	pageID, ok := page.NormalizeID(draftPage)
	if !ok {
		return "⚠ 元の発注部材表を片付けられません（ページIDが不正です）"
	}
	n, err := strconv.Atoi(strings.TrimSpace(draftIndex))
	if err != nil || n < 1 {
		return "⚠ 元の発注部材表を片付けられません（何枚目か分かりません）"
	}
	// ⚠ **開いている人が居たら触りません**——オートセーブと上書きし合います。
	if _, open := editlock.Locks.EditorOpen(pageNum(pageID)); open {
		return "⚠ 元の発注部材表はそのままです（誰かが発注ページを編集中です）"
	}
	// ⚠ **「まだ発注していません」は本文に書きません**（2026-09-22）。
	//    **出したら消えなければならない文**なので、本文に焼き込むと**発注書を出した
	//    あとも古いまま**残ります。進み具合は鏡が発注書ページから読み直します
	//    （[order_link_mirror.go](order_link_mirror.go)）。
	link := orderLinkHTML(orderID, supplier, rows)
	done := false
	if werr := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, ok := replaceDraftTable(cur, n, link)
		done = ok
		if !ok {
			return cur
		}
		return out
	}); werr != nil {
		return "⚠ 元の発注部材表を片付けられません: " + werr.Error()
	}
	if !done {
		return "⚠ 元の発注部材表が見つかりませんでした（画面を読み直してください）"
	}
	return ""
}

// replaceDraftTable は本文の n 枚目の発注部材表を、渡したHTMLで置き換えます。
//
// ⚠ **木で探して木で差し替えます**——文字列でやると、`<caption>` で名乗った表や
// 入れ子の `</table>` で切り損ねます。
func replaceDraftTable(body string, n int, replacementHTML string) (string, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, OrderDraftType)
	if n < 1 || n > len(tables) {
		return body, false
	}
	target := tables[n-1]
	repl, rerr := htmldoc.ParseFragment(replacementHTML)
	if rerr != nil {
		return body, false
	}
	// ⚠ **トップレベルの表には `Parent` がありません**（`ParseFragment` は根の無い
	//    ノード列を返す）。**本文の直下に置かれた表がまさにそれ**なので、
	//    その場合はノード列のほうを組み替えます。
	if target.Parent == nil {
		out := make([]*html.Node, 0, len(nodes)+len(repl))
		hit := false
		for _, nd := range nodes {
			if nd == target {
				hit = true
				out = append(out, repl...)
				continue
			}
			out = append(out, nd)
		}
		if !hit {
			return body, false
		}
		return htmldoc.Render(out), true
	}
	parent := target.Parent
	for _, r := range repl {
		parent.InsertBefore(r, target)
	}
	parent.RemoveChild(target)
	return htmldoc.Render(nodes), true
}

// draftedQty は「**いま発注部材表に入っている数**」を、加工製品×購入品ごとに返します。
//
// ユーザー（2026-09-22）:「未発注の表のチェックボックスをクリックして…表作成ボタンを
// クリックすると発注部材表が開き、**すると元の表からはそれらの行が消えます**」
//
// ⚠ **1段目の実装はこれを落としていました**——発注部材表に入れても未手配の一覧に
// 残り、**二重に出ていました**（実データで確認・同日）。**同じものを2回発注しかねません。**
//
// ⚠ **鍵は `orderedByProduct` と同じ**（`procKey`）です——発注書と発注部材表で
// 別の束ね方をすると、**引き算が合わなくなります**。
func draftedQty(db cms.ReadOnlyDB, canView func(int) bool) map[string]int {
	rows, err := cms.VocabRowsOfType(db, OrderDraftType)
	if err != nil {
		return map[string]int{}
	}
	out := map[string]int{}
	for _, r := range rows {
		if !canView(r.PageID) {
			continue
		}
		productID, ok := page.NormalizeID(strings.TrimSpace(r.Values["our-item-id"]))
		if !ok || productID == "" {
			continue // ⚠ 弊社品番の無い行（消耗品など）は、そもそも一覧に出ません
		}
		key := materialKeyOf(r.Values["material"], r.Values["shape"], r.Values["size"])
		if key == "" {
			key = cms.NormalizeText(strings.TrimSpace(r.Values["item-name"]))
		}
		if key == "" {
			continue
		}
		out[procKey(pageNum(productID), key)] += cms.VocabQuantity(r)
	}
	return out
}

// RemoveOrderDraftRowAPIHandler は POST /api/our-order/draft/remove です。
//
// 発注部材表から行を1つ外します（入力: {page_id, table, row}）。
//
// ユーザー（2026-09-22）:「**発注部材表から未手配の一覧へ戻す方法がありません**」
//
// ⚠ **「戻す」は「外す」です。** 一覧は**毎回計算される鏡**なので、発注部材表から
// 消せば**自動的に戻ってきます**——戻す先へ何かを書く必要はありません。
//
// ⚠ **閲覧モードから押せることが肝**です。本文の表なのでエディタでも消せますが、
// **一覧を見ながら出し入れする**のに、いちいち編集モードへ入るのは道が遠すぎます。
func RemoveOrderDraftRowAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Table  int    `json:"table"` // 何枚目の発注部材表か（1始まり）
		Row    int    `json:"row"`   // 見出しを除いた何行目か（1始まり）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := page.NormalizeID(req.PageID)
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	if !editlock.RefuseWhileEditing(w, pageID) {
		return
	}
	done := false
	if werr := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, ok := removeDraftRow(cur, req.Table, req.Row)
		done = ok
		if !ok {
			return cur
		}
		return out
	}); werr != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+werr.Error())
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict,
			"その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "order-draft.remove",
		pageID+" 表"+itoa(req.Table)+" 行"+itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// removeDraftRow は n 枚目の発注部材表から row 行目（見出しを除く）を外します。
//
// ⚠ **見出し行は数えません**——人が画面で見ている「何行目」と揃えるためです。
// ⚠ **鏡が足した `<tfoot>` の行も数えません**（あれは本文ではありません）。
func removeDraftRow(body string, n, row int) (string, bool) {
	if n < 1 || row < 1 {
		return body, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, OrderDraftType)
	if n > len(tables) {
		return body, false
	}
	var body0 *html.Node
	for c := tables[n-1].FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "tbody" {
			body0 = c
			break
		}
	}
	if body0 == nil {
		body0 = tables[n-1]
	}
	i := 0
	for c := body0.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "tr" {
			continue
		}
		if i == 0 { // 見出し行
			i++
			continue
		}
		if i == row {
			body0.RemoveChild(c)
			// ⚠ **最後の1行を外したら、表ごと消します**（2026-09-22 ユーザー:
			//    「**戻しても部材表から消えません**」）。**見出しだけの表が残ると、
			//    発注ページに空の表が溜まります**——しかも「何枚目へ足すか」の選択肢に
			//    並ぶので、**押し間違いの元**になります。
			//    ⚠ **空の表を作る道は残します**（何も選ばずに「発注部材表へ入れる」）
			//    ——**人が意図して作った空の表**と、**外して空になった表**は別のことです。
			if !hasBodyRow(body0) {
				return dropTable(nodes, tables[n-1])
			}
			return htmldoc.Render(nodes), true
		}
		i++
	}
	return body, false
}

// hasBodyRow は、見出しを除いた行が1つでも残っているかを返します。
func hasBodyRow(tbody *html.Node) bool {
	n := 0
	for c := tbody.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "tr" {
			n++
			if n > 1 { // 見出し行のほかに1つでもあれば
				return true
			}
		}
	}
	return false
}

// dropTable は表そのものをノード列から外します。
//
// ⚠ **トップレベルの表には `Parent` がありません**（`ParseFragment` は根の無いノード列を
// 返す）——**本文の直下に置かれた表がまさにそれ**なので、ノード列のほうを組み替えます。
func dropTable(nodes []*html.Node, target *html.Node) (string, bool) {
	if target.Parent != nil {
		target.Parent.RemoveChild(target)
		return htmldoc.Render(nodes), true
	}
	out := make([]*html.Node, 0, len(nodes))
	hit := false
	for _, nd := range nodes {
		if nd == target {
			hit = true
			continue
		}
		out = append(out, nd)
	}
	if !hit {
		return htmldoc.Render(nodes), false
	}
	return htmldoc.Render(out), true
}
