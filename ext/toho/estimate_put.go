package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積回答の単価を見積計算表へ写す（2026-10-05）
//
// 【要求】部材見積依頼 §4 の未決5「貰った単価の行き先」——利用者の答え（2026-10-05）: 「最新単価に見積も出す」と
// 「見積計算表へ写すボタン」の両方。こちらは後者——加工製品ページの「💴 見積回答」の行から、**人が選んだ**見積計算表の
// **人が選んだ行**（無ければ新しい行）の `価格` に単価を写し、`備考` に出所（見積 業者 日付）を書く。
//
//	GET  /api/estimate/rows?page_id=   … そのページの見積計算表と、写せる行（工程の行）
//	POST /api/estimate/put-cost        … {page_id, index, step, add, value, note}——index 枚目の表の工程 step の行に写す（add なら行を足す）
//
// ⚠ **どの行へ写すかは機械が決めない**——業者の単価は材料1枚・塗装1個あたりで、加工製品1個あたりに何枚・何個要るかは人が
// 見る（見積計算表の行は加工製品1個ぶんの原価）。写したあと人が直せる。
// ⚠ **出所を残す**（【要求】部材見積依頼 §3「混ぜて1つの数にせず、出所を必ず添える」）——備考に「見積 業者 日付」。前に写した
// 「見積 …」の書き込みは置き換える（値と出所が食い違わないように）。人が書いたほかの備考は残す。
// 2026-10-15 から見積計算表の列は `工程｜価格｜備考`（ロット・単価・総計の行は無い——ロットは組の見積表・estimate.go）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// estimateTableRows は見積計算表1枚の、写せる行の一覧です。
type estimateTableRows struct {
	Index int               `json:"index"`
	Lot   string            `json:"lot"` // 組の見積表のロット
	Rows  []estimateCostRow `json:"rows"`
	Why   string            `json:"why,omitempty"` // 写せない形の表の理由
}

type estimateCostRow struct {
	Step  string `json:"step"`
	Price string `json:"price"`
}

// estimateTablesOf は本文の見積計算表を読みます（列の違う表は Why を付けて行は空）。
func estimateTablesOf(body string) ([]estimateTableRows, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, errString("本文を読めません")
	}
	lotOf := map[*html.Node]string{}
	for _, b := range estimateBlocksOf(nodes) {
		if b.Calc != nil && b.Record != nil {
			lotOf[b.Calc] = estimateRecordOf(b.Record).Lot
		}
	}
	var out []estimateTableRows
	for i, t := range tablesOfType(nodes, estimateType) {
		tr := estimateTableRows{Index: i, Lot: lotOf[t], Rows: []estimateCostRow{}}
		rows := rowsOf(t)
		if len(rows) == 0 {
			tr.Why = "行がありません"
			out = append(out, tr)
			continue
		}
		col := headerIndex(rows[0])
		stepCol, okStep := col[estimateStepCol]
		priceCol, okPrice := col[estimatePriceCol]
		if !okStep || !okPrice {
			tr.Why = "この形の表（工程・価格の列が無い）には写せません"
			out = append(out, tr)
			continue
		}
		for _, r := range rows[1:] {
			cells := cellTexts(r)
			if stepCol >= len(cells) {
				continue
			}
			step := strings.TrimSpace(cells[stepCol])
			if step == "" {
				continue
			}
			price := ""
			if priceCol < len(cells) {
				price = strings.TrimSpace(cells[priceCol])
			}
			tr.Rows = append(tr.Rows, estimateCostRow{Step: step, Price: price})
		}
		out = append(out, tr)
	}
	return out, nil
}

// mergeQuoteNote は備考に出所を書きます——前に写した「見積 …」は置き換え、人が書いたほかの備考は残す。
func mergeQuoteNote(existing, note string) string {
	var keep []string
	for _, p := range strings.Split(existing, "／") {
		if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, "見積 ") {
			keep = append(keep, p)
		}
	}
	if note = strings.TrimSpace(note); note != "" {
		keep = append(keep, note)
	}
	return strings.Join(keep, "／")
}

