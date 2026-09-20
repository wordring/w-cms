package subcon

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"w-cms/internal/cms"
)

// 検算——発注書の数が互いに辻褄が合うかを、w-cms 自身が確かめます。
//
// ユーザー:「顧客の発注書に数量と単価があって、金額や総額もあるなら、**検算が
// 出来ると思います**」（2026-09-20）。
//
// ⚠ **検算を Gemini にやらせないこと。** 自分の読みを自分で検算させると、数字を
// 合うように書き直します——**誤りが消えるのではなく、見えなくなります**。Gemini に
// 頼むのは「書いてあるまま」だけで、掛け算と足し算はここでやります。
//
// 検算は3本あり、**捕まえるものがそれぞれ違います**:
//
//	行   : 数量 × 単価 = 金額    … その行の数の読み違い
//	小計 : Σ金額 = 小計          … ⚠ **行の落丁**（これだけが捕まえる）
//	合計 : 小計 + 消費税 = 合計   … 小計・消費税そのものの読み違い
//
// ⚠ **行の検算だけでは落丁に気づけません**——1行まるごと落ちても、**残った行は
// 互いに辻褄が合ったまま**だからです。小計との突き合わせが唯一の手段になります。
//
// ⚠ **3本目に税率を使いません。** `小計 × 税率 = 消費税` と書くと 10%／8%（軽減）を
// 当てにいくことになりますが、`小計 + 消費税 = 合計` なら**税率を知らなくても済みます**。
//
// ⚠ **検算が合うことは「正しい」を意味しません。** 合う読み違い方もありえます
// （単価と金額を揃えて取り違える等）。**合わないときに疑える**、それだけの道具です。

// moneyEpsilon は「同じ額」とみなす幅です（円）。
//
// ⚠ **単価に小数がある発注書では、金額に端数処理が入ります**（`390.5 × 3` など）。
// 1円の幅を持たせないと、正しい発注書で毎回 ⚠ が出ます。
// ⚠ 逆に、この幅では**1円の読み違いは捕まりません**——桁の読み違いは必ず1円より
// 大きく外れるので、狙いは果たせます。
const moneyEpsilon = 1.0

// offBy は差が「同じ額」の幅を超えているかを返します。3本の検算が同じ物差しを使います。
func offBy(diff float64) bool {
	return math.Abs(diff) > moneyEpsilon
}

// orderChecksum は検算の結果です。**判断は載せません**——数と、合わなかった事実だけ。
type orderChecksum struct {
	RowIssues []rowIssue // 数量 × 単価 ≠ 金額 だった行
	Checked   int        // 3つの数が揃って検算できた行
	Unchecked int        // 数が揃わず検算できなかった行（小計の行もここに入る）

	AmountSum float64 // Σ金額（検算できた行ぶんだけ）

	Subtotal, Tax, Total          float64
	HasSubtotal, HasTax, HasTotal bool
}

// rowIssue は辻褄の合わなかった1行です。
type rowIssue struct {
	Line                    int // 原本の何行目か（見出しを除いて1始まり）
	Name                    string
	Quantity, Price, Amount float64
}

// quantityHeads・priceHeads・amountHeads は、原本の見出しから列を見つけるための語です。
//
// ⚠ **当てにいかず、見つからなければ黙ります**（検算しない）。見出しは各社ばらばらで、
// 当て推量で別の列を掛け算すると**正しい発注書に ⚠ を出します**——狼少年になると、
// 本物の ⚠ も読まれなくなります。
//
// ⚠ 対応表を持つ**様式ページ**（`取引先／社名／発注書の様式`）が入ったら、そちらが
// 正本になります。ここはそれまでの間に合わせです。
var (
	quantityHeads = []string{"数量", "員数", "数"}
	priceHeads    = []string{"単価", "単金"}
	amountHeads   = []string{"金額", "価格"}
)

