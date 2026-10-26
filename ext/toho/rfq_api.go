package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材見積依頼の口（2026-10-03・段1——rfq.go の絵）
//
//	POST /api/rfq/collect        … 再部材見積依頼フォーム: 弊社品番＋ロットの部材を見積依頼必要部材表へ足す（items で何枚でも）
//	GET  /api/rfq/folder-products … 装置フォルダの下の加工製品（再部材見積依頼の一時的な表・読むだけ）
//	POST /api/rfq/needs/move     … 見積依頼必要部材表（と臨時部材表）の選んだ行を見積依頼部材表へ移す
//	POST /api/rfq/needs/remove   … 選んだ行を消す（「不要」——記録は残さない・利用者:「人間が判断して必要なければ消します」）
//	POST /api/rfq/draft/back     … 見積依頼部材表の行を見積依頼必要部材表へ戻す（↩ 戻す）
//
// ⚠ **どれも本文を書き換える**ので、書ける人だけ・誰も編集していないときだけ（`gateWritablePage`・`rewriteBodyOrFail`）。
// 「誰も編集していない」は、2026-10-12 からは道具（`/api/lock`）が握っていないことだけを見ます（ブラウザは編集権を取らない）。
// 人の保存とは、`rewriteBodyOrFail` が通る `cms.RewriteBody` の書き込みの錠で順に並びます。
// ⚠ **行は行番号で指します**（画面は値を送らない——表示は丸めることがあり、送った値と本文が食い違う）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// estimateLotsOf はページ productID の見積表に書いたロットを、表の順に返します（同じ数は1つ・読めなければ空）。
// 呼び手は読む先（estimateSourceOf——いちばん新しい見積計算書、無ければ加工製品ページ）を渡します（2026-10-17〜）。
//
// 2026-10-15 から、ロットは見積表のロットの列（見積計算表の「ロット」の行は無くなった——estimate.go）。
func estimateLotsOf(productID int) []int {
	body, err := cms.ReadPageBody(page.FormatID(productID))
	if err != nil {
		return nil
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, t := range tablesOfType(nodes, estimateRecordType) {
		if v, ok := estimateNumber(estimateRecordOf(t).Lot); ok && v >= 1 && v == float64(int(v)) && !seen[int(v)] {
			seen[int(v)] = true
			out = append(out, int(v))
		}
	}
	return out
}
// rfqLinesOfProduct は加工製品の構成部品を、lots のそれぞれの数で行にします（ロット × 部材の数量）。
func rfqLinesOfProduct(productID int, lots []int) []ourOrderLine {
	var out []ourOrderLine
	for _, lot := range lots {
		for _, it := range productNeeds(database.DB, productID, lot) {
			ln := rfqLineFromValues(it.Values)
			ln.ProductID = page.FormatID(productID)
			ln.Quantity = strconv.Itoa(it.Required)
			ln.Lot = strconv.Itoa(lot) // 重複は「同じもの・同じロット」で見る（rfqDupKey）
			out = append(out, ln)
		}
	}
	return out
}

// appendRFQNeeds は本文の見積依頼必要部材表（1枚目）へ行を足します。**表が無ければ作ります**（節で包む・本文の末尾）。
func appendRFQNeeds(body string, lines []ourOrderLine) (string, int) {
	if out, n, ok := appendRFQLines(body, RFQNeedsType, 1, lines); ok {
		return out, n
	}
	return body + tableOfLinesHTML(RFQNeedsType, lines, true), len(lines)
}

// RFQCollectAPIHandler は POST /api/rfq/collect です（入力: {page_id, product, lot}）。
func RFQCollectAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID  string           `json:"page_id"`
		Product string           `json:"product"`
		Lot     string           `json:"lot"`
		Items   []rfqCollectItem `json:"items"` // 装置フォルダの一時的な表で選んだ加工製品（あれば product・lot より先）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	// 1つの加工製品（弊社品番の欄）なら、断る理由はそのまま断る。何枚か（装置フォルダの一時的な表で選んだもの）なら、
	// 集められないものは飛ばして理由を返し、集められたものだけ入れる（1枚が空でもほかの分は入れたい）。
	single := len(req.Items) == 0
	items := req.Items
	if single {
		items = []rfqCollectItem{{Product: req.Product, Lot: req.Lot}}
	}
	var lines []ourOrderLine
	var done []map[string]any
	var skipped []string
	var log []string
	for _, it := range items {
		pid, lots, ls, status, msg := rfqItemLines(user, it)
		if msg != "" {
			if single {
				cms.JSONFail(w, status, msg)
				return
			}
			skipped = append(skipped, msg)
			continue
		}
		lines = append(lines, ls...)
		done = append(done, map[string]any{"product": pid, "lots": lots, "rows": len(ls)})
		log = append(log, pid+" 製品個数"+joinInts(lots))
	}
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusConflict, "集められるものがありません——"+strings.Join(skipped, "／"))
		return
	}
	// 送り先は要る（2026-10-19 から。それまでは、空なら部材見積依頼の置き場——案件残表から送るときの道。置き場ごとなくした）。
	target := strings.TrimSpace(req.PageID)
	if target == "" {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼必要部材表を置くページがありません（page_id）")
		return
	}
	pageID, okID := gateWritablePage(w, r, target)
	if !okID {
		return
	}
	added := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		var out string
		out, added = appendRFQNeeds(cur, lines)
		return out
	}) {
		return
	}
	auth.Audit(user.Username, "rfq.collect", pageID+" <- "+strings.Join(log, "・")+" "+strconv.Itoa(added)+"行")
	res := map[string]any{"success": true, "rows": added, "products": done, "skipped": skipped, "page_id": pageID}
	if single {
		res["lots"] = done[0]["lots"]
		res["product"] = done[0]["product"]
	}
	cms.WriteJSON(w, res)
}

