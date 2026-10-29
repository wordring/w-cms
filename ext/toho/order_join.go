package toho

// 相乗り——発注部材表の行を、まだ送っていない部材発注書へ足す（2026-10-21・【考察】製造単位の部材発注 §6.3 の6番）。
//
// 2026-10-21 利用者:「記録以下に保存された書類（例えば発注書）に別の製造ページが相乗りして良いことにしてはどうでしょう？これで複数の案件をまとめて部材発注が出来ます。」
// 2026-10-21 利用者:「相乗りする発注書のページIDを指定で良いと思います。「同じ仕入れ先の～」のような相乗り候補を知らせてくれる機能があればさらに良いです」
//
//   - 発注部材表の足元の欄に、相乗りする部材発注書のページ番号を書くと、新しい部材発注書を作らず、その部材発注書に足します。
//   - 足せるのは、まだ1行も送っていない部材発注書だけです（§5.5 問い1）。仕入先が違えば断ります。
//   - 同じ部材の行（数量・案件・弊社品番・製造・内訳のほかが同じ・取消でない行）には数を足し、内訳・案件・弊社品番・製造を書き足します。
//     違う行は、同じ部材の行のすぐ下に（無ければ表の末尾に）足します。
//   - 欄の下に、同じ仕入先のまだ送っていない部材発注書を候補として並べます（押すと欄にページ番号が入る）。

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// joinKey は相乗りで「同じ部材の行」と見る鍵です（数量・案件・弊社品番・製造・内訳・推奨業者は見ない）。
func joinKey(ln ourOrderLine) string {
	parts := []string{ln.Kind, ln.No, ln.ItemID, itemNameOf(ln), ln.Work, ln.Material, ln.Shape, ln.Size, ln.Color,
		ln.Spec, ln.Supplied, ln.Unit, costCell(ln.Cost), ln.Note}
	for i, p := range parts {
		parts[i] = cms.NormalizeText(strings.TrimSpace(p))
	}
	return strings.Join(parts, "\x1f")
}

// partHeadKey は「同じ部材」の大まかな鍵です（違う行を、その部材の行のすぐ下に足すため）。
func partHeadKey(ln ourOrderLine) string {
	return cms.NormalizeText(ln.Kind + "\x1f" + ln.Material + "\x1f" + ln.Shape + "\x1f" + ln.Size + "\x1f" + itemNameOf(ln) + "\x1f" + ln.No)
}