// parseMoney は原本のセルを数にします。
//
// カンマ・全角・`￥`・単位（円・個）・`万円` はコアの `NormalizeValue` が畳みます。
// ⚠ ここで足すのは **`△`・`▲`（会計のマイナス）** だけ——値引きの行に出ます。
func parseMoney(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	neg := false
	for _, m := range []string{"△", "▲", "−"} { // ⚠ 最後は全角マイナス（ハイフンではない）
		if rest, cut := strings.CutPrefix(s, m); cut {
			s, neg = rest, true
			break
		}
	}
	norm, ok := cms.NormalizeValue(cms.ColNumber, s)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(norm, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// columnAt は見出しの並びから、語のどれかにぴたり一致する列を探します。
//
// ⚠ **部分一致は採りません**——`合計金額` が `金額` に当たってしまい、合計の行を
// 明細として足すことになります（小計を二重に数える）。
func columnAt(headers []string, words []string) (int, bool) {
	for i, h := range headers {
		got := cms.NormalizeNameForIngest(h)
		for _, w := range words {
			if got == w {
				return i, true
			}
		}
	}
	return 0, false
}

// checkOrderArithmetic は原本の表とヘッダの3つの額から検算します。
//
// ⚠ **足すのは「数量・単価・金額の3つが揃った行」だけ**です。小計・消費税・合計は
// 「表の右下に飛び出して」書かれているので（2026-09-20 ユーザー）、**Gemini は
// それらを明細の行として `rows` に混ぜてきます**——3つ揃わない行として自然に
// 外れますが、外れたことは `Unchecked` で数えて黙らせません。
func checkOrderArithmetic(t orderSourceTable, subtotal, tax, total string) orderChecksum {
	var c orderChecksum
	c.Subtotal, c.HasSubtotal = parseMoney(subtotal)
	c.Tax, c.HasTax = parseMoney(tax)
	c.Total, c.HasTotal = parseMoney(total)

	qi, okQ := columnAt(t.Headers, quantityHeads)
	pi, okP := columnAt(t.Headers, priceHeads)
	ai, okA := columnAt(t.Headers, amountHeads)
	if !okQ || !okP || !okA {
		// 見つからない列があれば、行の検算はしません（当て推量をしない）。
		// ⚠ ヘッダの検算（小計＋消費税＝合計）はそれでもできるので、続けます。
		c.Unchecked = len(t.Rows)
		return c
	}
	ni, _ := columnAt(t.Headers, []string{"品名", "品目", "名称"})

	at := func(row []string, i int) string {
		if i < 0 || i >= len(row) {
			return ""
		}
		return row[i]
	}
	for n, row := range t.Rows {
		q, ok1 := parseMoney(at(row, qi))
		p, ok2 := parseMoney(at(row, pi))
		a, ok3 := parseMoney(at(row, ai))
		if !ok1 || !ok2 || !ok3 {
			c.Unchecked++
			continue
		}
		c.Checked++
		c.AmountSum += a
		if offBy(q*p - a) {
			c.RowIssues = append(c.RowIssues, rowIssue{
				Line: n + 1, Name: strings.TrimSpace(at(row, ni)),
				Quantity: q, Price: p, Amount: a,
			})
		}
	}
	return c
}

// Warnings は人へ見せる ⚠ の文を返します（合っていれば空）。
//
// ⚠ **差額を必ず書きます。** 「合いません」だけでは、どこを見ればよいのか分かりません
// ——差額が1行ぶんの金額と一致していれば落丁、桁1つぶんなら読み違いです。
func (c orderChecksum) Warnings() []string {
	var out []string
	for _, r := range c.RowIssues {
		name := r.Name
		if name == "" {
			name = "（品名なし）"
		}
		out = append(out, fmt.Sprintf("%d行目「%s」: %s × %s = %s のはずが %s と書かれています",
			r.Line, name, money(r.Quantity), money(r.Price), money(r.Quantity*r.Price), money(r.Amount)))
	}
	if c.HasSubtotal && c.Checked > 0 {
		if d := c.AmountSum - c.Subtotal; offBy(d) {
			out = append(out, fmt.Sprintf(
				"明細の金額を足すと %s ですが、小計は %s です（%s の差——行が抜けていませんか）",
				money(c.AmountSum), money(c.Subtotal), money(math.Abs(d))))
		}
	}
	if c.HasSubtotal && c.HasTax && c.HasTotal {
		if d := c.Subtotal + c.Tax - c.Total; offBy(d) {
			out = append(out, fmt.Sprintf(
				"小計 %s ＋ 消費税 %s は %s ですが、合計は %s です（%s の差）",
				money(c.Subtotal), money(c.Tax), money(c.Subtotal+c.Tax), money(c.Total), money(math.Abs(d))))
		}
	}
	return out
}

// Skipped は「検算できなかった」ことを知らせる文を返します（無ければ空）。
//
// ⚠ **黙って飛ばしません。** 検算の印が出ているのに一部しか見ていないのなら、
// それを言わないのは嘘に近いからです。
func (c orderChecksum) Skipped() string {
	if c.Unchecked == 0 {
		return ""
	}
	return fmt.Sprintf("%d行は数が揃わないので検算していません（小計などの行も数に入ります）", c.Unchecked)
}

// money は額を見やすく整えます（小数が無ければ付けない）。
func money(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
