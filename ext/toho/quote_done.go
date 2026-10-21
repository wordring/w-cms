package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積の依頼の案件は、見積を出して完了（2026-10-19・【考察】製造ページ §8.4・作る順の4番の続き）
//
//	2026-10-19 利用者:「注文が来たとき、見積の依頼の案件は直接つなぎません。見積依頼の案件は見積提出で完了です。
//	その品が注文された場合、DBが検索され販売価格が表示されます。そこで見積文書とリンクされるのは良いと思います。」
//
//   - 製造ページの「📄 この製造の見積書を作る」で、製造明細の見積の依頼の行から見積書を作る（`/api/mfg/estimate`（2026-10-21 から置き場は記録・明細の「製造」の列でつながる））。
//     見積明細表の行には「案件」（どの案件への見積か）を写す。単価は、その加工製品のいちばん新しい見積計算書（無ければ加工製品ページ）の
//     見積表から——ロットが数量と同じ表、無ければ数量以下でいちばん大きいロット、無ければいちばん小さいロット。
//   - 見積書に「送付日」か「送付不要」が付くと、その案件の行は「見積を出した」——案件残表から外れ、案件ページの足元にそう出る。
//   - 注文の案件の案件明細表には、単価の欄に、前に出した見積の単価と見積書へのリンクを出す（鏡・見積書は直接つながない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// quotedLookup は描画1回ぶんの「案件ページ×品番 → その行を載せて出した見積書（いちばん新しいページ番号）」です。
type quotedLookup map[string]int

// estimateSent は見積書ページのタグに「送付日」か「送付不要」があるかです（未送付の見積書の一覧と同じ線）。
func estimateSent(tags map[string][]string) bool {
	return strings.TrimSpace(cms.FirstTag(tags, EstimateSentTag)) != "" || strings.TrimSpace(cms.FirstTag(tags, EstimateNoSendTag)) != ""
}

// newQuotedLookup は、送付日か送付不要の付いた見積書の見積明細表で、「案件」のある行を全部引きます。読めなければ空。
func newQuotedLookup(db cms.ReadOnlyDB) quotedLookup {
	out := quotedLookup{}
	rows, err := cms.VocabRowsOfType(db, EstimateItemsType)
	if err != nil {
		return out
	}
	sent := map[int]bool{}
	for _, r := range rows {
		order := strings.TrimSpace(r.Values["order"])
		if order == "" {
			continue
		}
		ok, seen := sent[r.PageID]
		if !seen {
			if !cms.IsTemplateArea(page.FormatID(r.PageID)) {
				tags, _ := cms.TagsOfPage(db, r.PageID)
				ok = estimateSent(tags)
			}
			sent[r.PageID] = ok
		}
		if !ok {
			continue
		}
		k := shippedKey(order, r.Values["item-id"], r.Values["item-name"])
		if r.PageID > out[k] {
			out[k] = r.PageID
		}
	}
	return out
}

// of は案件ページ orderPage の品番 itemNo（空なら品名 itemName）の行を載せて出した見積書のページ番号です（無ければ 0）。
func (q quotedLookup) of(orderPage, itemNo, itemName string) int {
	return q[shippedKey(orderPage, itemNo, itemName)]
}

// quoteCaseNote は見積の依頼の案件ページの足元の文です——全部の行が出した見積書に載っていれば「完了」。
func quoteCaseNote(ctx *cms.MirrorContext, el *html.Node) string {
	const base = "見積の依頼の案件です（まだ注文ではないので、受注残高・赤字の通知には入りません）"
	rows := rowsOf(el)
	if len(rows) < 2 {
		return base
	}
	head := rows[0]
	ii, ni, qi := headerIndexOf(head, ItemNoTag), headerIndexOf(head, "品名"), headerIndexOf(head, "数量")
	if qi < 0 {
		return base
	}
	quoted := newQuotedLookup(ctx.DB)
	order := page.FormatID(ctx.PageID)
	var pages []int
	all, some := true, false
	for _, tr := range rows[1:] {
		if _, ok := estimateNumber(cellText(tr, qi)); !ok {
			continue
		}
		item, name := "", ""
		if ii >= 0 {
			item = cellText(tr, ii)
		}
		if ni >= 0 {
			name = cellText(tr, ni)
		}
		id := quoted.of(order, item, name)
		if id == 0 {
			all = false
			continue
		}
		some = true
		if !containsID(pages, id) {
			pages = append(pages, id)
		}
	}
	if !some {
		return base + "。見積書を出すと（送付日か送付不要を付けると）完了です"
	}
	sort.Ints(pages)
	var ids []string
	for _, id := range pages {
		ids = append(ids, "/"+page.FormatID(id))
	}
	if all {
		return "見積の依頼の案件です。見積書 " + strings.Join(ids, "・") + " を出したので完了です"
	}
	return base + "。見積書 " + strings.Join(ids, "・") + " を出した行があります（まだの行は案件残表に残ります）"
}

