package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼の口（2026-10-03・段1——rfq.go の絵）
//
//	POST /api/rfq/collect        … 再見積依頼フォーム: 弊社品番＋ロットの部材を見積依頼必要部材表へ足す（items で何枚でも）
//	GET  /api/rfq/folder-products … 装置フォルダの下の加工製品（再見積依頼の一時的な表・読むだけ）
//	POST /api/rfq/needs/move     … 見積依頼必要部材表（と臨時部材表）の選んだ行を見積依頼部材表へ移す
//	POST /api/rfq/needs/remove   … 選んだ行を消す（「不要」——記録は残さない・利用者:「人間が判断して必要なければ消します」）
//	POST /api/rfq/draft/back     … 見積依頼部材表の行を見積依頼必要部材表へ戻す（↩ 戻す）
//
// ⚠ **どれも本文を書き換える**ので、書ける人だけ・誰も編集していないときだけ（`gateWritablePage`・`rewriteBodyOrFail`）。
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

// estimateLotsOf は加工製品ページの見積計算表に書いたロットを、表の順に返します（同じ数は1つ・読めなければ空）。
//
// ⚠ 読めるのは縦の形（「工程」の列に「ロット」の行）だけ——列にロットを並べた形は読めない（【要求】見積 §4）。
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
	for _, t := range tablesOfType(nodes, estimateType) {
		rows := rowsOf(t)
		if len(rows) < 2 {
			continue
		}
		col := headerIndex(rows[0])
		step, okStep := col["工程"]
		num, okNum := col["数"]
		if !okStep || !okNum {
			continue
		}
		for _, tr := range rows[1:] {
			cells := cellTexts(tr)
			if step >= len(cells) || num >= len(cells) || strings.TrimSpace(cells[step]) != "ロット" {
				continue
			}
			if v, ok := estimateNumber(cells[num]); ok && v >= 1 && v == float64(int(v)) && !seen[int(v)] {
				seen[int(v)] = true
				out = append(out, int(v))
			}
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
			out = append(out, ln)
		}
	}
	return out
}

// appendRFQNeeds は本文の見積依頼必要部材表（1枚目）へ行を足します。**表が無ければ作ります**（節で包む・本文の末尾）。
func appendRFQNeeds(body string, lines []ourOrderLine) (string, int) {
	if out, n, ok := appendLinesToTable(body, RFQNeedsType, 1, lines); ok {
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
		log = append(log, pid+" ロット"+joinInts(lots))
	}
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusConflict, "集められるものがありません——"+strings.Join(skipped, "／"))
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
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
	res := map[string]any{"success": true, "rows": added, "products": done, "skipped": skipped}
	if single {
		res["lots"] = done[0]["lots"]
		res["product"] = done[0]["product"]
	}
	cms.WriteJSON(w, res)
}

// rfqCollectItem は集める加工製品1つです（弊社品番＝加工製品ページの番号・ロット——空なら見積計算表のロット）。
type rfqCollectItem struct {
	Product string `json:"product"`
	Lot     string `json:"lot"`
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
			return pid, nil, nil, http.StatusBadRequest, "/" + pid + " のロットは1以上の数で書いてください（空なら見積計算表のロット）"
		}
		lots = []int{n}
	} else if lots = estimateLotsOf(productID); len(lots) == 0 {
		lots = []int{1} // 見積計算表が無い・ロットが読めない——1個分
	}
	lines = rfqLinesOfProduct(productID, lots)
	if len(lines) == 0 {
		return pid, lots, nil, http.StatusConflict, "/" + pid + " に材料・購入部品・外注加工の表がありません（集めるものがありません）"
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
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	fid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("folder")), "/"))
	exists := 0
	if ok {
		// ⚠ 管理者には CanView が無いページでも通るので、在るかを別に見る（無い番号に「0 件」と答えない）。
		database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = ?`, pageNum(fid)).Scan(&exists)
	}
	if !ok || exists == 0 || !page.CanView(user, pageNum(fid)) {
		cms.JSONFail(w, http.StatusNotFound, "その番号のページがありません（装置フォルダのページ番号を書いてください）")
		return
	}
	rows, err := productListRows(user, pageNum(fid))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "加工製品を読めませんでした: "+err.Error())
		return
	}
	type product struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Machine   string `json:"machine"`
		PartNo    string `json:"part_no"`
		DrawingNo string `json:"drawing_no"`
		Lots      []int  `json:"lots"`
		Migrating bool   `json:"migrating"`
	}
	out := make([]product, 0, len(rows))
	for _, p := range rows {
		lots := estimateLotsOf(p.PageID)
		if lots == nil {
			lots = []int{}
		}
		out = append(out, product{ID: page.FormatID(p.PageID), Title: p.Title, Machine: p.Machine,
			PartNo: p.PartNo, DrawingNo: p.DrawingNo, Lots: lots, Migrating: p.Migrating})
	}
	cms.WriteJSON(w, map[string]any{"success": true, "folder": fid, "title": cms.PageTitleByID(pageNum(fid)), "products": out})
}

// rfqPickRequest は見積依頼必要部材表の選んだ行です（行番号は見出しを除いた何行目か・1始まり）。
type rfqPickRequest struct {
	PageID   string `json:"page_id"`
	Rows     []int  `json:"rows"`
	TempRows []int  `json:"temp_rows"`
	Into     string `json:"into"`
}

// takeRFQPicked は本文から、選んだ必要部材表の行と臨時部材表の行を抜いて、行の中身を返します（必要部材表の順→臨時の順）。
func takeRFQPicked(cur string, rows, tempRows []int) (string, []ourOrderLine) {
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
	return cur, out
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
	moved, found := 0, true
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		next, lines := takeRFQPicked(cur, req.Rows, req.TempRows)
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
		out, k, ok := appendLinesToTable(next, RFQDraftType, n, lines)
		if !ok {
			found = false
			return cur
		}
		moved = k
		return out
	}) {
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
	removed := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		next, lines := takeRFQPicked(cur, req.Rows, req.TempRows)
		removed = len(lines)
		return next
	}) {
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
		next, lines, ok := takeTableRows(cur, RFQDraftType, req.Table, []int{req.Row}, true)
		if !ok || len(lines) == 0 {
			return cur
		}
		done = true
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
