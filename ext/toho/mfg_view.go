package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページの作業の帯（2026-10-19・【考察】製造ページ・作る順の2番）
//
//	2026-10-19 利用者（§7 の答え）:「2．勧め通りで」——製造完了は製造ページの製造明細表に置き、案件明細の「製造完了」は鏡にする。
//	2026-10-19 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」
//
//   - 製造明細表の「製造完了」の欄に、押せるチェック（鏡）。押すと本文の欄に「済」を書く（`/api/mfg/made`・版が残る）。
//   - 製造明細表の右に「納品」の欄（鏡）——この製造ページで、まだ納めていない数があれば選べる。
//     表の足元の「🚚 選んだ行で納品書を作る」で、納品書を作る（`/api/delivery/new` の `mfg`）（2026-10-21 から置き場は記録・明細の「製造」の列でつながる）。
//   - 「まだ納めていない数」は、製造明細の数量から、この製造の納品書（`ページ種類：弊社納品書`・mfgDeliveryRows）の数を引いた数。
// ─────────────────────────────────────────────────────────────────────────

import (
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

// deliveredUnder は、製造ページ mfg の分の納品書（`ページ種類：弊社納品書`）の行の数を、案件ページ×品番（品番が空なら品名）で足します。
// どの行が「この製造の分」かは mfgDeliveryRows（明細の「製造」の列・それより前の納品書は製造ページの子かどうか）。
func deliveredUnder(mfg int) map[string]float64 {
	out := map[string]float64{}
	for _, r := range mfgDeliveryRows(mfg) {
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
	oi, ii, ni, qi, mi, pi := headerIndexOf(head, "案件"), headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名"),
		headerIndexOf(head, "数量"), headerIndexOf(head, "製造完了"), headerIndexOf(head, OurItemNoTag)
	if oi < 0 || qi < 0 {
		return false, nil
	}
	writable := writableMirror(ctx)
	at := func(tr *html.Node, i int) string {
		if i < 0 {
			return ""
		}
		return strings.TrimSpace(cellText(tr, i))
	}
	// ⚠ 表の中には、本文の値と行の先頭のチェックだけを置く（2026-10-21 夕・mfg_work.go——利用者:「この表はご法度のはずの一緒にボタンを入れています」）。
	//    ボタンは表の下の帯に、「この製造の書類」と「閉じる」はページの末尾に。
	delivered := deliveredUnder(ctx.PageID)
	quote := mfgHasQuoteRows(el) // 列を足す前に数える
	open := mfgIsOpen(ctx.PageID)
	pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
	// 帯の頭の「製造完了: N / M 行」は 2026-10-23 に外した（利用者:「製造完了: 0 / 1 行　は必要ないです」）。
	if writable {
		prependRowChromeCells(el, "mfg-pick-cell", func(n int, tr *html.Node) string {
			left := 0.0
			if qty, ok := estimateNumber(at(tr, qi)); ok {
				left = qty - delivered[shippedKey(at(tr, oi), at(tr, ii), at(tr, ni))]
			}
			if left < 0 {
				left = 0
			}
			return pickHTML("mfg-pick", map[string]string{"row": strconv.Itoa(n), "item": at(tr, ii), "name": at(tr, ni), "order": at(tr, oi),
				"left": fmtQty(left), "made": at(tr, mi), "product": strings.TrimPrefix(at(tr, pi), "/")})
		})
		bar := `<span class="mfg-bar-page" data-mfg="` + pid + `"></span>` +
			// 「➕ 追加の発注」は 2026-10-23 に外した——もう部材を手配した行で押すと、警告して「追加の発注」を選べる（mfg_work.go の mfgStartedRows）。
			barButton("mfg-act-parts", "🧾 部材作業", "選んだ行の、まだ手配していない部材を集めて、「部材作業」の見出しの下に必要部材表を作ります（もう手配した行があれば、確かめます）") +
			barButton("mfg-act-dlv", "🚚 納品作業", "選んだ行の、まだ納めていない数で、「納品作業」の見出しの下に納品表を作ります") +
			barButton("mfg-act-made", "✓ 製造完了", "選んだ行の「製造完了」に「済」を書きます") +
			barButton("mfg-act-calc", "🧮 見積もり計算作業", "選んだ行の加工製品の、新しい見積計算書を作ります（記録の年月・タグ「製造」でこの製造とつながる）")
		if quote {
			bar += barButton("mfg-act-estimate", "📄 見積書", "この製造の見積の依頼の行から、見積書を作ります")
		}
		if open { // 製造待ちへ戻すのは「製造」の直下にあるあいだだけ（mfg_wait.go の MfgBackAPIHandler）
			// 2026-10-23 までの名前は「↩ 取り消し」（利用者:「取り消しボタンは製造フォルダへ戻すに改名します」）。
			bar += barButton("mfg-act-back", "↩ 製造フォルダへ戻す", "選んだ行を製造明細から外して、「製造」の製造待ちへ戻します（この製造で納めた分がある行は戻せません）")
		}
		bar += `<span class="mfg-bar-say"></span>`
		appendBarAfter(el, "mfg-items-bar", bar)
	}
	// 数量の右に、加工製品の在庫数（2026-10-23・stock_mirror.go）。位置で読むところ（上の at）が済んでから足す。
	insertStockColumn(el, stockLevels(ctx.DB, ctx.Viewer), productStockKey)
	appendMfgPageEnd(ctx, el, writable, open)
	return false, nil
}

// appendMfgPageEnd は「この製造の書類」と「閉じる」を置きます（鏡）。
//
// 書類は、種類ごとに、それを作る作業の見出しの節の末尾に置きます（2026-10-21 夜——mfgDocHeading）。
//
// > 2026-10-21 利用者:「部材発注書は納品作業ではなく、発注作業です」
//
// それまでは全部をページの末尾に置いていて、末尾は最後の見出し「納品作業」の節の中なので、部材発注書も納品作業の下に見えていました。
// 見出しの無いページ（作業の見出しを足す前の形）の書類と「閉じる」は、いままでどおりページの末尾です。
func appendMfgPageEnd(ctx *cms.MirrorContext, el *html.Node, writable, open bool) {
	root := rootOf(el)
	if root == el {
		return
	}
	var rest []mfgDoc
	byHeading := map[string][]mfgDoc{}
	for _, d := range mfgDocs(ctx.Viewer, ctx.PageID) {
		h := mfgDocHeading(d.Kind)
		if h == "" || !hasTopHeading(root, h) {
			rest = append(rest, d)
			continue
		}
		byHeading[h] = append(byHeading[h], d)
	}
	for _, h := range []string{MfgHeadStart, MfgHeadOrder, MfgHeadQuote, MfgHeadDelivery, MfgHeadCalc} {
		if docs := byHeading[h]; len(docs) > 0 {
			div := &html.Node{Type: html.ElementNode, Data: "div",
				Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome w-no-print mfg-docs-under"}, {Key: "contenteditable", Val: "false"}}}
			appendHTML(div, mfgDocsHTML(docs))
			insertAtSectionEnd(root, h, div)
		}
	}
	end := ""
	if len(rest) > 0 || len(byHeading) == 0 {
		end = mfgDocsHTML(rest)
	}
	switch {
	case !open:
		end += `<p class="mfg-closed">この製造は閉じています（記録へ移しました）</p>`
	case writable:
		end += `<p class="mfg-close-row"><button type="button" class="chip-btn mfg-close-go" data-mfg="` + stdhtml.EscapeString(page.FormatID(ctx.PageID)) +
			`">📁 この製造を閉じる（記録へ移す）</button> <span class="mfg-close-say"></span></p>`
	}
	div := &html.Node{Type: html.ElementNode, Data: "div",
		Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome w-no-print mfg-page-end"}, {Key: "contenteditable", Val: "false"}}}
	appendHTML(div, end)
	root.AppendChild(div)
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
	if !requireMfgPage(w, pageID) {
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	// 製造完了にしたら、空の製造数に数量を入れ、納める案件を超えて作った分を加工製品在庫表へ（2026-10-23・mfg_made_stock.go）。
	value := strings.TrimSpace(req.Value)
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "製造ページを読めません: "+err.Error())
		return
	}
	var vals map[string]string
	changed, cellErr, err := rebuildBody(pageID, user.Username, body, func(cur string) (string, error) {
		out, v, err := setMfgMade(cur, req.Row, req.ItemNo, req.Old, value)
		vals = v
		return out, err
	})
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return
	}
	if cellErr != nil {
		failCellEdit(w, cellErr)
		return
	}
	stocked, notes := 0, []string(nil)
	if changed {
		auth.Audit(user.Username, "mfg.made", pageID+" 行"+req.ItemNo+" 製造完了="+value)
		stocked, notes = stockOnMade(user, pageID, vals, value)
	}
	cms.WriteJSON(w, map[string]any{"success": true, "changed": changed, "stocked": stocked, "stock_notes": notes})
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
// sel が nil でなければ、選んだ行（製造明細の索引の行番号・0始まり）だけ（2026-10-21 夕・mfg_work.go）。
func mfgPartLines(user *auth.User, mfgID int, sel map[int]bool) (lines []ourOrderLine, skipped int, err error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, 0, err
	}
	items, err := UnorderedItems(user)
	if err != nil {
		return nil, 0, err
	}
	supplied := mfgSupplied(mfgID)
	for i, r := range rows {
		if sel != nil && !sel[i] {
			continue
		}
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		order, okO := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/"))
		qty, okQ := estimateNumber(r.Values["quantity"])
		if !ok || !okO || !okQ || qty <= 0 {
			skipped++
			continue
		}
		need := map[string]int{}
		vendor := map[string]string{}
		for _, it := range productNeeds(database.DB, pageNum(pid), int(qty)) {
			need[it.Key] += it.Required
			if vendor[it.Key] == "" {
				vendor[it.Key] = it.Vendor
			}
		}
		for _, u := range items {
			if u.TempRow > 0 || page.FormatID(u.OrderPageID) != order || page.FormatID(u.ProductPageID) != pid {
				continue
			}
			q := u.Remaining
			// この製造で作る数のぶんから、この製造のためにもう手当てした数を引く（2026-10-21 夕）——案件の数が製造の数より多いとき、
			// 「部材」を押し直すと残りが集まってしまっていた。
			if n, has := need[u.Key]; has {
				if n -= supplied[order+"|"+pid+"|"+u.Key]; n < q {
					q = n
				}
			}
			if q <= 0 {
				continue
			}
			v := u.Values
			// 仕入先の初めの値は、部材の表の推奨業者（2026-10-21・§6.3）。材料表には推奨業者の列が無いので、前に買った仕入先。
			vend := strings.TrimSpace(vendor[u.Key])
			if vend == "" {
				vend = strings.TrimSpace(u.Supplier)
			}
			lines = append(lines, ourOrderLine{
				ProductID: pid, ForOrder: order, Mfg: page.FormatID(mfgID), Kind: u.Kind, No: v["番号"], ItemID: v["品番"], ItemName: v["品名"], WorkPartNo: v["加工部品番号"], WorkPartName: v["加工部品名"], Work: v["加工内容"],
				Material: u.Material, Shape: u.Shape, Size: u.Size, Color: v["表面"], Spec: v["仕様"], Supplied: v["支給"],
				Quantity: strconv.Itoa(q), Cost: unorderedCostValue(u), Vendor: vend, Lot: fmtQty(qty), // ロットは製造明細の数量（見積もりに要る・2026-10-21 夕）
			})
		}
	}
	return lines, skipped, nil
}

// unorderedCostValue は部材発注書へ送る単価です。
//
// ⚠ **参考単価が無いときは空を送ります。** `0` と書くと、紙の上では
// **「0円で発注した」**という意味になります——「まだ分からない」と「ただ」は
// 別のことで、⚠ **仕入先は紙に書いてあるとおりに読みます**。
func unorderedCostValue(u UnorderedItem) string {
	if u.Cost <= 0 {
		return ""
	}
	return strconv.FormatFloat(u.Cost, 'f', -1, 64)
}

// mfgSupplied は、製造ページ mfg のために手当てした数を「案件|弊社品番|部材の鍵」ごとに数えます（2026-10-21 夕）。
// 手当ては、発注部材表・部材発注明細表・手配不要表の行のうち、列「製造」がこの製造を指す行（それまでの、製造ページの中の
// 発注部材表・手配不要表の「製造」の空いた行も）。数え方は手配済みの帳簿と同じ（rowSupplies——内訳ごと・備考「追加」は数えない）。
func mfgSupplied(mfg int) map[string]int {
	out := map[string]int{}
	for _, typ := range []string{ourOrderItemsType, OrderDraftType, SkipType} {
		rows, err := cms.VocabRowsOfType(database.DB, typ)
		if err != nil {
			continue
		}
		def, _ := cms.VocabDefByType(typ)
		for _, r := range rows {
			mine := refersTo(r.Values["mfg"], mfg) || (typ != ourOrderItemsType && r.PageID == mfg && strings.TrimSpace(r.Values["mfg"]) == "")
			if !mine {
				continue
			}
			key := orderRowKey(def, r)
			if key == "" {
				continue
			}
			for _, e := range rowSupplies(r) {
				out[e.Order+"|"+e.Product+"|"+key] += e.Qty
			}
		}
	}
	return out
}