// latestEstimateFor は、加工製品 productID の見積明細表の行のうち、いちばん新しい見積（見積日、同じなら新しいページ）を返します。
func latestEstimateFor(db cms.ReadOnlyDB, canView func(int) bool, productID int) (orderPriceRow, bool) {
	list := estimatePricesForProduct(db, canView, productID)
	best, found := orderPriceRow{}, false
	for _, q := range list {
		if strings.TrimSpace(q.Price) == "" {
			continue
		}
		if !found || q.Date > best.Date || (q.Date == best.Date && q.OrderPage > best.OrderPage) {
			best, found = q, true
		}
	}
	return best, found
}

// showQuoteInOrderTable は注文の案件の案件明細表の「単価」の欄に、前に出した見積の単価と見積書へのリンクをクロームで出します（鏡）。
// 2026-10-19 利用者:「その品が注文された場合、DBが検索され販売価格が表示されます。そこで見積文書とリンクされるのは良いと思います。」
func showQuoteInOrderTable(ctx *cms.MirrorContext, el *html.Node) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	head := rows[0]
	oi, pi := headerIndexOf(head, "弊社品番"), headerIndexOf(head, "単価")
	if oi < 0 || pi < 0 {
		return
	}
	canView := func(id int) bool { return true }
	if ctx.Viewer != nil {
		canView = viewCheck(ctx.Viewer)
	}
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if pi >= len(cells) {
			continue
		}
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(cellText(tr, oi)), "/"))
		if !ok {
			continue
		}
		q, found := latestEstimateFor(ctx.DB, canView, pageNum(pid))
		if !found {
			continue
		}
		price := q.Price
		if v, okP := parseMoney(price); okP {
			price = yen(v)
		}
		label := "見積 " + price
		if q.Date != "" {
			label += "（" + q.Date
			if q.Qty != "" {
				label += "・" + q.Qty + q.Unit
			}
			label += "）"
		}
		appendRawChrome(cells[pi], `<span class="vocab-chrome quote-mirror" contenteditable="false" title="前に出した見積の単価（見積書の見積明細表から）">`+
			`<a href="/`+page.FormatID(q.OrderPage)+`">`+stdhtml.EscapeString(label)+`</a></span>`)
	}
}

// pickEstimateOption は見積表の中から、数量 qty に使う1枚を選びます——ロットが数量と同じ表、無ければ数量以下で
// いちばん大きいロット、無ければいちばん小さいロット（ロットの無い表は最後の手）。単価の無い表は選びません。
func pickEstimateOption(opts []estimateOption, qty float64) (estimateLine, bool) {
	var exact, below, above, nolot *estimateLine
	var belowLot, aboveLot float64
	for i := range opts {
		if opts[i].Why != "" {
			continue
		}
		ln := &opts[i].Line
		lot, ok := estimateNumber(ln.Quantity)
		switch {
		case !ok:
			if nolot == nil {
				nolot = ln
			}
		case lot == qty:
			if exact == nil {
				exact = ln
			}
		case lot < qty:
			if below == nil || lot > belowLot {
				below, belowLot = ln, lot
			}
		default:
			if above == nil || lot < aboveLot {
				above, aboveLot = ln, lot
			}
		}
	}
	for _, c := range []*estimateLine{exact, below, above, nolot} {
		if c != nil {
			return *c, true
		}
	}
	return estimateLine{}, false
}

