package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積計算表と見積表——ロットごとの見積もり（2026-10-15 に形を改めた）
//
// 2026-10-01 の形: 利用者:「加工製品ページの見積もり計算表の下に弊社利益を掛ける項目を作り確定単価としたいです。弊社利益は
// デフォルトで10％なので、単価に1.1をかければ良いのですが、利益率を変更できるようにしたいです。確定単価は目立つように太字です」
// 「単価*10%=○○円と表示したいです。この○○円は見積もりの集計で使うことになるかもしれません」「見積の時には、色を
// 塗るか塗らないか、ロット数などで価格が変わることがあります。そのため、見積もり計算表は複数ある場合があります」。
// ——ワンノートの ■見積もり（`工程｜数｜単位｜備考`・ロット・単価・総計・弊社利益の行が混ざる）の下に、鏡で確定単価を出していた。
//
// 2026-10-15 の形: 利用者:「加工製品ページには、見積もり計算表があります。この計算表はもしかするとDBに入れる必要が無く、
// 結果の単価のみDBに入れるべきかもしれません。DBに入るべきは、弊社品番とロット、単価、表面処理の有無、更新日時になると
// 思います。一方で、部材価格が変わると計算表を更新し、更新された単価をDBに追加する必要が出来ます」「ロットごとに計算表を
// 分けてはどうでしょう？」「見積もり計算表に総計は必要ないです」「ロットごとに表を分けるのは東邦拡張の機能だと思います」。
// 利用者の構成（「という構成はどうでしょう？」→ 筆者の提案を足して「提案通りの構成でお願いします」）:
//
//	見積もり計算（H2）
//	1個（H3）
//	  見積計算表  工程｜価格｜備考          … 加工製品1個あたりの原価の内訳（総計の行は置かない）
//	  見積表      個数｜表面処理｜原価｜利益率｜単価｜備考  … 1行だけ。これが結果（DB に入る単価）
//	20個（H3）
//	  …
//
//   - **見積表は、すぐ上の見積計算表の結果です**（文書の順で組にする——見積計算表のあとに来た最初の見積表）。
//     見出し（1個）は人のための名前で、機械が読むのは見積表の個数・表面処理の列だけ。
//     （2026-10-22 に「ロット」を「個数」に改めた——見出しも「ロット1」から「1個」へ。単価はどれも1個あたり。）
//   - **原価＝見積計算表の価格の列の合計**（空は飛ばす・式は計算した値——コアの FormulaTableValues）。
//     **利益は円未満を四捨五入**、単価＝原価＋利益（2026-10-01 と同じ計算）。利益率は見積表の利益率の欄、空なら設定の既定
//     （`extensions.toho.estimate_profit_rate`・10）。
//   - **単価は人が「📌 この単価を記録」を押したときに見積表へ書く**（鏡で毎回計算しない）——見積書に写すのも、受注の
//     単価と並べるのも、記録した値。計算表を直して記録と合わなくなったら、見積表の下に「⚠ 合いません」と出す
//     （部材の値段が変わったときの知らせ）。
//     2026-10-19 から、テンプレート「見積計算書」の見積表の単価に式（`=ROUND(原価*(1+利益率/100))`）があれば、📌 は原価と
//     利益率だけを書き、単価には式を書く（計算した数は書かない——withEstimateRecord・templateRecordFormulas）。
//   - 見積表が無い見積計算表の下には「📌 見積表を作って記録」を出す（押すと、計算表のすぐ後ろに見積表を足して記録する）。
//   - 「⧉ この組を写す」で、見出し・見積計算表・見積表を組ごと写して、すぐ後ろに置く（個数・原価・単価は空にする）。
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
	"w-cms/internal/cms/page"
)

// estimateType は見積計算表の形式です（vocab.go の part-estimate）。
const estimateType = "part-estimate"

// estimateRecordType は見積表の形式です（vocab.go の part-estimate-record）。
const estimateRecordType = "part-estimate-record"

