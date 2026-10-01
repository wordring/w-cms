package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積計算表の確定単価——弊社利益を掛ける（2026-10-01）
//
// 利用者:「加工製品ページの見積もり計算表の下に弊社利益を掛ける項目を作り確定単価としたいです。弊社利益はデフォルトで
// 10％なので、単価に1.1をかければ良いのですが、利益率を変更できるようにしたいです。確定単価は目立つように太字です」
// 「単価*10%=○○円と表示したいです。この○○円は見積もりの集計で使うことになるかもしれません」「見積の時には、色を
// 塗るか塗らないか、ロット数などで価格が変わることがあります。そのため、見積もり計算表は複数ある場合があります」。
//
// 見積計算表（ワンノートの ■見積もり・`工程｜数｜単位｜備考`）は**第1列が鍵の表**です——行が項目（ロット・材料・
// ブランク・…・単価・総計）で、`数` がその値。表の下に**鏡で**2行を出します（本文には書かない）:
//
//	弊社利益: 単価 365円 × 10% = 37円
//	確定単価: 402円（太字）
//
//   - **単価**は「単価」の行の数。空なら、単位が「円」の行（単価・総計・弊社利益は除く）を足したもの——ワンノートで
//     単価を空のまま総計だけ書いた表があるため。足したときはそう書く。
//   - **率**は表の「弊社利益」の行の数（%）。無ければ設定の既定（`extensions.toho.estimate_profit_rate`・10）。
//     表ごとに変えられる——足元の欄で変えると、その行を書く（無ければ表の末尾に足す・`/api/estimate-rate`）。
//   - 利益は**円未満を四捨五入**。確定単価＝単価＋利益。
//   - 表が何枚あっても1枚ずつ（塗装あり・なし、ロットごとに表を分ける）。
//   - ⚠ 鏡なので、人が単価や率を直せば次に開いたときに計算し直す（焼き込まない）。集計で使う日は、同じ関数
//     （`estimateOf`）で表から計算する。
//   - 列の違う見積計算表（`ロット20｜ロット40` を列にした表など）は計算できないので、そう書く。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	stdhtml "html"
	"math"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// estimateType は見積計算表の形式です（vocab.go の part-estimate）。
const estimateType = "part-estimate"

// 見積計算表の行の名前（`工程` の列に書く言葉）。
const (
	estimateUnitPriceRow = "単価"
	estimateTotalRow     = "総計"
	estimateProfitRow    = "弊社利益"
	estimateFinalRow     = "確定単価"
)

func init() {
	cms.RegisterMirror(estimateType, cms.MirrorHandlerFunc(renderEstimate))
}

// estimateResult は見積計算表1枚の計算の結果です。
type estimateResult struct {
	OK        bool    // 計算できた
	Why       string  // できなかった理由（OK でないとき）
	UnitPrice float64 // 単価
	Summed    bool    // 単価の行が空で、円の行を足した
	Rate      float64 // 利益率（%）
	RateSet   bool    // 表の「弊社利益」の行から読んだ（false なら既定）
	Profit    int     // 利益（円・四捨五入）
	Final     int     // 確定単価（円）
}

// estimateOf は見積計算表1枚を計算します（defaultRate は表に「弊社利益」の行が無いときの率）。
func estimateOf(table *html.Node, defaultRate float64, hasDefault bool) estimateResult {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return estimateResult{Why: "行がありません"}
	}
	col := headerIndex(rows[0])
	stepCol, okStep := col["工程"]
	numCol, okNum := col["数"]
	if !okStep || !okNum {
		return estimateResult{Why: "この形の表（工程・数の列が無い）は計算できません"}
	}
	unitCol, hasUnit := col["単位"]
	var r estimateResult
	unitPrice, unitSet := 0.0, false
	sum, summed := 0.0, false
	for _, tr := range rows[1:] {
		cells := cellTexts(tr)
		if stepCol >= len(cells) || numCol >= len(cells) {
			continue
		}
		step := strings.TrimSpace(cells[stepCol])
		v, ok := estimateNumber(cells[numCol])
		switch step {
		case estimateUnitPriceRow:
			if ok {
				unitPrice, unitSet = v, true
			}
		case estimateProfitRow:
			if ok {
				r.Rate, r.RateSet = v, true
			}
		case estimateTotalRow, estimateFinalRow:
		default:
			if ok && hasUnit && unitCol < len(cells) && strings.TrimSpace(cells[unitCol]) == "円" {
				sum += v
				summed = true
			}
		}
	}
	switch {
	case unitSet:
		r.UnitPrice = unitPrice
	case summed:
		r.UnitPrice, r.Summed = sum, true
	default:
		return estimateResult{Why: "単価がまだありません（「単価」の行か、単位が円の行に数を書くと計算します）"}
	}
	if !r.RateSet {
		if !hasDefault {
			return estimateResult{Why: "利益率の既定が設定されていません（設定の extensions.toho.estimate_profit_rate）"}
		}
		r.Rate = defaultRate
	}
	r.Profit = int(math.Round(r.UnitPrice * r.Rate / 100))
	r.Final = int(math.Round(r.UnitPrice)) + r.Profit
	r.OK = true
	return r
}