// rfqCollectItem は集める加工製品1つです（弊社品番＝加工製品ページの番号・ロット——空なら見積表のロット・2026-10-15〜）。
//
// ForOrder はどの受注のための部材か（2026-10-04・段3——受注フォルダの受注残表から送るとき、受注ページの番号）。行の「受注」の
// 列に入る（【要求】部材見積依頼 §2「見積依頼明細の『受注』の列は発注明細と同じく残し、受注残表から集めた部材に入る」）。
// ロットにはその受注残（数量 − 出荷済数量）を入れて送る——手配済みは引かない（同 §2）。
// ⚠ 2026-10-19 に、案件残表（受注残表の改名）から送る欄を外したので、いまの画面は for_order を送りません（受け口だけ残る）。
// 「受注」の列も「案件」に改名済み——製造ページから集めるときは mfg_more.go が行の「案件」を写します。
type rfqCollectItem struct {
	Product  string `json:"product"`
	Lot      string `json:"lot"`
	ForOrder string `json:"for_order"`
}

// rfqItemLines は加工製品1つの部材を行にします。集められなければ断る理由（msg）と、そのときの状態の番号を返します。
func rfqItemLines(user *auth.User, it rfqCollectItem) (pid string, lots []int, lines []ourOrderLine, status int, msg string) {
	pid, okP := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(it.Product), "/"))
	if !okP || !page.CanView(user, pageNum(pid)) {
		return "", nil, nil, http.StatusBadRequest, "弊社品番（加工製品ページの番号）を書いてください"
	}
	productID := pageNum(pid)
	if s := strings.TrimSpace(it.Lot); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return pid, nil, nil, http.StatusBadRequest, "/" + pid + " の製品個数は1以上の数で書いてください（空なら見積表の個数）"
		}
		lots = []int{n}
	} else if lots = estimateLotsOf(estimateSourceOf(user, productID)); len(lots) == 0 { // いちばん新しい見積計算書のロット（無ければ加工製品ページ・2026-10-17）
		lots = []int{1} // 見積表が無い・ロットが読めない——1個分
	}
	lines = rfqLinesOfProduct(productID, lots)
	if len(lines) == 0 {
		return pid, lots, nil, http.StatusConflict, "/" + pid + " に材料表・購入部品表・外注加工表がありません（集めるものがありません）"
	}
	if s := strings.TrimSpace(it.ForOrder); s != "" {
		order, ok := page.NormalizeID(strings.TrimPrefix(s, "/"))
		if !ok {
			return pid, lots, nil, http.StatusBadRequest, "案件ページの番号が読めません: " + s
		}
		for i := range lines {
			lines[i].ForOrder = order
		}
	}
	return pid, lots, lines, 0, ""
}

