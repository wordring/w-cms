package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼の口（2026-10-03・段1——rfq.go の絵）
//
//	POST /api/rfq/collect        … 再見積依頼フォーム: 弊社品番＋ロットの部材を見積依頼必要部材表へ足す
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
		PageID  string `json:"page_id"`
		Product string `json:"product"`
		Lot     string `json:"lot"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pid, okP := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(req.Product), "/"))
	if !okP || !page.CanView(user, pageNum(pid)) {
		cms.JSONFail(w, http.StatusBadRequest, "弊社品番（加工製品ページの番号）を書いてください")
		return
	}
	productID := pageNum(pid)
	var lots []int
	if s := strings.TrimSpace(req.Lot); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			cms.JSONFail(w, http.StatusBadRequest, "ロットは1以上の数で書いてください（空なら見積計算表のロット）")
			return
		}
		lots = []int{n}
	} else if lots = estimateLotsOf(productID); len(lots) == 0 {
		lots = []int{1} // 見積計算表が無い・ロットが読めない——1個分
	}
	lines := rfqLinesOfProduct(productID, lots)
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusConflict, "/"+pid+" に材料・購入部品・外注加工の表がありません（集めるものがありません）")
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
	auth.Audit(user.Username, "rfq.collect", pageID+" <- "+pid+" ロット"+joinInts(lots)+" "+strconv.Itoa(added)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": added, "lots": lots, "product": pid})
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
