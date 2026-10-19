package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページの作業の帯（2026-10-19・【考察】製造ページ・作る順の2番）
//
//	2026-10-19 利用者（§7 の答え）:「2．勧め通りで」——製造完了は製造ページの製造明細表に置き、案件明細の「製造完了」は鏡にする。
//	2026-10-19 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」
//
//   - 製造明細表の「製造完了」の欄に、押せるチェック（鏡）。押すと本文の欄に「済」を書く（`/api/mfg/made`・版が残る）。
//   - 製造明細表の右に「納品」の欄（鏡）——この製造ページで、まだ納めていない数があれば選べる。
//     表の足元の「🚚 選んだ行で納品書を作る」で、納品書をこの製造ページの子に作る（`/api/delivery/new` の `mfg`）。
//   - 「まだ納めていない数」は、製造明細の数量から、この製造ページの子の納品書（`ページ種類：弊社納品書`）の数を引いた数。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

func init() {
	cms.RegisterMirror(MfgItemsType, cms.MirrorHandlerFunc(renderMfgItems))
}

// deliveredUnder は、ページ parent の子の納品書（`ページ種類：弊社納品書`）の行の数を、案件ページ×品番（品番が空なら品名）で足します。
func deliveredUnder(parent int) map[string]float64 {
	out := map[string]float64{}
	rows, err := cms.VocabRowsOfType(database.DB, DeliveryItemsType)
	if err != nil {
		return out
	}
	mine := map[int]bool{}
	for _, r := range rows {
		ok, seen := mine[r.PageID]
		if !seen {
			meta, has := page.ReadSidecar(page.FormatID(r.PageID))
			ok = has && meta.ParentID == page.FormatID(parent) && pageKindOf(r.PageID) == PageKindOurDelivery
			mine[r.PageID] = ok
		}
		if !ok {
			continue
		}
		if q, okQ := estimateNumber(r.Values["quantity"]); okQ && q > 0 {
			out[shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])] += q
		}
	}
	return out
}

// renderMfgItems は製造明細表に、製造完了のチェックと「納品」の欄、足元の帯を出します（鏡）。
func renderMfgItems(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	rows := rowsOf(el)
	if len(rows) < 2 {
		return false, nil
	}
	head := rows[0]
	oi, ii, ni, qi, mi := headerIndexOf(head, "案件"), headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名"),
		headerIndexOf(head, "数量"), headerIndexOf(head, "製造完了")
	if oi < 0 || qi < 0 {
		return false, nil
	}
	writable := ctx.Viewer != nil && canWritePage(ctx.Viewer, ctx.PageID) && !cms.IsTemplateArea(page.FormatID(ctx.PageID))
	at := func(tr *html.Node, i int) string {
		if i < 0 {
			return ""
		}
		return strings.TrimSpace(cellText(tr, i))
	}
	delivered := deliveredUnder(ctx.PageID)
	priceCell(head, "th", "mfg-dlv").AppendChild(&html.Node{Type: html.TextNode, Data: "納品"})
	done, total, open := 0, 0, 0
	for n, tr := range rows[1:] {
		cells := cellsOf(tr)
		qty, okQ := estimateNumber(at(tr, qi))
		if !okQ {
			priceCell(tr, "td", "mfg-dlv")
			continue
		}
		total++
		made := at(tr, mi) == MarkDone
		if made {
			done++
		}
		// 製造完了のチェック（本文の欄の中に、保存されないチェックを足す）。
		if writable && mi >= 0 && mi < len(cells) {
			checked := ""
			if made {
				checked = " checked"
			}
			appendRawChrome(cells[mi], `<input type="checkbox" class="vocab-chrome mfg-made" contenteditable="false" aria-label="製造完了"`+
				` data-row="`+strconv.Itoa(n)+`" data-item="`+stdhtml.EscapeString(at(tr, ii))+`" data-old="`+stdhtml.EscapeString(at(tr, mi))+`"`+checked+`>`)
		}
		left := qty - delivered[shippedKey(at(tr, oi), at(tr, ii), at(tr, ni))]
		td := priceCell(tr, "td", "mfg-dlv")
		switch {
		case left <= 0:
			td.AppendChild(&html.Node{Type: html.TextNode, Data: "納め済み"})
		case writable:
			open++
			appendRawChrome(td, `<input type="checkbox" class="mfg-dlv-pick" title="この行を納品書に載せる"`+
				` data-order="`+stdhtml.EscapeString(at(tr, oi))+`" data-item="`+stdhtml.EscapeString(at(tr, ii))+`" data-name="`+stdhtml.EscapeString(at(tr, ni))+
				`" data-left="`+fmtQty(left)+`"> 残り `+fmtQty(left))
		default:
			td.AppendChild(&html.Node{Type: html.TextNode, Data: "残り " + fmtQty(left)})
		}
	}
	// 見積計算の欄（2026-10-19・mfg_more.go）——⚠ 足元の行の colspan が列の数に合うよう、足元より先に足す。
	appendMfgCalcColumn(ctx, el, writable)
	span := headerCellCount(el)
	appendFootRow(el, span, "mfg-state", "製造完了: "+strconv.Itoa(done)+" / "+strconv.Itoa(total)+" 行")
	if writable {
		pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
		// 部材を発注部材表へ（2026-10-19・作る順の3番）——この製造の、まだ手配していない部材を、この製造ページの発注部材表へ。
		parts := `<button type="button" class="chip-btn mfg-parts-go" data-mfg="` + pid + `">📦 この製造の部材を発注部材表へ</button> <span class="mfg-parts-say"></span>`
		// 部材の見積を頼む（2026-10-19・mfg_more.go）——この製造の部材を、この製造ページの見積依頼必要部材表へ。
		parts += ` <button type="button" class="chip-btn mfg-rfq-go" data-mfg="` + pid + `">📨 この製造の部材を見積依頼必要部材表へ</button> <span class="mfg-rfq-say"></span>`
		// 見積の依頼の行があれば、この製造の見積書（2026-10-19・quote_done.go）。
		if mfgHasQuoteRows(el) {
			parts = `<button type="button" class="chip-btn mfg-estimate-go" data-mfg="` + pid + `">📄 この製造の見積書を作る</button> <span class="mfg-estimate-say"></span> ` + parts
		}
		dlv := ""
		if open > 0 {
			dlv = ` <button type="button" class="chip-btn mfg-dlv-go" data-mfg="` + pid + `">🚚 選んだ行で納品書を作る</button> ` +
				`<span class="mfg-dlv-say"></span><div class="mfg-dlv-panel" hidden></div>`
		}
		appendFootHTML(el, span, "mfg-actions", parts+dlv)
	}
	return false, nil
}