// RFQFolderProductsAPIHandler は GET /api/rfq/folder-products?folder=001234 です（2026-10-03）。
//
// 利用者:「装置フォルダのページ番号を入力する欄を作り、その下にある加工製品を列挙する一時的な表を作ります。その表で
// チェックした加工製品から見積依頼必要部材表に追加するように」。**読むだけ**——表は画面の中だけで組み（本文には残さない）、
// 選んだものは `/api/rfq/collect` の items で送る。並びと「加工製品ページかどうか」は「加工製品の一覧」と同じ
// （`productListRows`——改定で子ページへ移った旧版は出さない）。どのページの番号でもよい（その下を全部見る）。
func RFQFolderProductsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, fid, rows, ok := folderProductsOrFail(w, r)
	if !ok {
		return
	}
	type product struct {
		folderProduct
		Lots []int `json:"lots"`
	}
	out := make([]product, 0, len(rows))
	for _, p := range rows {
		lots := estimateLotsOf(estimateSourceOf(user, p.PageID)) // いちばん新しい見積計算書のロット（無ければ加工製品ページ・2026-10-17）
		if lots == nil {
			lots = []int{}
		}
		out = append(out, product{folderProduct: folderProductOf(p), Lots: lots})
	}
	cms.WriteJSON(w, map[string]any{"success": true, "folder": fid, "title": cms.PageTitleByID(pageNum(fid)), "products": out})
}

// rfqPickRequest は見積依頼必要部材表の選んだ行です（行番号は見出しを除いた何行目か・1始まり）。
type rfqPickRequest struct {
	PageID   string `json:"page_id"`
	Rows     []int    `json:"rows"`
	TempRows []int    `json:"temp_rows"`
	RowSigs  []string `json:"row_sigs"`  // 行の指紋（rows と同じ並び・rowSig・2026-10-22）
	TempSigs []string `json:"temp_sigs"` // 臨時部材表の行の指紋（temp_rows と同じ並び）
	Into     string   `json:"into"`
}

// resolveRowsIn は、本文の n 枚目の vocabType の表について、行の番号と指紋をいまの行の番号へ引き直します（resolveRowsBySig）。
func resolveRowsIn(body, vocabType string, n int, rows []int, sigs []string) ([]int, bool) {
	if len(rows) == 0 {
		return rows, true
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, false
	}
	tables := tablesOfType(nodes, vocabType)
	if n < 1 || n > len(tables) {
		return nil, false
	}
	return resolveRowsBySig(tables[n-1], rows, sigs)
}

// takeRFQPicked は本文から、選んだ必要部材表の行と臨時部材表の行を抜いて、行の中身を返します（必要部材表の順→臨時の順）。
// 行は、画面が描いたときの指紋で引き直します（2026-10-22 の見直し——番号だけだと、ほかの人が行を足す・消すと、選んでいない
// 行を運んだり消したりしていた）。合わなければ ok=false（何もしない）。
func takeRFQPicked(cur string, rows, tempRows []int, rowSigs, tempSigs []string) (string, []ourOrderLine, bool) {
	rows, okR := resolveRowsIn(cur, RFQNeedsType, 1, rows, rowSigs)
	tempRows, okT := resolveRowsIn(cur, RFQTempPartsType, 1, tempRows, tempSigs)
	if !okR || !okT {
		return cur, nil, false
	}
	var out []ourOrderLine
	if len(rows) > 0 {
		next, lines, ok := takeTableRows(cur, RFQNeedsType, 1, sortedInts(rows), false)
		if ok {
			cur = next
			out = append(out, lines...)
		}
	}
	if len(tempRows) > 0 {
		next, lines, ok := takeTableRows(cur, RFQTempPartsType, 1, sortedInts(tempRows), false)
		if ok {
			cur = next
			out = append(out, lines...)
		}
	}
	return cur, out, true
}