// mfgEstimateLines は製造ページ mfgID の製造明細のうち、見積の依頼の案件の行から見積明細表の行を組みます。
// 単価が見つからない行も入れて（単価は空・備考に断り）、missing に数えます。
func mfgEstimateLines(user *auth.User, mfgID int) (lines []estimateLine, missing int, err error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, 0, err
	}
	kinds := map[string]string{}
	for _, r := range rows {
		order, okO := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/"))
		qty, okQ := estimateNumber(r.Values["quantity"])
		if !okO || !okQ || qty <= 0 {
			continue
		}
		k, seen := kinds[order]
		if !seen {
			k = pageKindOf(pageNum(order))
			kinds[order] = k
		}
		if k != PageKindCustomerQuote {
			continue
		}
		ln := estimateLine{Order: order, ItemID: strings.TrimSpace(r.Values["item-id"]), ItemName: strings.TrimSpace(r.Values["item-name"]),
			Quantity: fmtQty(qty), Unit: strings.TrimSpace(r.Values["unit"])}
		pid, okP := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		if !okP || !page.CanView(user, pageNum(pid)) {
			ln.Note = "⚠ 弊社品番がありません——単価を書いてください"
			missing++
			lines = append(lines, ln)
			continue
		}
		ln.ProductID = pid
		opts, _ := estimateOptionsOf(estimateSourceOf(user, pageNum(pid)))
		got, found := pickEstimateOption(opts, qty)
		if !found {
			ln.Note = "⚠ 見積表に単価がありません——単価を書いてください"
			missing++
		} else {
			ln.Price, ln.Note = got.Price, got.Note
			if ln.Unit == "" {
				ln.Unit = got.Unit
			}
			if ln.ItemID == "" && ln.ItemName == "" {
				ln.ItemID, ln.ItemName = got.ItemID, got.ItemName
			}
			if lot, ok := estimateNumber(got.Quantity); ok && lot != qty {
				ln.Note = strings.TrimSpace("ロット " + got.Quantity + " の単価　" + ln.Note)
			}
		}
		lines = append(lines, ln)
	}
	return lines, missing, nil
}

// mfgHasQuoteRows は製造ページの製造明細に、見積の依頼の案件の行があるかです（「見積書を作る」を出すか）。
func mfgHasQuoteRows(el *html.Node) bool {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return false
	}
	oi := headerIndexOf(rows[0], "案件")
	if oi < 0 {
		return false
	}
	seen := map[string]bool{}
	for _, tr := range rows[1:] {
		id, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(cellText(tr, oi)), "/"))
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if pageKindOf(pageNum(id)) == PageKindCustomerQuote {
			return true
		}
	}
	return false
}

// MfgEstimateAPIHandler は POST /api/mfg/estimate です。入力: {page_id, signer}（製造ページ）。
// 製造明細の見積の依頼の行から見積書を作ります（置き場は記録・明細の「製造」の列にこの製造ページ。2026-10-20 までは製造ページの子。見積先は製造ページの客先・宛名は御中・差出人は選んだ人か既定の人）。
func MfgEstimateAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Signer string `json:"signer"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if !requireMfgPage(w, pageID) {
		return
	}
	lines, missing, err := mfgEstimateLines(user, pageNum(pageID))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "製造明細を読めません: "+err.Error())
		return
	}
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "この製造に見積の依頼の行がありません")
		return
	}
	signer := strings.TrimSpace(req.Signer)
	if signer == "" {
		if def, has := DefaultSigner(user, Signers(user)); has {
			signer = page.FormatID(def.PageID)
		}
	}
	client := cms.PageTagValue(database.DB, pageNum(pageID), DeliveryClientTag)
	newID, client, ok := newEstimatePage(w, user, client, "", signer, lines, pageID)
	if !ok {
		return
	}
	auth.Audit(user.Username, "estimate.new", newID+" ← 製造 "+pageID+" "+client)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "rows": len(lines), "missing": missing,
		"message": mfgEstimateMessage(len(lines), missing)})
}

func mfgEstimateMessage(rows, missing int) string {
	msg := "見積書を作りました（" + strconv.Itoa(rows) + " 行）"
	if missing > 0 {
		msg += "。単価の無い行が " + strconv.Itoa(missing) + " 行あります——見積書で書いてください"
	}
	return msg
}