// 見積計算表・見積表の列の言葉。
const (
	estimateStepCol    = "工程"
	estimatePriceCol   = "価格"
	estimateLotCol     = "個数" // 2026-10-22 までの名前は「ロット」（利用者:「業者の見積もりは1個当たりの単価で来るので、ロットという表現をやめて、個数に統一したほうが分かりやすいでしょうか？」）
	estimateSurfaceCol = "表面処理"
	estimateCostCol    = "原価"
	estimateRateCol    = "利益率"
	estimateUnitCol    = "単価"
	estimateNoteCol    = "備考"
)

func init() {
	cms.RegisterMirror(estimateType, cms.MirrorHandlerFunc(func(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
		if el.Data != "table" {
			return true, nil
		}
		renderEstimateCalc(ctx, el)
		// 見積表の無いページ（移す前の形・見積計算表だけ）は、最後の見積計算表の直後に「🧾 受注・見積の単価」（order_prices.go）。
		if estimateRecordsIn(ctx.PageID) == 0 {
			placeOrderPricesAfterEstimate(ctx, el)
		}
		return true, nil
	}))
	cms.RegisterMirror(estimateRecordType, cms.MirrorHandlerFunc(func(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
		if el.Data != "table" {
			return true, nil
		}
		renderEstimateRecord(ctx, el)
		placeOrderPricesAfterRecord(ctx, el)
		return true, nil
	}))
}

// ── 組（見積計算表と見積表）─────────────────────────────────────────────

// estimateBlock は見積計算表と、その結果の見積表の組です（どちらかが無いこともある）。
type estimateBlock struct {
	Calc   *html.Node
	Record *html.Node
}