// appendRawChrome は、サーバーが組んだ HTML の断片を el の末尾に足します（断片はこの関数の呼び手がエスケープ済みで組む）。
func appendRawChrome(el *html.Node, fragment string) {
	// ⚠ 文脈の要素には atom が要る（機械が作った td は DataAtom が空で、そのままだと中身が消える）。
	ctxEl := &html.Node{Type: html.ElementNode, Data: el.Data, DataAtom: atom.Lookup([]byte(el.Data))}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctxEl)
	if err != nil {
		return
	}
	for _, n := range nodes {
		el.AppendChild(n)
	}
}

// MfgMadeAPIHandler は POST /api/mfg/made です。入力: {page_id, row, item_no, old, value}（value は「済」か空）。
// 製造ページの製造明細表の「製造完了」の欄を書きます（照合は行番号・品番・いまの値の3つ）。
func MfgMadeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
		ItemNo string `json:"item_no"`
		Old    string `json:"old"`
		Value  string `json:"value"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := cms.PageIDOrFail(w, req.PageID)
	if !okID {
		return
	}
	if v := strings.TrimSpace(req.Value); v != "" && v != MarkDone {
		cms.JSONFail(w, http.StatusBadRequest, "製造完了は「"+MarkDone+"」か空です")
		return
	}
	if pageKindOf(pageNum(pageID)) != PageKindMfg {
		cms.JSONFail(w, http.StatusBadRequest, "製造ページではありません")
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "製造ページを読めません: "+err.Error())
		return
	}
	fixed, err := setItemTableCell(body, MfgItemsType, req.Row, req.ItemNo, "製造完了", req.Old, strings.TrimSpace(req.Value))
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errCellConflict) {
			code = http.StatusConflict
		}
		cms.JSONFail(w, code, err.Error())
		return
	}
	if fixed != body {
		if err := cms.RewriteBody(pageID, user.Username, func(string) string { return fixed }); err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
			return
		}
		auth.Audit(user.Username, "mfg.made", pageID+" 行"+req.ItemNo+" 製造完了="+req.Value)
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "changed": fixed != body})
}

// madeText は案件明細の1行の製造完了の見せ方です（2026-10-19）——全部の数が製造完了の製造に入ったら「済」、一部なら「N/M」、無ければ空。
func madeText(done float64, qty int) string {
	switch {
	case qty > 0 && done >= float64(qty):
		return MarkDone
	case done > 0:
		return fmtQty(done) + "/" + strconv.Itoa(qty)
	}
	return ""
}

// showMadeInOrderTable は案件ページの案件明細表の「製造完了」の欄に、製造ページから数えた製造完了をクロームで出します（鏡）。
// ⚠ 本文の欄には書きません。人が欄に書いた値は使わないので、そう添えます。
func showMadeInOrderTable(ctx *cms.MirrorContext, el *html.Node, mfg *mfgLookup) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	head := rows[0]
	mi, ii, ni, qi := headerIndexOf(head, "製造完了"), headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名"), headerIndexOf(head, "数量")
	if mi < 0 || qi < 0 {
		return
	}
	order := page.FormatID(ctx.PageID)
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if mi >= len(cells) {
			continue
		}
		name := ""
		if ni >= 0 {
			name = cellText(tr, ni)
		}
		item := ""
		if ii >= 0 {
			item = cellText(tr, ii)
		}
		qty, _ := estimateNumber(cellText(tr, qi))
		text := madeText(mfg.doneOf(order, item, name), int(qty))
		if typed := strings.TrimSpace(textOf(cells[mi])); typed != "" {
			text += "（⚠ 欄に書いた " + typed + " は使いません——製造ページで付けます）"
		}
		if text == "" {
			continue
		}
		cells[mi].AppendChild(&html.Node{Type: html.ElementNode, Data: "span",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome made-mirror"}, {Key: "contenteditable", Val: "false"},
				{Key: "title", Val: "製造ページの製造完了から数えた（人は書きません）"}},
			FirstChild: &html.Node{Type: html.TextNode, Data: text}})
	}
}

// mfgPartLines は製造ページ mfgID の製造明細の行ごとに、まだ手配していない部材を、この製造で作る数ぶんまで集めます（2026-10-19・作る順の3番）。
//
// 部材の数は「必要部材表と同じ残り（案件の行ごとに、発注した・発注部材表に入れた・不要にした分を引いた数）」と
// 「この製造で作る数 × 1個あたりの数」の小さいほう。弊社品番（加工製品）の無い行は飛ばします（skipped に数える）。
func mfgPartLines(user *auth.User, mfgID int) (lines []ourOrderLine, skipped int, err error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, 0, err
	}
	items, err := UnorderedItems(user)
	if err != nil {
		return nil, 0, err
	}
	for _, r := range rows {
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		order, okO := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/"))
		qty, okQ := estimateNumber(r.Values["quantity"])
		if !ok || !okO || !okQ || qty <= 0 {
			skipped++
			continue
		}
		need := map[string]int{}
		for _, it := range productNeeds(database.DB, pageNum(pid), int(qty)) {
			need[it.Key] += it.Required
		}
		for _, u := range items {
			if u.TempRow > 0 || page.FormatID(u.OrderPageID) != order || page.FormatID(u.ProductPageID) != pid {
				continue
			}
			q := u.Remaining
			if n, has := need[u.Key]; has && n < q {
				q = n
			}
			if q <= 0 {
				continue
			}
			v := u.Values
			lines = append(lines, ourOrderLine{
				ProductID: pid, ForOrder: order, Kind: u.Kind, No: v["番号"], ItemID: v["品番"], ItemName: v["品名"], Work: v["加工内容"],
				Material: u.Material, Shape: u.Shape, Size: u.Size, Color: v["表面"], Spec: v["仕様"], Supplied: v["支給"],
				Quantity: strconv.Itoa(q), Cost: unorderedCostValue(u),
			})
		}
	}
	return lines, skipped, nil
}

// MfgPartsAPIHandler は POST /api/mfg/parts です。入力: {page_id}（製造ページ）。
// この製造の、まだ手配していない部材を、製造ページの本文に新しい発注部材表として入れます（部材発注書はその表の「部材発注書を作る」から——製造ページの子にできる）。
func MfgPartsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if pageKindOf(pageNum(pageID)) != PageKindMfg {
		cms.JSONFail(w, http.StatusBadRequest, "製造ページではありません")
		return
	}
	lines, skipped, err := mfgPartLines(user, pageNum(pageID))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "部材を数えられません: "+err.Error())
		return
	}
	if len(lines) == 0 {
		msg := "この製造で発注する部材はありません（手配済みか、加工製品の部材の表に行がありません）"
		if skipped > 0 {
			msg += "。弊社品番の無い行が " + strconv.Itoa(skipped) + " 行あります"
		}
		cms.JSONFail(w, http.StatusBadRequest, msg)
		return
	}
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, _, _ := putLinesIntoDraft(cur, "", lines)
		return out
	}) {
		return
	}
	auth.Audit(user.Username, "mfg.parts", pageID+" "+strconv.Itoa(len(lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": len(lines), "skipped": skipped})
}
