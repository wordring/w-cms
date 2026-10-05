package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積回答の単価を見積計算表へ写す（2026-10-05）
//
// 【要求】見積依頼 §4 の未決5「貰った単価の行き先」——利用者の答え（2026-10-05）: 「最新単価に見積も出す」と
// 「見積計算表へ写すボタン」の両方。こちらは後者——加工製品ページの「💴 見積回答」の行から、**人が選んだ**見積計算表の
// **人が選んだ行**（無ければ新しい行）の `数` に単価を写し、`備考` に出所（見積 業者 日付）を書く。
//
//	GET  /api/estimate/rows?page_id=   … そのページの見積計算表と、写せる行（工程の行——単価・総計・弊社利益・確定単価・ロットは除く）
//	POST /api/estimate/put-cost        … {page_id, index, step, add, value, note}——index 枚目の表の工程 step の行に写す（add なら行を足す）
//
// ⚠ **どの行へ写すかは機械が決めない**——業者の単価は材料1枚・塗装1個あたりで、加工製品1個あたりに何枚・何個要るかは人が
// 見る（見積計算表の行は加工製品1個ぶんの原価）。写したあと人が直せる。
// ⚠ **出所を残す**（【要求】見積依頼 §3「混ぜて1つの数にせず、出所を必ず添える」）——備考に「見積 業者 日付」。前に写した
// 「見積 …」の書き込みは置き換える（値と出所が食い違わないように）。人が書いたほかの備考は残す。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// estimateLotRow は見積計算表のロットの行の名前です。
const estimateLotRow = "ロット"

// estimateFixedRows は写す先にしない行（計算の結果・条件の行）です。
var estimateFixedRows = map[string]bool{
	estimateUnitPriceRow: true, estimateTotalRow: true, estimateProfitRow: true, estimateFinalRow: true, estimateLotRow: true,
}

// estimateTableRows は見積計算表1枚の、写せる行の一覧です。
type estimateTableRows struct {
	Index int               `json:"index"`
	Lot   string            `json:"lot"`
	Rows  []estimateCostRow `json:"rows"`
	Why   string            `json:"why,omitempty"` // 写せない形の表の理由
}

type estimateCostRow struct {
	Step string `json:"step"`
	Num  string `json:"num"`
	Unit string `json:"unit"`
}

// estimateTablesOf は本文の見積計算表を読みます（列の違う表は Why を付けて行は空）。
func estimateTablesOf(body string) ([]estimateTableRows, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, errString("本文を読めません")
	}
	var out []estimateTableRows
	for i, t := range tablesOfType(nodes, estimateType) {
		tr := estimateTableRows{Index: i, Rows: []estimateCostRow{}}
		rows := rowsOf(t)
		if len(rows) == 0 {
			tr.Why = "行がありません"
			out = append(out, tr)
			continue
		}
		col := headerIndex(rows[0])
		stepCol, okStep := col["工程"]
		numCol, okNum := col["数"]
		if !okStep || !okNum {
			tr.Why = "この形の表（工程・数の列が無い）には写せません"
			out = append(out, tr)
			continue
		}
		unitCol, hasUnit := col["単位"]
		for _, r := range rows[1:] {
			cells := cellTexts(r)
			if stepCol >= len(cells) {
				continue
			}
			step := strings.TrimSpace(cells[stepCol])
			num := ""
			if numCol < len(cells) {
				num = strings.TrimSpace(cells[numCol])
			}
			if step == estimateLotRow {
				tr.Lot = num
			}
			if step == "" || estimateFixedRows[step] {
				continue
			}
			unit := ""
			if hasUnit && unitCol < len(cells) {
				unit = strings.TrimSpace(cells[unitCol])
			}
			tr.Rows = append(tr.Rows, estimateCostRow{Step: step, Num: num, Unit: unit})
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

// withEstimateCost は本文の index 枚目の見積計算表の工程 step の行（add なら新しい行）に単価を写した本文を返します。
func withEstimateCost(body string, index int, step string, add bool, value, note string) (string, error) {
	step = strings.TrimSpace(step)
	value = cleanPrice(value)
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return "", errString("単価が数ではありません（" + value + "）")
	}
	if step == "" {
		return "", errString("写す行（工程）を選んでください")
	}
	if estimateFixedRows[step] {
		return "", errString("「" + step + "」の行には写せません（工程の行を選ぶか、新しい行を足してください）")
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
	stepCol, okStep := col["工程"]
	numCol, okNum := col["数"]
	if !okStep || !okNum {
		return "", errString("この形の表（工程・数の列が無い）には写せません")
	}
	unitCol, hasUnit := col["単位"]
	noteCol, hasNote := col["備考"]
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
		// 新しい行は計算の結果の行（単価・総計・弊社利益・確定単価）の前へ——無ければ表の末尾。
		target = &html.Node{Type: html.ElementNode, Data: "tr"}
		for i := 0; i < len(cellsOf(rows[0])); i++ {
			target.AppendChild(&html.Node{Type: html.ElementNode, Data: "td"})
		}
		setCellText(cellsOf(target)[stepCol], step)
		var before *html.Node
		for _, tr := range rows[1:] {
			if s := stepOf(tr); s != estimateLotRow && estimateFixedRows[s] {
				before = tr
				break
			}
		}
		if before != nil {
			before.Parent.InsertBefore(target, before)
		} else {
			rows[len(rows)-1].Parent.AppendChild(target)
		}
	}
	cells := cellsOf(target)
	if numCol >= len(cells) {
		return "", errString("「" + step + "」の行に数の欄がありません")
	}
	setCellText(cells[numCol], value)
	if hasUnit && unitCol < len(cells) && strings.TrimSpace(textOf(cells[unitCol])) == "" {
		setCellText(cells[unitCol], "円")
	}
	if hasNote && noteCol < len(cells) {
		setCellText(cells[noteCol], mergeQuoteNote(textOf(cells[noteCol]), note))
	}
	return htmldoc.Render(nodes), nil
}

// EstimateRowsAPIHandler は GET /api/estimate/rows?page_id= です。
func EstimateRowsAPIHandler(w http.ResponseWriter, r *http.Request) {
	pageID, _, _, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
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
	cms.WriteJSON(w, map[string]any{"success": true, "tables": tables})
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