// estimateBlocksOf は断片の見積計算表と見積表を文書の順に組にします——見積計算表が組を始め、そのあとの最初の見積表が
// 組の結果。見積計算表の無い見積表は、それだけで1組。
func estimateBlocksOf(nodes []*html.Node) []estimateBlock {
	var out []estimateBlock
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			switch {
			case isTableOfType(n, estimateType):
				out = append(out, estimateBlock{Calc: n})
				return
			case isTableOfType(n, estimateRecordType):
				if k := len(out) - 1; k >= 0 && out[k].Record == nil {
					out[k].Record = n
				} else {
					out = append(out, estimateBlock{Record: n})
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// estimateBlocksOfPage はページの本文（正本のファイル）を読んで組にします。
func estimateBlocksOfPage(pageID string) ([]*html.Node, []estimateBlock, error) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, nil, err
	}
	return nodes, estimateBlocksOf(nodes), nil
}

// blockOfCalc・blockOfRecord は、k 枚目（0から）の見積計算表・見積表の組の番号を返します（無ければ -1）。
func blockOfCalc(blocks []estimateBlock, k int) int {
	for i, b := range blocks {
		if b.Calc != nil {
			if k == 0 {
				return i
			}
			k--
		}
	}
	return -1
}

func blockOfRecord(blocks []estimateBlock, k int) int {
	for i, b := range blocks {
		if b.Record != nil {
			if k == 0 {
				return i
			}
			k--
		}
	}
	return -1
}

// ── 見積計算表の原価 ──────────────────────────────────────────────────

// estimateCost は見積計算表1枚の原価（価格の列の合計）です。
type estimateCost struct {
	OK   bool
	Why  string // 計算できなかった理由
	Cost float64
}

// estimateCostOf は見積計算表の価格の列を足します（空は飛ばす・式は計算した値・数でないものがあれば理由を返す）。
func estimateCostOf(calc *html.Node) estimateCost {
	headers, rows := tableValues(calc)
	if headers == nil {
		return estimateCost{Why: "行がありません"}
	}
	col := map[string]int{}
	for i, h := range headers {
		if _, dup := col[h]; !dup {
			col[h] = i
		}
	}
	priceCol, ok := col[estimatePriceCol]
	if !ok {
		return estimateCost{Why: "この形の表（価格の列が無い）は計算できません"}
	}
	stepCol, hasStep := col[estimateStepCol]
	_, raw := tableRawValues(calc)
	sum, any := 0.0, false
	for i, cells := range rows {
		if priceCol >= len(cells) || strings.TrimSpace(cells[priceCol]) == "" {
			continue
		}
		// 合計の式（=合計(…)）の行は足さない——ほかの行の価格の合計なので、足すと原価が倍になった（2026-10-22 の見直し）。
		if i < len(raw) && priceCol < len(raw[i]) && cms.IsFormula(raw[i][priceCol]) && (strings.Contains(raw[i][priceCol], "合計(") || strings.Contains(raw[i][priceCol], "合計（")) {
			continue
		}
		v, ok := estimateNumber(cells[priceCol])
		if !ok {
			name := strconv.Itoa(i+1) + "行目"
			if hasStep && stepCol < len(cells) && strings.TrimSpace(cells[stepCol]) != "" {
				name = strings.TrimSpace(cells[stepCol])
			}
			return estimateCost{Why: "「" + name + "」の価格が数ではありません（" + strings.TrimSpace(cells[priceCol]) + "）"}
		}
		sum += v
		any = true
	}
	if !any {
		return estimateCost{Why: "価格がまだありません（見積計算表の価格の列に、1個あたりの値段を書くと計算します）"}
	}
	return estimateCost{OK: true, Cost: sum}
}

// tableValues は表の見出しと、見出しより下の行の値を返します（式は計算した値——コアの FormulaTableValues）。
// 見出しの無い表は headers が nil。
func tableValues(t *html.Node) (headers []string, rows [][]string) {
	headers, rows = tableRawValues(t)
	if headers == nil {
		return nil, nil
	}
	return headers, cms.FormulaTableValues(headers, rows)
}

// tableRawValues は tableValues の、式を計算する前の値です（セルに書かれた字のまま）。
func tableRawValues(t *html.Node) (headers []string, rows [][]string) {
	trs := rowsOf(t)
	if len(trs) == 0 {
		return nil, nil
	}
	for _, s := range cellTexts(trs[0]) {
		headers = append(headers, strings.TrimSpace(s))
	}
	for _, tr := range trs[1:] {
		cells := cellTexts(tr)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	return headers, rows
}

// ── 見積表 ──────────────────────────────────────────────────────────

// estimateRecord は見積表の1行（記録）です。値は書いてある文字（式は計算した値）。
type estimateRecord struct {
	Lot, Surface, Cost, Rate, Unit, Note string
}

// estimateRecordOf は見積表の最初の行を読みます（行が無ければ空の記録）。
func estimateRecordOf(rec *html.Node) estimateRecord {
	headers, rows := tableValues(rec)
	var r estimateRecord
	if len(rows) == 0 {
		return r
	}
	at := func(label string) string {
		for i, h := range headers {
			if h == label && i < len(rows[0]) {
				return rows[0][i]
			}
		}
		return ""
	}
	r.Lot, r.Surface, r.Cost = at(estimateLotCol), at(estimateSurfaceCol), at(estimateCostCol)
	r.Rate, r.Unit, r.Note = at(estimateRateCol), at(estimateUnitCol), at(estimateNoteCol)
	return r
}

// estimatePlan は組から計算した、記録すべき値です。
type estimatePlan struct {
	OK          bool
	Why         string
	Cost        float64
	Rate        float64
	RateDefault bool // 見積表の利益率が空で、設定の既定を使った
	Profit      int  // 利益（円・四捨五入）
	Unit        int  // 単価＝原価＋利益
}

// estimatePlanOf は組の見積計算表から原価を出し、見積表の利益率（空なら既定）で単価を計算します。
func estimatePlanOf(b estimateBlock) estimatePlan {
	if b.Calc == nil {
		return estimatePlan{Why: "見積計算表がありません（この見積表のすぐ上に、見積計算表を置くと計算します）"}
	}
	c := estimateCostOf(b.Calc)
	if !c.OK {
		return estimatePlan{Why: c.Why}
	}
	p := estimatePlan{OK: true, Cost: c.Cost}
	rate, ok := 0.0, false
	if b.Record != nil {
		rate, ok = estimateNumber(estimateRecordOf(b.Record).Rate)
	}
	if !ok {
		def, hasDef := EstimateProfitRate()
		if !hasDef {
			return estimatePlan{Why: "利益率の既定が設定されていません（設定の extensions.toho.estimate_profit_rate）"}
		}
		rate, p.RateDefault = def, true
	}
	p.Rate = rate
	p.Profit = int(math.Round(c.Cost * rate / 100))
	p.Unit = int(math.Round(c.Cost)) + p.Profit
	return p
}

// matches は見積表の記録が計算と合っているかです（原価・利益率・単価の3つ）。
func (p estimatePlan) matches(r estimateRecord) bool {
	cost, ok1 := estimateNumber(r.Cost)
	rate, ok2 := estimateNumber(r.Rate)
	unit, ok3 := estimateNumber(r.Unit)
	if !(ok1 && ok2 && ok3 && math.Abs(cost-p.Cost) < 0.005 && math.Abs(rate-p.Rate) < 1e-9) {
		return false
	}
	// ⚠ **単価は2つの丸め方のどちらでも合っているとします**（2026-10-20）——計算は「原価の円＋利益の円」で丸めますが、
	// テンプレート「見積計算書」の単価の式 `=ROUND(原価*(1+利益率/100))` はまとめて1回だけ丸めます（2026-10-19・📌 は式を残す）。
	// 原価 50・利益率 15 だと、計算は 58、式は 57 になり、📌 で記録した直後なのに「⚠ 記録が計算表と合いません」が出ていた。
	u := int(math.Round(unit))
	return u == p.Unit || u == int(math.Round(p.Cost*(1+p.Rate/100)))
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
	// 「NaN」「Inf」は数とみなさない（紙の金額や納めた数が壊れた値になる・2026-10-22 の見直し）。
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// yen は円の数をカンマ区切りにします（小数は四捨五入）。
func yen(v float64) string { return comma(int(math.Round(v))) + "円" }

// rateText は率を書きます（10 → 10・2.5 → 2.5）。
func rateText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ── 鏡 ──────────────────────────────────────────────────────────────

// planText は計算の1行です（「計算表から: 原価 1,200円 × 利益率 10% = 利益 120円 → 単価 1,320円」）。
func planText(p estimatePlan) string {
	rate := rateText(p.Rate) + "%"
	if p.RateDefault {
		rate += "（既定）"
	}
	return "計算表から: 原価 " + yen(p.Cost) + " × 利益率 " + rate + " = 利益 " + comma(p.Profit) + "円 → 単価 " + comma(p.Unit) + "円"
}

// renderEstimateCalc は見積計算表の足元です——見積表があれば何も出さない（結果は見積表の下）。無ければ、計算と
// 「📌 見積表を作って記録」。
func renderEstimateCalc(ctx *cms.MirrorContext, el *html.Node) {
	cms.DropChrome(el)
	// 足元のいちばん上に、価格の列の合計（2026-10-23・見積表があってもなくても出す。鏡なので本文には書かない）。
	// 利用者:「【弊社】見積計算書の見積もり計算表の下に価格の合計を入れてください。」
	if c := estimateCostOf(el); c.OK {
		appendFootRow(el, headerCellCount(el), "estimate-calc-total", "価格の合計: "+yen(c.Cost))
	}
	k := ctx.Counter(estimateType)
	_, blocks, err := estimateBlocksOfPage(pageIDOf(ctx))
	if err != nil {
		return
	}
	bi := blockOfCalc(blocks, k)
	if bi < 0 || blocks[bi].Record != nil {
		return
	}
	span := headerCellCount(el)
	p := estimatePlanOf(estimateBlock{Calc: el})
	if !p.OK {
		appendFootRow(el, span, "estimate-note", p.Why)
		return
	}
	appendFootRow(el, span, "estimate-plan", planText(p))
	if ctx.Viewer != nil && canWritePage(ctx.Viewer, ctx.PageID) {
		appendFootHTML(el, span, "estimate-record", estimateRecordButtonHTML(bi, estimateBlockSig(blocks[bi]), "📌 見積表を作って記録",
			"この見積計算表のすぐ後ろに見積表を足して、原価・利益率・単価を書きます（個数と表面処理は書いてください）"))
	}
}

// renderEstimateRecord は見積表の足元です——計算表から計算した値と、記録が合っているか・「📌 この単価を記録」・
// 「⧉ この組を写す」（2026-10-22 までの名前は「⧉ このロットを写す」）・「📄 見積書に入れる」。
func renderEstimateRecord(ctx *cms.MirrorContext, el *html.Node) {
	cms.DropChrome(el)
	k := ctx.Counter(estimateRecordType)
	_, blocks, err := estimateBlocksOfPage(pageIDOf(ctx))
	if err != nil {
		return
	}
	bi := blockOfRecord(blocks, k)
	if bi < 0 {
		return
	}
	span := headerCellCount(el)
	b := estimateBlock{Calc: blocks[bi].Calc, Record: el} // 記録は見ている表から（計算表は正本から）
	rec := estimateRecordOf(el)
	p := estimatePlanOf(b)
	canWrite := ctx.Viewer != nil && canWritePage(ctx.Viewer, ctx.PageID)
	switch {
	case !p.OK:
		appendFootRow(el, span, "estimate-note", p.Why)
	case p.matches(rec):
		appendFootRow(el, span, "estimate-plan", "✓ "+planText(p)+"（記録と合っています）")
	default:
		say := "⚠ 記録が計算表と合いません（記録の単価: " + orBlank(rec.Unit) + "）"
		if strings.TrimSpace(rec.Unit) == "" {
			say = "まだ記録していません"
		}
		appendFootRow(el, span, "estimate-plan", planText(p))
		appendFootRow(el, span, "estimate-stale", say)
		if canWrite {
			appendFootHTML(el, span, "estimate-record", estimateRecordButtonHTML(bi, estimateBlockSig(blocks[bi]), "📌 この単価を記録",
				"見積計算表から計算した原価・利益率・単価を、この見積表に書きます"))
		}
	}
	if canWrite {
		appendFootHTML(el, span, "estimate-dup", `<button type="button" class="estimate-dup-go" data-estimate-block="`+
			strconv.Itoa(bi)+`" data-estimate-sig="`+estimateBlockSig(blocks[bi])+`" title="見出し・見積計算表・見積表を組ごと写して、すぐ後ろに置きます（個数・原価・単価は空にします）">⧉ この組を写す</button>`+
			` <span class="estimate-dup-say"></span>`)
		// 見積書に入れる（2026-10-01・estimate_doc.go）——見積表の単価・ロット・表面処理と備考を見積書の見積明細表へ写す。
		appendFootHTML(el, span, "estimate-add", estimateAddFormHTML(ctx.Viewer, ctx.PageID, k, tableSig(blocks[bi].Record)))
	}
}

func orBlank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "空"
	}
	return s
}

// estimateBlockSig は見積の組（見積計算表と見積表）の指紋です（table_read.go の tableSig・2026-10-22 の見直し）。
func estimateBlockSig(b estimateBlock) string {
	return tableSig(b.Calc) + "/" + tableSig(b.Record)
}

func estimateRecordButtonHTML(block int, sig, label, title string) string {
	return `<button type="button" class="estimate-record-go" data-estimate-block="` + strconv.Itoa(block) +
		`" data-estimate-sig="` + sig + `" title="` +
		stdhtml.EscapeString(title) + `">` + stdhtml.EscapeString(label) + `</button> <span class="estimate-record-say"></span>`
}

// pageIDOf は鏡のページIDを6桁で返します。
func pageIDOf(ctx *cms.MirrorContext) string { return page.FormatID(ctx.PageID) }

// ── 記録する・写す ─────────────────────────────────────────────────────

// EstimateRecordAPIHandler は POST /api/estimate/record です。入力: {page_id, block}——ページの block 番目（0から・文書の順）
// の組の見積計算表から原価・利益率・単価を計算して、見積表に書きます（見積表が無ければ見積計算表のすぐ後ろに作る）。
func EstimateRecordAPIHandler(w http.ResponseWriter, r *http.Request) {
	estimateBlockAPI(w, r, "estimate.record", withEstimateRecord)
}

// EstimateDuplicateAPIHandler は POST /api/estimate/duplicate です。入力: {page_id, block}——その組（見出し・見積計算表・
// 見積表）を写して、すぐ後ろに置きます。
func EstimateDuplicateAPIHandler(w http.ResponseWriter, r *http.Request) {
	estimateBlockAPI(w, r, "estimate.duplicate", withEstimateDuplicate)
}

func estimateBlockAPI(w http.ResponseWriter, r *http.Request, audit string, edit func(body string, block int) (string, error)) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Block  int    `json:"block"`
		Sig    string `json:"sig"` // 描いたときの組の指紋（estimateBlockSig・2026-10-22）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	// 番号は描いたときのもの——いまの本文で、指紋の合う組を引き直す（ほかの人がロットを写すと、番号がずれて別の組に効いていた）。
	edit0 := edit
	edit = func(body string, block int) (string, error) {
		nodes, err := htmldoc.ParseFragment(body)
		if err != nil {
			return "", errString("本文を読めません")
		}
		blocks := estimateBlocksOf(nodes)
		i := pickBySig(len(blocks), func(i int) string { return estimateBlockSig(blocks[i]) }, block, req.Sig)
		if i < 0 {
			if req.Sig == "" {
				return "", errString("その見積計算表が見つかりません（ページを開き直してください）")
			}
			return "", errTableMoved
		}
		return edit0(body, i)
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を読めません: "+err.Error())
		return
	}
	// 先に確かめて理由を返す（書く直前にも同じ手で、そのときの本文に当てる——人の保存と重なっても消さない）。
	if _, err := edit(body, req.Block); err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	var lateErr error
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		next, err := edit(cur, req.Block)
		if err != nil {
			lateErr = err
			return cur
		}
		return next
	}) {
		return
	}
	if lateErr != nil {
		cms.JSONFail(w, http.StatusConflict, lateErr.Error()+"（ページを開き直してください）")
		return
	}
	auth.Audit(user.Username, audit, pageID+" #"+strconv.Itoa(req.Block))
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// estimateBlockAt は本文を読んで、block 番目の組（見積計算表と見積表）を返します（読めない・無ければ、人に見せる断り）。
// 2026-10-19 に寄せた（記録する口と写す口が同じ9行を写していた）。
func estimateBlockAt(body string, block int) ([]*html.Node, estimateBlock, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, estimateBlock{}, errString("本文を読めません")
	}
	blocks := estimateBlocksOf(nodes)
	if block < 0 || block >= len(blocks) {
		return nil, estimateBlock{}, errString("その見積計算表が見つかりません（ページを開き直してください）")
	}
	return nodes, blocks[block], nil
}