// estimateNumber は表の数を読みます（カンマ・全角・「円」「%」を許す）。空や数でないものは ok=false。
func estimateNumber(s string) (float64, bool) {
	s = strings.TrimSpace(strings.NewReplacer("円", "", "%", "", "％", "").Replace(s))
	if s == "" {
		return 0, false
	}
	if norm, ok := cms.NormalizeValue(cms.ColNumber, s); ok {
		s = norm
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// yen は円の数をカンマ区切りにします（小数は四捨五入）。
func yen(v float64) string { return comma(int(math.Round(v))) + "円" }

// rateText は率を書きます（10 → 10・2.5 → 2.5）。
func rateText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// renderEstimate は見積計算表の足元に、弊社利益と確定単価を出します（鏡）。
func renderEstimate(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	index := ctx.Counter(estimateType)
	span := headerCellCount(el)
	def, hasDef := EstimateProfitRate()
	r := estimateOf(el, def, hasDef)
	if !r.OK {
		appendFootRow(el, span, "estimate-note", r.Why)
		return true, nil
	}
	basis := "単価 " + yen(r.UnitPrice)
	if r.Summed {
		basis = "単価（円の行の合計） " + yen(r.UnitPrice)
	}
	rate := rateText(r.Rate) + "%"
	if !r.RateSet {
		rate += "（既定）"
	}
	appendFootRow(el, span, "estimate-profit", estimateProfitRow+": "+basis+" × "+rate+" = "+comma(r.Profit)+"円")
	appendFootHTML(el, span, "estimate-final", `<strong>`+estimateFinalRow+`: `+stdhtml.EscapeString(comma(r.Final))+`円</strong>`)
	// 率を変える欄——書ける人にだけ（閲覧モードで押す・編集モードでは表の「弊社利益」の行を直接直せる）。
	if ctx.Viewer != nil && canWritePage(ctx.Viewer, ctx.PageID) {
		appendFootHTML(el, span, "estimate-rate", `<span class="estimate-rate-form" data-estimate-index="`+
			strconv.Itoa(index)+`">利益率 <input type="number" class="estimate-rate-input" min="0" step="0.1" value="`+
			stdhtml.EscapeString(rateText(r.Rate))+`"> % <button type="button" class="estimate-rate-set">変える</button>`+
			` <span class="estimate-rate-say"></span></span>`)
	}
	return true, nil
}

// EstimateRateAPIHandler は POST /api/estimate-rate です。入力: {page_id, index, rate}——ページの index 枚目
// （0から・文書の順）の見積計算表の「弊社利益」の行に率を書きます（無ければ表の末尾に足す）。
func EstimateRateAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string  `json:"page_id"`
		Index  int     `json:"index"`
		Rate   float64 `json:"rate"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if req.Rate < 0 || req.Rate >= 1000 || math.IsNaN(req.Rate) {
		cms.JSONFail(w, http.StatusBadRequest, "利益率は 0 以上 1000 未満で書いてください")
		return
	}
	pageID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を読めません: "+err.Error())
		return
	}
	next, err := withEstimateRate(body, req.Index, req.Rate)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !rewriteBodyOrFail(w, pageID, user.Username, func(string) string { return next }) {
		return
	}
	auth.Audit(user.Username, "estimate.rate", pageID+" #"+strconv.Itoa(req.Index)+" "+rateText(req.Rate)+"%")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// withEstimateRate は本文の index 枚目の見積計算表の「弊社利益」の行に率を書いた本文を返します。
func withEstimateRate(body string, index int, rate float64) (string, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", err
	}
	tables := tablesOfType(nodes, estimateType)
	if index < 0 || index >= len(tables) {
		return "", errString("その見積計算表が見つかりません（ページを開き直してください）")
	}
	t := tables[index]
	rows := rowsOf(t)
	if len(rows) < 1 {
		return "", errString("見積計算表に見出しがありません")
	}
	col := headerIndex(rows[0])
	stepCol, okStep := col["工程"]
	numCol, okNum := col["数"]
	if !okStep || !okNum {
		return "", errString("この形の表（工程・数の列が無い）には書けません")
	}
	for _, tr := range rows[1:] {
		cells := cellsOf(tr)
		if stepCol < len(cells) && numCol < len(cells) && strings.TrimSpace(textOf(cells[stepCol])) == estimateProfitRow {
			setCellText(cells[numCol], rateText(rate))
			return htmldoc.Render(nodes), nil
		}
	}
	// 無ければ表の末尾に1行足す（見出しの列の数だけ・単位の列には %）。
	unitCol, hasUnit := col["単位"]
	n := len(cellsOf(rows[0]))
	tr := &html.Node{Type: html.ElementNode, Data: "tr"}
	for i := 0; i < n; i++ {
		td := &html.Node{Type: html.ElementNode, Data: "td"}
		switch {
		case i == stepCol:
			setCellText(td, estimateProfitRow)
		case i == numCol:
			setCellText(td, rateText(rate))
		case hasUnit && i == unitCol:
			setCellText(td, "%")
		}
		tr.AppendChild(td)
	}
	rows[len(rows)-1].Parent.AppendChild(tr)
	return htmldoc.Render(nodes), nil
}

// errString は文だけのエラーです。
type errString string

func (e errString) Error() string { return string(e) }