// splitRefs は「、」で並べた参照の値を分けます（`000040、000042`）。
func splitRefs(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == '、' || r == ',' || r == '，' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// joinRefs は参照の値を重ねずに「、」で並べます。
func joinRefs(vs ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vs {
		for _, p := range splitRefs(v) {
			id := p
			if n, ok := page.NormalizeID(strings.TrimPrefix(p, "/")); ok {
				id = n
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return strings.Join(out, "、")
}

// ensureColumnAfter は表に見出し label の列が無ければ、after の列の右（無ければ右端）に足します（どの行にも空のセル）。
func ensureColumnAfter(table *html.Node, label, after string) {
	rows := rowsOf(table)
	if len(rows) == 0 || headerIndexOf(rows[0], label) >= 0 {
		return
	}
	at := headerIndexOf(rows[0], after)
	for i, tr := range rows {
		tag := "td"
		if i == 0 {
			tag = "th"
		}
		cell := &html.Node{Type: html.ElementNode, Data: tag}
		if i == 0 {
			cell.AppendChild(&html.Node{Type: html.TextNode, Data: label})
		}
		cells := rowCellNodes(tr)
		if at >= 0 && at < len(cells) {
			tr.InsertBefore(cell, cells[at].NextSibling)
		} else {
			tr.AppendChild(cell)
		}
	}
}

// rowCellNodes は行のセル（th・td）を並びの順に返します。
func rowCellNodes(tr *html.Node) []*html.Node {
	var out []*html.Node
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
			out = append(out, c)
		}
	}
	return out
}

// setRowCell は行 tr の、見出し label の列のセルに s を書きます（列が無ければ何もしない）。
func setRowCell(head, tr *html.Node, label, s string) {
	i := headerIndexOf(head, label)
	cells := rowCellNodes(tr)
	if i < 0 || i >= len(cells) {
		return
	}
	setCellText(cells[i], s)
}

// mergeIntoOrderTable は本文 body の最初の部材発注明細表へ lines を足します。
// summed は数を足した行の数、added は新しく足した行の数です。表が無ければ ok=false。
func mergeIntoOrderTable(body string, lines []ourOrderLine) (out string, added, summed int, ok bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0, 0, false
	}
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return body, 0, 0, false
	}
	table := tables[0]
	// それまでの部材発注書には「製造」「内訳」の列が無い——足してから書く。
	ensureColumnAfter(table, MfgRefTag, "案件")
	ensureColumnAfter(table, breakdownLabel, MfgRefTag)
	rows := rowsOf(table)
	if len(rows) == 0 {
		return body, 0, 0, false
	}
	head := rows[0]
	si := headerIndexOf(head, "状態")
	tbody := lastChild(table, "tbody")
	if tbody == nil {
		tbody = table
	}
	fields := fieldsOfHeader(table, ourOrderItemsType)
	for _, nl := range lines {
		var match, lastSame *html.Node
		for _, tr := range rowsOf(table)[1:] {
			el := lineOfRow(head, tr)
			if lineIsEmpty(el) {
				continue
			}
			if si >= 0 && orderLineCancelled(strings.TrimSpace(cellText(tr, si))) {
				continue
			}
			if partHeadKey(el) == partHeadKey(nl) {
				lastSame = tr
			}
			if match == nil && joinKey(el) == joinKey(nl) {
				match = tr
			}
		}
		if match != nil {
			el := lineOfRow(head, match)
			oldQty, err1 := strconv.Atoi(strings.TrimSpace(el.Quantity))
			addQty, err2 := strconv.Atoi(strings.TrimSpace(nl.Quantity))
			oldEs, ok1 := entriesOf(el)
			newEs, ok2 := entriesOf(nl)
			if err1 == nil && err2 == nil && ok1 && ok2 {
				es := mergeEntries(append(oldEs, newEs...))
				setRowCell(head, match, "数量", strconv.Itoa(oldQty+addQty))
				setRowCell(head, match, "案件", joinEntries(es, func(e breakdownEntry) string { return e.Order }))
				setRowCell(head, match, "弊社品番", joinEntries(es, func(e breakdownEntry) string { return e.Product }))
				setRowCell(head, match, MfgRefTag, joinRefs(el.Mfg, nl.Mfg))
				bd := ""
				if len(es) > 1 {
					bd = formatBreakdown(es)
				}
				setRowCell(head, match, breakdownLabel, bd)
				summed++
				continue
			}
		}
		vals := ourOrderRowValues(nl)
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for _, field := range fields {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			if v := vals[field]; v != "" {
				td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
			}
			tr.AppendChild(td)
		}
		if lastSame != nil && lastSame.Parent != nil {
			lastSame.Parent.InsertBefore(tr, lastSame.NextSibling)
		} else {
			tbody.AppendChild(tr)
		}
		added++
	}
	return htmldoc.Render(nodes), added, summed, true
}

// mergeEntries は同じ案件・同じ加工製品の内訳を足し合わせます（並びは先に出てきた順）。
func mergeEntries(in []breakdownEntry) []breakdownEntry {
	var out []breakdownEntry
	at := map[string]int{}
	for _, e := range in {
		k := e.Order + "/" + e.Product
		if i, ok := at[k]; ok {
			out[i].Qty += e.Qty
			continue
		}
		at[k] = len(out)
		out = append(out, e)
	}
	return out
}

// joinableOrder は、部材発注書 id に相乗りできるかを確かめます（できなければ HTTP の番号と理由）。
func joinableOrder(user *auth.User, id, supplier string) (int, string) {
	n := pageNum(id)
	if pageKindOf(n) != PageKindOurPurchase || cms.IsTemplateArea(id) {
		return http.StatusBadRequest, "/" + id + " は部材発注書ではありません"
	}
	if !page.CanView(user, n) {
		return http.StatusNotFound, "/" + id + " の部材発注書がありません"
	}
	if !canWritePage(user, n) {
		return http.StatusForbidden, "/" + id + " の部材発注書に書けません"
	}
	if c := OrderSendStateOf(database.DB, n); c.Sent > 0 {
		return http.StatusConflict, "/" + id + " の部材発注書は送ってあるので足せません（新しい部材発注書を作ってください）"
	}
	if have := strings.TrimSpace(cms.PageTagValue(database.DB, n, SupplierTag)); cms.NormalizeText(have) != cms.NormalizeText(supplier) {
		return http.StatusConflict, "/" + id + " の部材発注書は仕入先が「" + have + "」です（「" + supplier + "」とは違います）"
	}
	return 0, ""
}