// withEstimateRecord は本文の block 番目の組の見積表に、計算した原価・利益率・単価を書いた本文を返します。
func withEstimateRecord(body string, block int) (string, error) {
	nodes, b, err := estimateBlockAt(body, block)
	if err != nil {
		return "", err
	}
	p := estimatePlanOf(b)
	if !p.OK {
		return "", errString("記録できません: " + p.Why)
	}
	rec := b.Record
	if rec == nil {
		rec = newEstimateRecordTable()
		nodes = insertAfter(nodes, b.Calc, rec)
	}
	trs := rowsOf(rec)
	if len(trs) == 0 {
		return "", errString("見積表に見出しがありません")
	}
	col := headerIndex(trs[0])
	for _, need := range []string{estimateCostCol, estimateRateCol, estimateUnitCol} {
		if _, ok := col[need]; !ok {
			return "", errString("見積表に「" + need + "」の列がありません")
		}
	}
	if len(trs) < 2 {
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for range cellsOf(trs[0]) {
			tr.AppendChild(&html.Node{Type: html.ElementNode, Data: "td"})
		}
		trs[0].Parent.AppendChild(tr)
		trs = append(trs, tr)
	}
	cells := cellsOf(trs[1])
	set := func(label, v string) {
		if i := col[label]; i < len(cells) {
			setCellText(cells[i], v)
		}
	}
	set(estimateCostCol, cms.FormatFormulaNumber(p.Cost))
	set(estimateRateCol, rateText(p.Rate))
	// 原価と利益率のほかの列は、式なら残し、テンプレート「見積計算書」の見積表に式があればそれを写す（2026-10-19）。
	// 2026-10-19 利用者:「この式をテンプレートに書いておく方式はどうでしょう？」「出来るだけハードコーディングを無くしたいです」
	// 式はコードに書かない——テンプレートに無ければ、いままでどおり単価に計算した数を書く。
	isFormulaAt := func(label string) bool {
		i, ok := col[label]
		return ok && i < len(cells) && cms.IsFormula(strings.TrimSpace(textOf(cells[i])))
	}
	for label, f := range templateRecordFormulas() {
		if label == estimateCostCol || label == estimateRateCol || isFormulaAt(label) {
			continue
		}
		set(label, f)
	}
	if !isFormulaAt(estimateUnitCol) {
		set(estimateUnitCol, strconv.Itoa(p.Unit))
	}
	return htmldoc.Render(nodes), nil
}