// withEstimateCost は本文の index 枚目の見積計算表の工程 step の行（add なら新しい行を表の末尾に）に単価を写した本文を返します。
func withEstimateCost(body string, index int, step string, add bool, value, note string) (string, error) {
	step = strings.TrimSpace(step)
	value = cleanPrice(value)
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return "", errString("単価が数ではありません（" + value + "）")
	}
	if step == "" {
		return "", errString("写す行（工程）を選んでください")
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", errString("本文を読めません")
	}
	tables := tablesOfType(nodes, estimateType)
	if index < 0 || index >= len(tables) {
		return "", errString("その見積計算表が見つかりません（ページを開き直してください）")
	}
	rows := rowsOf(tables[index])
	if len(rows) < 1 {
		return "", errString("見積計算表に見出しがありません")
	}
	col := headerIndex(rows[0])
	stepCol, okStep := col[estimateStepCol]
	priceCol, okPrice := col[estimatePriceCol]
	if !okStep || !okPrice {
		return "", errString("この形の表（工程・価格の列が無い）には写せません")
	}
	noteCol, hasNote := col[estimateNoteCol]
	stepOf := func(tr *html.Node) string {
		if cells := cellsOf(tr); stepCol < len(cells) {
			return strings.TrimSpace(textOf(cells[stepCol]))
		}
		return ""
	}
	var target *html.Node
	for _, tr := range rows[1:] {
		if stepOf(tr) == step {
			target = tr
			break
		}
	}
	switch {
	case add && target != nil:
		return "", errString("「" + step + "」の行がもうあります（その行を選んでください）")
	case !add && target == nil:
		return "", errString("「" + step + "」の行が見つかりません（ページを開き直してください）")
	case add:
		target = &html.Node{Type: html.ElementNode, Data: "tr"}
		for i := 0; i < len(cellsOf(rows[0])); i++ {
			target.AppendChild(&html.Node{Type: html.ElementNode, Data: "td"})
		}
		setCellText(cellsOf(target)[stepCol], step)
		rows[len(rows)-1].Parent.AppendChild(target)
	}
	cells := cellsOf(target)
	if priceCol >= len(cells) {
		return "", errString("「" + step + "」の行に価格の欄がありません")
	}
	setCellText(cells[priceCol], value)
	if hasNote && noteCol < len(cells) {
		setCellText(cells[noteCol], mergeQuoteNote(textOf(cells[noteCol]), note))
	}
	return htmldoc.Render(nodes), nil
}

// EstimateRowsAPIHandler は GET /api/estimate/rows?page_id= です。
//
// page_id が加工製品ページで、その品物の見積計算書があれば、**いちばん新しい見積計算書**の見積計算表を返します（2026-10-17・
// estimate_calc_doc.go）。応答の `page_id` が写す先のページです（画面はそこへ put-cost する）。
func EstimateRowsAPIHandler(w http.ResponseWriter, r *http.Request) {
	pageID, pageInt, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	if pageKindOf(pageInt) == PageKindProduct {
		if src := estimateSourceOf(user, pageInt); src != pageInt {
			pageID = page.FormatID(src)
		}
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません")
		return
	}
	tables, err := estimateTablesOf(body)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(tables) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "このページに見積計算表がありません")
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "tables": tables, "page_id": pageID, "title": cms.PageTitleByID(pageNum(pageID))})
}

// EstimatePutCostAPIHandler は POST /api/estimate/put-cost です（入力: {page_id, index, step, add, value, note}）。
func EstimatePutCostAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Index  int    `json:"index"`
		Step   string `json:"step"`
		Add    bool   `json:"add"`
		Value  string `json:"value"`
		Note   string `json:"note"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	writeRFQRewrite(w, user, pageID, "estimate.put-cost", func(cur string) (string, int, error) {
		out, err := withEstimateCost(cur, req.Index, req.Step, req.Add, req.Value, req.Note)
		if err != nil {
			return cur, 0, err
		}
		return out, 1, nil
	})
}
