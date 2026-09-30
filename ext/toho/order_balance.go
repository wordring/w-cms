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
	if b.NoPrice == b.Open {
		// 残のある行がどれも単価を持たない——0円と言うと「残が無い」と読まれる。
		return "受注残高: 単価が無いので出せません（残のある行 " + strconv.Itoa(b.Open) + "／" + strconv.Itoa(b.Rows) + "行）"
	}
	msg := "受注残高: " + comma(int(math.Round(b.Amount))) + "円（残のある行 " + strconv.Itoa(b.Open) + "／" +
		strconv.Itoa(b.Rows) + "行"
	if b.NoPrice > 0 {
		msg += "・⚠ 単価の無い " + strconv.Itoa(b.NoPrice) + "行は入っていません"
	}
	return msg + "）"
}

// appendOrderBalance は受注明細の足元に受注残高の行を足します（数えられなければ何もしない）。
func appendOrderBalance(table *html.Node, span int) {
	b, ok := orderBalanceOf(table)
	if !ok || b.Rows == 0 {
		return
	}
	appendFootRow(table, span, "order-balance", strings.TrimSpace(balanceMessage(b)))
}