// templateRecordFormulas は、テンプレート「見積計算書」の本文の最初の見積表の1行目にある式を、列の見出しごとに返します
// （2026-10-19——見積表の単価の式 `=ROUND(原価*(1+利益率/100))` などを、コードに書かずにテンプレートに置く）。
// テンプレートが無い・見積表が無い・式が無ければ空。
func templateRecordFormulas() map[string]string {
	body, err := cms.PageTemplateBody(EstimateCalcTemplate)
	if err != nil {
		return nil
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	recs := tablesOfType(nodes, estimateRecordType)
	if len(recs) == 0 {
		return nil
	}
	trs := rowsOf(recs[0])
	if len(trs) < 2 {
		return nil
	}
	head, cells := cellTexts(trs[0]), cellTexts(trs[1])
	out := map[string]string{}
	for i, h := range head {
		if i < len(cells) && cms.IsFormula(strings.TrimSpace(cells[i])) {
			out[strings.TrimSpace(h)] = strings.TrimSpace(cells[i])
		}
	}
	return out
}

// estimateRecordColumns は見積表の列です（vocab.go の宣言と同じ並び）。
var estimateRecordColumns = []string{estimateLotCol, estimateSurfaceCol, estimateCostCol, estimateRateCol, estimateUnitCol, estimateNoteCol}

// newEstimateRecordTable は空の見積表（見出しの行と空の1行）です。
func newEstimateRecordTable() *html.Node {
	var b strings.Builder
	b.WriteString(`<table><caption>見積表</caption><tbody><tr>`)
	for _, c := range estimateRecordColumns {
		b.WriteString(`<th>` + c + `</th>`)
	}
	b.WriteString(`</tr><tr>`)
	for range estimateRecordColumns {
		b.WriteString(`<td></td>`)
	}
	b.WriteString(`</tr></tbody></table>`)
	ns, _ := htmldoc.ParseFragment(b.String())
	for _, n := range ns {
		if n.Type == html.ElementNode && n.Data == "table" {
			return n
		}
	}
	return nil
}

// withEstimateDuplicate は本文の block 番目の組（すぐ前の見出し・見積計算表・見積表）を写して、組のすぐ後ろに置いた本文を返します。
// 写しの見積表は、ロット・原価・単価を空にします（表面処理・利益率・備考は残す。式のセルは空にしない）。見出しは「（写し）」を付けます。
func withEstimateDuplicate(body string, block int) (string, error) {
	nodes, b, err := estimateBlockAt(body, block)
	if err != nil {
		return "", err
	}
	if b.Calc == nil {
		return "", errString("見積計算表の無い見積表は写せません")
	}
	var copies []*html.Node
	if h := prevElement(nodes, b.Calc); h != nil && h.Data == "h3" {
		hc := cloneTree(h)
		setCellText(hc, strings.TrimSpace(textOf(h))+"（写し）")
		copies = append(copies, hc)
	}
	copies = append(copies, cloneTree(b.Calc))
	last := b.Calc
	if b.Record != nil {
		rc := cloneTree(b.Record)
		trs := rowsOf(rc)
		if len(trs) >= 2 {
			col := headerIndex(trs[0])
			cells := cellsOf(trs[1])
			for _, label := range []string{estimateLotCol, estimateCostCol, estimateUnitCol} {
				// 式は残す（2026-10-19——単価の式 =ROUND(原価*(1+利益率/100)) などは、写しでも同じ式で計算する）。
				if i, ok := col[label]; ok && i < len(cells) && !cms.IsFormula(strings.TrimSpace(textOf(cells[i]))) {
					setCellText(cells[i], "")
				}
			}
		}
		copies = append(copies, rc)
		last = b.Record
	}
	for i := len(copies) - 1; i >= 0; i-- {
		nodes = insertAfter(nodes, last, copies[i])
	}
	return htmldoc.Render(nodes), nil
}

// insertAfter は ref のすぐ後ろに n を置きます。ref が断片の上の段（親が無い）なら、nodes の並びに差し込んだものを返します。
func insertAfter(nodes []*html.Node, ref, n *html.Node) []*html.Node {
	if ref.Parent != nil {
		ref.Parent.InsertBefore(n, ref.NextSibling)
		return nodes
	}
	for i, x := range nodes {
		if x == ref {
			out := append([]*html.Node{}, nodes[:i+1]...)
			out = append(out, n)
			return append(out, nodes[i+1:]...)
		}
	}
	return append(nodes, n)
}

// prevElement は el のすぐ前の要素です（空白の文字は飛ばす・上の段なら nodes の並びで見る）。
func prevElement(nodes []*html.Node, el *html.Node) *html.Node {
	isBlank := func(n *html.Node) bool { return n.Type == html.TextNode && strings.TrimSpace(n.Data) == "" }
	if el.Parent != nil {
		for p := el.PrevSibling; p != nil; p = p.PrevSibling {
			if isBlank(p) {
				continue
			}
			if p.Type == html.ElementNode {
				return p
			}
			return nil
		}
		return nil
	}
	for i, x := range nodes {
		if x != el {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if isBlank(nodes[j]) {
				continue
			}
			if nodes[j].Type == html.ElementNode {
				return nodes[j]
			}
			return nil
		}
	}
	return nil
}

// cloneTree は要素を中身ごと写します。`data-id` は落とします（同じ番号が2つできないように——保存で振り直される）。
func cloneTree(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace}
	for _, a := range n.Attr {
		if a.Key != "data-id" {
			c.Attr = append(c.Attr, a)
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.AppendChild(cloneTree(ch))
	}
	return c
}

// errString は文だけのエラーです。
type errString string

func (e errString) Error() string { return string(e) }