// joinCandidates は、仕入先 supplier のまだ送っていない部材発注書（user が書けるもの・古い順）です。
// supplier が空なら（推奨業者の無い材料の表など）、仕入先を問わず全部です。
func joinCandidates(user *auth.User, supplier string) []UnsentOrder {
	if user == nil {
		return nil
	}
	list, err := UnsentOrders(user)
	if err != nil {
		return nil
	}
	want := cms.NormalizeText(strings.TrimSpace(supplier))
	var out []UnsentOrder
	for _, o := range list {
		if o.Unsent != o.Total || (want != "" && cms.NormalizeText(strings.TrimSpace(o.Supplier)) != want) {
			continue // 1行でも送った（取消を含む）・仕入先が違う
		}
		if cms.IsTemplateArea(page.FormatID(o.PageID)) || !canWritePage(user, o.PageID) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// joinFieldHTML は発注部材表の足元の欄の「相乗り」の部分です（欄と候補）。
func joinFieldHTML(user *auth.User, supplier string) string {
	var b strings.Builder
	b.WriteString(`<label class="matsearch-field"><span>相乗りする部材発注書</span>` +
		`<input type="text" class="matsearch-input" data-draft="join" placeholder="ページ番号（空なら新しく作る）"/></label>`)
	if cs := joinCandidates(user, supplier); len(cs) > 0 {
		lead := "同じ仕入先のまだ送っていない部材発注書: "
		if strings.TrimSpace(supplier) == "" {
			lead = "まだ送っていない部材発注書（仕入先を合わせてください）: "
		}
		b.WriteString(`<div class="draft-join-cands">` + stdhtml.EscapeString(lead))
		for _, c := range cs {
			id := page.FormatID(c.PageID)
			b.WriteString(`<button type="button" class="chip-btn draft-join-pick" data-join="` + id + `" title="この部材発注書に足す（欄にページ番号が入ります）">/` +
				id + ` ` + stdhtml.EscapeString(c.Title) + `</button> `)
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

// joinOurOrder は NewOurOrderAPIHandler の相乗りの道です——発注部材表の lines を、部材発注書 joinIn に足して、元の表を消します。
func joinOurOrder(w http.ResponseWriter, user *auth.User, joinIn, supplier string, lines []ourOrderLine, draftPage, draftIndex, draftSig string) {
	id, ok := refPageID(joinIn)
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "相乗りする部材発注書のページ番号が読めません: "+joinIn)
		return
	}
	if code, why := joinableOrder(user, id, supplier); code != 0 {
		cms.JSONFail(w, code, why)
		return
	}
	added, summed := 0, 0
	var fail string
	if !rewriteBodyOrFail(w, id, user.Username, func(cur string) string {
		// 送ったかは、ロックの中で読んだ本文でもう一度見る（索引で確かめてからここまでのあいだに送られると、送った行へ数が足された・
		// 2026-10-22 の見直し）。
		if orderBodyHasSentLine(cur) {
			fail = "/" + id + " の部材発注書は送ってあるので足せません（新しい部材発注書を作ってください）"
			return cur
		}
		out, a, s, ok := mergeIntoOrderTable(cur, lines)
		if !ok {
			fail = "/" + id + " に部材発注明細表がありません"
			return cur
		}
		added, summed = a, s
		return out
	}) {
		return
	}
	if fail != "" {
		cms.JSONFail(w, http.StatusConflict, fail)
		return
	}
	auth.Audit(user.Username, "our-order.join", id+" "+supplier+" 足した "+strconv.Itoa(added)+"行・数を足した "+strconv.Itoa(summed)+"行")
	out := map[string]any{"success": true, "page_id": id, "url": "/" + id, "joined": true, "added": added, "summed": summed}
	if note := removeDraftAfterOrder(user, draftPage, draftIndex, draftSig); note != "" {
		out["draft_note"] = note
	}
	cms.WriteJSON(w, out)
}

// orderBodyHasSentLine は、本文の部材発注明細表に送った行（発注済・納品済）があるかです。
func orderBodyHasSentLine(body string) bool {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	for _, table := range tablesOfType(nodes, ourOrderItemsType) {
		rows := rowsOf(table)
		if len(rows) == 0 {
			continue
		}
		si := headerIndexOf(rows[0], "状態")
		if si < 0 {
			continue
		}
		for _, tr := range rows[1:] {
			if orderLineSent(strings.TrimSpace(cellText(tr, si))) {
				return true
			}
		}
	}
	return false
}