// RFQNeedsMoveAPIHandler は POST /api/rfq/needs/move です——選んだ行を見積依頼部材表へ移します。
//
// ⚠ **何も選ばなくても空の見積依頼部材表を作れます**（発注部材表と同じ——人が手で書き足す道）。
func RFQNeedsMoveAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req rfqPickRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	into := strings.TrimSpace(req.Into)
	moved, found, same := 0, true, true
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		next, lines, ok := takeRFQPicked(cur, req.Rows, req.TempRows, req.RowSigs, req.TempSigs)
		if !ok {
			same = false
			return cur
		}
		for i := range lines {
			lines[i].Cost = "" // 単価は聞くもの——見積依頼部材表には書かない
		}
		if into == "" {
			moved = len(lines)
			return placeNewTableAfterLast(next, RFQDraftType, tableOfLinesHTML(RFQDraftType, lines, true))
		}
		n, err := strconv.Atoi(into)
		if err != nil {
			found = false
			return cur
		}
		out, k, ok := appendRFQLines(next, RFQDraftType, n, lines)
		if !ok {
			found = false
			return cur
		}
		moved = k
		return out
	}) {
		return
	}
	if !same {
		cms.JSONFail(w, http.StatusConflict, errTableMoved.Error())
		return
	}
	if !found {
		cms.JSONFail(w, http.StatusConflict, "入れる先の見積依頼部材表が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "rfq.draft", pageID+" "+strconv.Itoa(moved)+"行 into="+into)
	cms.WriteJSON(w, map[string]any{"success": true, "rows": moved})
}

// RFQNeedsRemoveAPIHandler は POST /api/rfq/needs/remove です——選んだ行を消します（「不要」・記録は残さない）。
func RFQNeedsRemoveAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req rfqPickRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Rows)+len(req.TempRows) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "消す行を選んでください")
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	removed, same := 0, true
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		next, lines, ok := takeRFQPicked(cur, req.Rows, req.TempRows, req.RowSigs, req.TempSigs)
		if !ok {
			same = false
			return cur
		}
		removed = len(lines)
		return next
	}) {
		return
	}
	if !same {
		cms.JSONFail(w, http.StatusConflict, errTableMoved.Error())
		return
	}
	auth.Audit(user.Username, "rfq.needs.remove", pageID+" "+strconv.Itoa(removed)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": removed})
}

// RFQDraftBackAPIHandler は POST /api/rfq/draft/back です——見積依頼部材表の1行を見積依頼必要部材表へ戻します
// （入力: {page_id, table, row}——何枚目の見積依頼部材表の、見出しを除いた何行目か）。最後の1行なら表ごと消えます。
func RFQDraftBackAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Table  int    `json:"table"`
		Row    int    `json:"row"`
		Sig    string `json:"sig"` // 行の指紋（rowSig・2026-10-22）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	done := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		// 行は指紋で引き直す（その表に無ければ、ほかの見積依頼部材表から——前の表が消えて番号がずれたとき）。
		table, row, okRow := findDraftRow(cur, req.Table, req.Row, req.Sig)
		if !okRow {
			return cur
		}
		next, lines, ok := takeTableRows(cur, RFQDraftType, table, []int{row}, true)
		if !ok || len(lines) == 0 {
			return cur
		}
		done = true
		// 製造ページでは外すだけ（2026-10-21 夕——部材は必要部材表に残っている・mfg_work.go）。ほかでは見積依頼必要部材表へ戻す。
		if pageKindOf(pageNum(pageID)) == PageKindMfg {
			return next
		}
		out, _ := appendRFQNeeds(next, lines)
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict, "その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "rfq.draft.back", pageID+" 表"+strconv.Itoa(req.Table)+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true})
}

// sortedInts は重複を除いて小さい順に並べます。
func sortedInts(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// joinInts は数を「・」で繋ぎます（記録の文）。
func joinInts(in []int) string {
	s := make([]string, len(in))
	for i, v := range in {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, "・")
}

// findDraftRow は、見積依頼部材表の table 枚目の row 行目（指紋 sig）を、いまの本文で引き直します。その表で合わなければ、ほかの
// 見積依頼部材表で指紋の合う行がちょうど1つならそれ（前の表が消えて、表の番号がずれたとき）。指紋が空なら番号のまま。
func findDraftRow(body string, table, row int, sig string) (int, int, bool) {
	if rows, ok := resolveRowsIn(body, RFQDraftType, table, []int{row}, []string{sig}); ok {
		return table, rows[0], true
	}
	if sig == "" {
		return 0, 0, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return 0, 0, false
	}
	foundT, foundR := 0, 0
	for i, t := range tablesOfType(nodes, RFQDraftType) {
		for j, tr := range rowsOf(t) {
			if j == 0 || (tr.Parent != nil && tr.Parent.Data == "tfoot") || rowSig(tr) != sig {
				continue
			}
			if foundT != 0 {
				return 0, 0, false // 2つ以上——決められない
			}
			foundT, foundR = i+1, j
		}
	}
	return foundT, foundR, foundT != 0
}

// resolveRowsIfSigned は resolveRowsIn と同じで、指紋が1つも無ければ番号のまま返します（指紋を送らない古い画面・道具——
// これまでどおり。画面は 2026-10-26 から指紋を送る）。
func resolveRowsIfSigned(body, vocabType string, n int, rows []int, sigs []string) ([]int, bool) {
	for _, s := range sigs {
		if strings.TrimSpace(s) != "" {
			return resolveRowsIn(body, vocabType, n, rows, sigs)
		}
	}
	return rows, true
}
