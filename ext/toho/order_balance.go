package toho

// ─────────────────────────────────────────────────────────────────────────
// 受注残高を受注ページに出す——表示のときだけ（2026-09-30）
//
// 利用者:「受注ページに受注残高を出したい」。
//
// **受注残高 = Σ（数量 − 出荷済み）× 単価**——残のある行だけ（出荷済みが数量を超えた行は0）。`状態` が `完了` の行は
// 入れません（受注残表と同じ——人が「もう出さない」と言った行・改定で差し替えた行）。
//
// ⚠ **本文には書きません（鏡型）**——出荷済みを直せば次に開いたとき数え直す（検算と同じ足元に出す・引き金も同じ）。
// ⚠ **単価の読めない行は金額に入れず、数を言います**——黙って入れないと、残高が小さく見えるだけで気づけない。
// どの行にも単価が無ければ、そのまま **0円**（2026-09-30 深夜 利用者:「『単価が無いので出せません』ではなく、シンプルに0円で
// 大丈夫です」——それまでは「0円と言うと残が無いと読まれる」として「出せません」と出していた。単価の無い行の数は括弧に出る）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// orderBalance は受注明細の表から数えた受注残高です。
type orderBalance struct {
	Amount  float64 // Σ 残数 × 単価（単価の読める行だけ）
	Open    int     // 残のある行（完了を除く）
	Rows    int     // 明細の行
	NoPrice int     // 残はあるのに単価が読めない行
	Done    int     // 完了の行（数えない）
}

// orderBalanceOf は受注明細の表（見出し行＋データ行）から受注残高を数えます。見出しに `数量` が無ければ ok=false。
func orderBalanceOf(table *html.Node) (orderBalance, bool) {
	var b orderBalance
	rows := rowsOf(table)
	if len(rows) == 0 {
		return b, false
	}
	head := headerIndex(rows[0])
	iQty, okQty := head["数量"]
	if !okQty {
		return b, false
	}
	iShip, okShip := head["出荷済み"]
	iPrice, okPrice := head["単価"]
	iSt, okSt := head["状態"]
	for _, tr := range rows[1:] {
		cells := cellTexts(tr)
		if len(cells) == 0 || isHeaderRow(tr) {
			continue
		}
		at := func(i int, ok bool) string {
			if !ok || i >= len(cells) {
				return ""
			}
			return cells[i]
		}
		b.Rows++
		if at(iSt, okSt) == StatusDone {
			b.Done++
			continue
		}
		qty, ok := parseMoney(at(iQty, true))
		if !ok {
			continue // 数量の読めない行は残を数えられない
		}
		shipped, _ := parseMoney(at(iShip, okShip))
		rem := math.Max(qty-shipped, 0)
		if rem == 0 {
			continue
		}
		b.Open++
		price, ok := parseMoney(at(iPrice, okPrice))
		if !ok {
			b.NoPrice++
			continue
		}
		b.Amount += rem * price
	}
	return b, true
}

// isHeaderRow は見出しだけの行か（`th` だけで `td` が無い）を返します。
func isHeaderRow(tr *html.Node) bool {
	td := false
	for _, c := range cellsOf(tr) {
		if c.Data == "td" {
			td = true
		}
	}
	return !td
}

// balanceMessage は足元に出す1行です。
func balanceMessage(b orderBalance) string {
	if b.Open == 0 {
		msg := "受注残高: 0円（残のある行はありません"
		if b.Done > 0 {
			msg += "・完了 " + strconv.Itoa(b.Done) + "行"
		}
		return msg + "）"
	}
	msg := "受注残高: " + comma(int(math.Round(b.Amount))) + "円（残のある行 " + strconv.Itoa(b.Open) + "／" +
		strconv.Itoa(b.Rows) + "行"
	if b.NoPrice > 0 {
		msg += "・⚠ 単価の無い " + strconv.Itoa(b.NoPrice) + "行は入っていません"
	}
	return msg + "）"
}

// ── 受注フォルダのトップの受注残高（2026-09-30 夜）──
//
// 利用者:「受注残高は、受注フォルダのトップに入れて欲しいです」——受注残表（backlog.go）のいちばん上に、表に並んだ行の
// Σ 残 × 単価 を出す。**数える行は受注残表と同じ**（「移行中」の受注ページ・`完了` の行・残の無い行は入らない）ので、
// 表と合計が食い違わない。⚠ 受注ページの足元の受注残高（上）は残す——1枚の受注の残高と、フォルダ全体の残高は別の問い。

// backlogTotal は受注残表に並んだ行の合計です。
type backlogTotal struct {
	Amount  float64 // Σ 残 × 単価（単価の読める行だけ）
	Rows    int     // 並んだ行
	Orders  int     // その行を持つ受注ページの枚数
	NoPrice int     // 単価が読めない行
}

// backlogTotalOf は受注残表の組から合計を数えます。
func backlogTotalOf(gs []backlogGroup) backlogTotal {
	var t backlogTotal
	orders := map[string]bool{}
	for _, g := range gs {
		for _, r := range g.Rows {
			t.Rows++
			orders[r.OrderPageID] = true
			if !r.HasPrice {
				t.NoPrice++
				continue
			}
			t.Amount += float64(r.Remaining) * r.Price
		}
	}
	t.Orders = len(orders)
	return t
}

// backlogTotalHTML は受注残表のいちばん上の1行です（行が無ければ空）。中身は数と決まった文だけ（本文の値を含まない）。
func backlogTotalHTML(t backlogTotal) string {
	if t.Rows == 0 {
		return ""
	}
	// どの行にも単価が無ければ 0円（冒頭の注記——単価の無い行の数は括弧に出る）。
	msg := "受注残高: " + comma(int(math.Round(t.Amount))) + "円（残のある行 " + strconv.Itoa(t.Rows) +
		"・受注 " + strconv.Itoa(t.Orders) + "枚"
	if t.NoPrice > 0 {
		msg += "・⚠ 単価の無い " + strconv.Itoa(t.NoPrice) + "行は入っていません"
	}
	return `<p class="backlog-total">` + msg + "）</p>"
}

// appendOrderBalance は受注明細の足元に受注残高の行を足します（数えられなければ何もしない）。
func appendOrderBalance(table *html.Node, span int) {
	b, ok := orderBalanceOf(table)
	if !ok || b.Rows == 0 {
		return
	}
	appendFootRow(table, span, "order-balance", strings.TrimSpace(balanceMessage(b)))
}
