package toho

// ─────────────────────────────────────────────────────────────────────────
// 1台あたりの個数が小数の行を知らせる（2026-10-22）
//
// 利用者:「一台あたりの数が少数の場合、警告してください」
//
// 要る数は「1台あたり × 台数」を切り上げて数えます（procurement.go の productNeeds。0.5 枚 × 3台 → 2枚）。
// 小数が書き間違いでも、切り上げで黙って数が変わるので、加工製品ページの部材の表の足元で知らせます。
// どの列が1台あたりかは、設定の種類（order_kinds の columns の `数量` の右側）が決めます。
// 印は表示のときだけです（`<tfoot class="vocab-chrome">` とセルの class は保存されない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

// perUnitLabel は、その形式の表で1台あたりの個数を書く列の名前です（設定の種類に無い表・数量を運ばない種類は空）。
func perUnitLabel(vocabType string) string {
	for _, k := range OrderKinds() {
		def, ok := cms.VocabDefByHeading(k.From)
		if !ok || def.Type != vocabType {
			continue
		}
		return strings.TrimSpace(k.Columns[orderQtyLabel])
	}
	return ""
}

// isFractional は、数が整数でないかを返します。
func isFractional(f float64) bool {
	return math.Abs(f-math.Round(f)) > 1e-9
}

// markFractionalPerUnit は、1台あたりの個数が小数の行のセルに印を付け、表の足元に警告の行を足します。
//
// ⚠ 廃版の行は数えない（手配に入らない・isObsoleteRow と同じ）。鏡が足した `<tfoot>` の行も見ない。
func markFractionalPerUnit(vocabType string, tbl *html.Node) {
	label := perUnitLabel(vocabType)
	if label == "" {
		return
	}
	rows := rowsOf(tbl)
	if len(rows) < 2 {
		return
	}
	col := headerIndexOf(rows[0], label)
	if col < 0 {
		return
	}
	status := statusColumnIndex(tbl)
	var found []string
	n := 0
	for _, tr := range rows[1:] {
		if tr.Parent != nil && tr.Parent.Data == "tfoot" {
			continue
		}
		n++
		if status >= 0 && cellText(tr, status) == obsoleteMark {
			continue
		}
		v := cellText(tr, col)
		f, ok := estimateNumber(v)
		if !ok || !isFractional(f) {
			continue
		}
		addClass(cellsOf(tr)[col], "cell-fraction")
		found = append(found, strconv.Itoa(n)+"行目 "+v)
	}
	if len(found) == 0 {
		return
	}
	appendFootRow(tbl, len(cellsOf(rows[0])), "per-unit-fraction",
		"⚠ 1台あたりの"+label+"が小数の行があります（"+strings.Join(found, "・")+"）。"+
			"必要な数は「1台あたり × 台数」を切り上げて数えます（0.5 × 3台 → 2）。")
}
