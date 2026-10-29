package cms

// ─────────────────────────────────────────────────────────────────────────
// 表のセルの式（2026-10-04）
//
// 利用者:「汎用的な仕組みとして、列の題名で計算させるということです。=個数*単価みたいにセルに書きます。編集時には
// 書いたままを表示して編集でき、閲覧時には計算結果を表示できるのはどうですか？数値に変換できない場合、エラーと赤背景
// 赤太文字を表示してはどうでしょう？」「小数も残す」。——【考察】表の集計の一般化 §4.5（表の中に式を書く・採らない）は
// 筆者の判断で、利用者の確認待ちだった。この日に利用者が決めた（同書の決定ログ）。
//
//   - **本文に入るのは式だけ**（`=個数*単価`）。値は書かない——正本は1つ（§4.5 の心配の1つ目）。
//   - **計算はここ1か所**——閲覧の表示（RenderComputedViews が `td` に結果の属性を付け、画面が見せる）と、DB への索引・
//     表の写しが同じ関数を通る。見えている値と、DB で探せる値が食い違わない。
//   - **読めないときは理由を返す**（黙って空にしない・§4.5 の心配の3つ目）。画面は赤で出す。
//   - **空のセルを参照したら結果も空**（エラーにしない）——空欄は「まだ分からない」（単価に 0 を書かないのと同じ線）。
//
// 書き方: `=` か `＝` で始める。列は**見出しの言葉**で指す（全角半角・前後の空白は畳んで比べる）。演算は
// `+ - * / × ÷`（全角も）と括弧（全角も）・数（`1.1`・全角も）。見出しに括弧や空白・演算の記号が入る列は
// `「単価（ロット1）」`・`[単価 (ロット1)]` のように囲む。⚠ 長音 `ー` は名前の一部（引き算ではない）。
//
// ── 縦の計算と合計（2026-10-15）──
//
// 利用者:「表のセルには=単価*数といった式が書ける仕様になっっていると思います。これを縦にも適用できるように出来ないでしょうか？
// ExcelのオートSUMのように総計を出したいのです」「オートSUMはコアの機能」「SUMは必要ないです。列の見出し以外全部の合計も
// あったほうがよいと思います」。
//
//   - **行は先頭の列の言葉で指す**（`=材料+塗装`——同じ列の「材料」の行と「塗装」の行）。列の名前と同じ規則で畳んで比べる。
//     同じ名前の行が2つあれば上のもの（先勝ち）。
//   - **名前は列を先に探し、次に行を探す。** 列にも行にもある名前は読み違えるので**エラーにして**、`列「単価」`・`行「単価」`
//     と書き分けてもらう（黙ってどちらかに決めない）。
//   - **関数は `合計` の1つだけ**（`SUM` は受けない——利用者「SUMは必要ないです」）。中身は4つの形:
//     `合計()` … 自分の列の、見出しの行と自分を除いた全部（利用者「列の見出し以外全部の合計」）
//     `合計(上)` … 自分の列の、自分より上の全部
//     `合計(価格)` … 名前が列なら、その列の全部（自分は除く）。名前が行なら、その行の先頭の列を除いた全部
//     `合計(材料:塗装)` … 両端が行なら自分の列のその範囲、両端が列なら自分の行のその範囲（`：` でも）
//   - 合計は**空のセルを飛ばします**（表計算ソフトと同じ）。全部が空なら結果も空。数でないセルがあればエラー
//     （黙って飛ばすと、打ち損じた値が合計から消える）。
//   - ⚠ 表計算ソフトの再発明にしない（§4.1）——関数を足すのは、要るものが出てからにする。
//   - **`ROUND(式)`**（2026-10-19）——式の値を整数に四捨五入する（0.5 は 0 から遠い方へ——表計算ソフトの ROUND と同じ）。
//     利用者:「式の結果に円未満が出ますが、ROUNDという関数を用意しては？」（見積表の単価 `=ROUND(原価*(1+利益率/100))`）。
//     桁の引数は受けない——`,` は数の桁区切り（`1,000`）として読むので、`ROUND(x, 0)` と区別できない。空なら空、エラーはそのまま。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// FormulaResult は、1つのセルの式を計算した結果です。
type FormulaResult struct {
	Formula bool   // セルが式か
	Value   string // 計算した値（空のセルを参照したら ""）
	Err     string // 計算できなかった理由（空でなければエラー）
}

// IsFormula は、セルの文字が式か（`=`・`＝` で始まるか）を返します。
func IsFormula(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "=") || strings.HasPrefix(t, "＝")
}

// EvalTableFormulas は、表の式のセルを全部計算します。headers は見出しの行の文字、rows は見出しより下の行のセルの文字
// （どちらも左から・上から）。返す配列は rows と同じ形で、式でないセルは Formula=false です。
//
// 式は、同じ行のほかのセルを見出しの言葉で、同じ列のほかのセルを行の先頭の言葉で指せます（式のセルも）。
// 見出しや行の名前が重なったら、左・上のもの（先勝ち——表を読むほかの道具と同じ）。
func EvalTableFormulas(headers []string, rows [][]string) [][]FormulaResult {
	out := make([][]FormulaResult, len(rows))
	has := false
	for r, cells := range rows {
		out[r] = make([]FormulaResult, len(cells))
		for c, s := range cells {
			if IsFormula(s) {
				out[r][c].Formula = true
				has = true
			}
		}
	}
	if !has {
		return out
	}
	ev := newTableEval(headers, rows)
	for r, cells := range rows {
		for c := range cells {
			if !out[r][c].Formula {
				continue
			}
			v := ev.cell(r, c)
			switch {
			case v.err != "":
				out[r][c].Err = v.err
			case v.blank:
				out[r][c].Value = ""
			default:
				out[r][c].Value = FormatFormulaNumber(v.num)
			}
		}
	}
	return out
}

// FormulaTableValues は rows の式のセルを計算した値に置き換えた写しを返します（DB に入れる値）。
// 計算できなかったセルは**書いたまま**（式の文字）にします——畳めない値を落とさない、表の写しの約束と同じ。
// 拡張が本文の表を読んで計算するときも、これを通せば式の結果を読めます。
func FormulaTableValues(headers []string, rows [][]string) [][]string {
	res := EvalTableFormulas(headers, rows)
	out := make([][]string, len(rows))
	for r, cells := range rows {
		out[r] = append([]string(nil), cells...)
		for c, x := range res[r] {
			if x.Formula && x.Err == "" {
				out[r][c] = x.Value
			}
		}
	}
	return out
}

// evalRowFormulas は、1行だけの表として式を計算します（ほかの行を指す式は、その行が無いのでエラーになります）。
// 表の全部の行が手元にあるときは EvalTableFormulas を使うこと。
func evalRowFormulas(headers, cells []string) []FormulaResult {
	return EvalTableFormulas(headers, [][]string{cells})[0]
}

// formulaCellValues は、1行だけの表として FormulaTableValues を通します。
func formulaCellValues(headers, cells []string) []string {
	return FormulaTableValues(headers, [][]string{cells})[0]
}

// FormatFormulaNumber は計算した数を文字にします——小数は残す（利用者:「小数も残す」）。ただし浮動小数の
// 端数（0.1+0.2 = 0.30000000000000004）は有効数字12桁で丸めて見せない。桁区切りは付けない（表の値の書き方と同じ）。
func FormatFormulaNumber(f float64) string {
	g, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 12, 64), 64)
	if err != nil {
		g = f
	}
	s := strconv.FormatFloat(g, 'f', -1, 64)
	if s == "-0" {
		s = "0"
	}
	return s
}

// formulaNameKey は列・行の名前を比べる形へ畳みます（NFKC・空白を除く）。
func formulaNameKey(s string) string {
	return strings.Join(strings.Fields(NormalizeText(s)), "")
}

// ── 表の中の計算 ─────────────────────────────────────────────────────────

type formulaValue struct {
	num   float64
	blank bool
	err   string
}

// fPos は表の中のセルの位置です（r は見出しより下の行の番号・c は列の番号）。
type fPos struct{ r, c int }

type tableEval struct {
	rows  [][]string
	col   map[string]int // 見出しの言葉 → 列
	row   map[string]int // 行の先頭の言葉 → 行
	width int            // いちばん長い行の長さ
	state map[fPos]int   // 0 まだ・1 計算中・2 済み
	val   map[fPos]formulaValue
}

func newTableEval(headers []string, rows [][]string) *tableEval {
	e := &tableEval{rows: rows, col: map[string]int{}, row: map[string]int{}, state: map[fPos]int{}, val: map[fPos]formulaValue{}}
	for i, h := range headers {
		k := formulaNameKey(h)
		if _, dup := e.col[k]; !dup && k != "" {
			e.col[k] = i
		}
	}
	e.width = len(headers)
	for r, cells := range rows {
		if len(cells) > e.width {
			e.width = len(cells)
		}
		if len(cells) == 0 || IsFormula(cells[0]) {
			continue
		}
		k := formulaNameKey(cells[0])
		if _, dup := e.row[k]; !dup && k != "" {
			e.row[k] = r
		}
	}
	return e
}

func (e *tableEval) text(p fPos) string {
	if p.r < 0 || p.r >= len(e.rows) || p.c < 0 || p.c >= len(e.rows[p.r]) {
		return ""
	}
	return strings.TrimSpace(e.rows[p.r][p.c])
}

// cell はセルの値です（式なら計算する・循環は止める）。
func (e *tableEval) cell(r, c int) formulaValue {
	p := fPos{r, c}
	switch e.state[p] {
	case 1:
		return formulaValue{err: errFormulaCycle}
	case 2:
		return e.val[p]
	}
	e.state[p] = 1
	var v formulaValue
	text := e.text(p)
	switch {
	case IsFormula(text):
		expr, err := parseFormula(text[len(firstRuneString(text)):])
		if err != nil {
			v = formulaValue{err: err.Error()}
		} else {
			v = expr.eval(e, p)
		}
	case text == "":
		v = formulaValue{blank: true}
	default:
		v = formulaValue{err: "not-number"} // 呼び手が名前を添えて言い直す
		if n, ok := NormalizeValue(ColNumber, text); ok && n != "" {
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				v = formulaValue{num: f}
			}
		}
	}
	e.state[p] = 2
	e.val[p] = v
	return v
}

// ref は、名前で指したセルの値を、呼び手に分かる理由に言い直して返します。
func (e *tableEval) ref(name string, p fPos) formulaValue {
	v := e.cell(p.r, p.c)
	switch {
	case v.err == "not-number":
		return formulaValue{err: "「" + name + "」が数ではありません"}
	case v.err != "" && v.err != errFormulaCycle && IsFormula(e.text(p)):
		return formulaValue{err: "「" + name + "」の式がエラーです"}
	}
	return v
}

// 名前の向き（`列「…」`・`行「…」` で書き分けたもの）。
const (
	axisAny = ""
	axisCol = "列"
	axisRow = "行"
)

// resolve は名前が列か行かを決めます。isCol なら idx は列、そうでなければ行です。
func (e *tableEval) resolve(name, axis string) (idx int, isCol bool, err string) {
	k := formulaNameKey(name)
	ci, okC := e.col[k]
	ri, okR := e.row[k]
	switch axis {
	case axisCol:
		if !okC {
			return 0, false, "「" + name + "」の列がありません"
		}
		return ci, true, ""
	case axisRow:
		if !okR {
			return 0, false, "「" + name + "」の行がありません"
		}
		return ri, false, ""
	}
	switch {
	case okC && okR:
		return 0, false, "「" + name + "」は列にも行にもあります（列「" + name + "」か行「" + name + "」と書いてください）"
	case okC:
		return ci, true, ""
	case okR:
		return ri, false, ""
	}
	return 0, false, "「" + name + "」の列も行もありません"
}

func firstRuneString(s string) string {
	t := strings.TrimSpace(s)
	for _, r := range t {
		return string(r)
	}
	return ""
}

// ── 式の読み手（小さく閉じる——四則と括弧と名前と数と合計だけ） ─────────────────

type fNode interface {
	eval(e *tableEval, at fPos) formulaValue
}

type fNum float64
type fName struct{ name, axis string }
type fNeg struct{ x fNode }
type fBin struct {
	op   rune
	l, r fNode
}

// fRound は `ROUND(式)` です（整数に四捨五入）。
type fRound struct{ x fNode }

func (n fRound) eval(e *tableEval, at fPos) formulaValue {
	v := n.x.eval(e, at)
	if v.err != "" || v.blank {
		return v
	}
	return formulaValue{num: math.Round(v.num)}
}

// fSum は `合計(…)` です。kind は "" （中身なし）・"上"・"one"（名前1つ）・"range"（名前:名前）。
type fSum struct {
	kind string
	a, b fName
}

func (n fNum) eval(*tableEval, fPos) formulaValue { return formulaValue{num: float64(n)} }

func (n fName) eval(e *tableEval, at fPos) formulaValue {
	idx, isCol, err := e.resolve(n.name, n.axis)
	if err != "" {
		return formulaValue{err: err}
	}
	if isCol {
		return e.ref(n.name, fPos{at.r, idx})
	}
	return e.ref(n.name, fPos{idx, at.c})
}

func (n fNeg) eval(e *tableEval, at fPos) formulaValue {
	v := n.x.eval(e, at)
	if v.err != "" || v.blank {
		return v
	}
	return formulaValue{num: -v.num}
}

func (n fBin) eval(e *tableEval, at fPos) formulaValue {
	l := n.l.eval(e, at)
	if l.err != "" {
		return l
	}
	r := n.r.eval(e, at)
	if r.err != "" {
		return r
	}
	if l.blank || r.blank {
		return formulaValue{blank: true}
	}
	var f float64
	switch n.op {
	case '+':
		f = l.num + r.num
	case '-':
		f = l.num - r.num
	case '*':
		f = l.num * r.num
	case '/':
		if r.num == 0 {
			return formulaValue{err: "0 で割っています"}
		}
		f = l.num / r.num
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return formulaValue{err: "数が大きすぎます"}
	}
	return formulaValue{num: f}
}

func (n fSum) eval(e *tableEval, at fPos) formulaValue {
	var cells []fPos
	vertical := true // 縦に足すか（理由の言い方を決める）
	switch n.kind {
	case "", "上":
		end := len(e.rows)
		if n.kind == "上" {
			end = at.r
		}
		for r := 0; r < end; r++ {
			cells = append(cells, fPos{r, at.c})
		}
	case "one":
		idx, isCol, err := e.resolve(n.a.name, n.a.axis)
		if err != "" {
			return formulaValue{err: err}
		}
		if isCol {
			for r := range e.rows {
				cells = append(cells, fPos{r, idx})
			}
		} else {
			vertical = false
			for c := 1; c < e.width; c++ {
				cells = append(cells, fPos{idx, c})
			}
		}
	case "range":
		ia, colA, err := e.resolve(n.a.name, n.a.axis)
		if err != "" {
			return formulaValue{err: err}
		}
		ib, colB, err := e.resolve(n.b.name, n.b.axis)
		if err != "" {
			return formulaValue{err: err}
		}
		if colA != colB {
			return formulaValue{err: "合計の範囲の両端は、行どうしか列どうしにしてください（「" + n.a.name + "」と「" + n.b.name + "」）"}
		}
		if ia > ib {
			ia, ib = ib, ia
		}
		for i := ia; i <= ib; i++ {
			if colA {
				vertical = false
				cells = append(cells, fPos{at.r, i})
			} else {
				cells = append(cells, fPos{i, at.c})
			}
		}
	}
	sum, any := 0.0, false
	for _, p := range cells {
		if p == at {
			continue // 自分は足さない（列の全部・行の全部の合計を、その列・行の中に書けるように）
		}
		v := e.cell(p.r, p.c)
		if v.blank {
			continue
		}
		if v.err != "" {
			name := e.text(fPos{p.r, 0})
			if !vertical {
				name = e.headerName(p.c)
			} else if name == "" {
				name = strconv.Itoa(p.r+1) + "行目"
			}
			if v.err == "not-number" {
				return formulaValue{err: "合計: 「" + name + "」が数ではありません"}
			}
			if v.err == errFormulaCycle {
				return v
			}
			return formulaValue{err: "合計: 「" + name + "」の式がエラーです"}
		}
		sum += v.num
		any = true
	}
	if !any {
		return formulaValue{blank: true}
	}
	if math.IsInf(sum, 0) || math.IsNaN(sum) {
		return formulaValue{err: "数が大きすぎます"}
	}
	return formulaValue{num: sum}
}

// headerName は列の見出しの言葉です（理由の文に使う）。
func (e *tableEval) headerName(c int) string {
	for k, i := range e.col {
		if i == c {
			return k
		}
	}
	return strconv.Itoa(c+1) + "列目"
}

var errFormulaSyntax = errors.New("式が読めません")

// errFormulaCycle は式がたどって自分に戻ったときの理由です（`=価格*2` を価格の列に書いた など）。
const errFormulaCycle = "式が循環しています"

// formulaSumName は合計の関数の名前です（利用者「SUMは必要ないです」——これ1つだけ）。
const formulaSumName = "合計"

// formulaRoundName は四捨五入の関数の名前です（2026-10-19 利用者「ROUNDという関数を用意しては？」）。大文字と小文字は区別しない。
const formulaRoundName = "ROUND"

type fTok struct {
	kind rune // 'n' 数・'i' 名前・演算と括弧と範囲はその字（+ - * / ( ) :）
	num  float64
	name string
	axis string // 名前が `列「…」`・`行「…」` で書き分けられていれば "列"・"行"
}

// tokenizeFormula は式を字句に分けます。
func tokenizeFormula(s string) ([]fTok, error) {
	rs := []rune(s)
	var out []fTok
	isOp := func(r rune) rune {
		switch r {
		case '+', '＋':
			return '+'
		case '-', '－', '−':
			return '-'
		case '*', '＊', '×', '✕':
			return '*'
		case '/', '／', '÷':
			return '/'
		case '(', '（':
			return '('
		case ')', '）':
			return ')'
		case ':', '：':
			return ':'
		}
		return 0
	}
	isSpace := func(r rune) bool { return r == ' ' || r == '　' || r == '\t' || r == '\n' || r == '\r' }
	closer := map[rune]rune{'「': '」', '[': ']', '［': '］', '『': '』'}
	// quoted は i 番目の囲みを読み、中の名前と、閉じの次の位置を返します。
	quoted := func(i int) (string, int, error) {
		end := closer[rs[i]]
		j := i + 1
		for j < len(rs) && rs[j] != end {
			j++
		}
		if j >= len(rs) {
			return "", 0, errors.New("式が読めません（「" + string(rs[i]) + "」が閉じていません）")
		}
		name := strings.TrimSpace(string(rs[i+1 : j]))
		if name == "" {
			return "", 0, errFormulaSyntax
		}
		return name, j + 1, nil
	}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case isSpace(r):
			i++
		case isOp(r) != 0:
			out = append(out, fTok{kind: isOp(r)})
			i++
		case closer[r] != 0:
			name, next, err := quoted(i)
			if err != nil {
				return nil, err
			}
			out = append(out, fTok{kind: 'i', name: name})
			i = next
		default:
			// 名前か数——演算・括弧・空白・囲みの手前まで。全部が数の字なら数（`1.1`・`１，０００`）。
			j := i
			for j < len(rs) && !isSpace(rs[j]) && isOp(rs[j]) == 0 && closer[rs[j]] == 0 {
				j++
			}
			word := string(rs[i:j])
			// `列「単価」`・`行「単価」`——向きを書き分けた名前。
			if (word == axisCol || word == axisRow) && j < len(rs) && closer[rs[j]] != 0 {
				name, next, err := quoted(j)
				if err != nil {
					return nil, err
				}
				out = append(out, fTok{kind: 'i', name: name, axis: word})
				i = next
				continue
			}
			if f, ok := formulaNumber(word); ok {
				out = append(out, fTok{kind: 'n', num: f})
			} else {
				out = append(out, fTok{kind: 'i', name: word})
			}
			i = j
		}
	}
	return out, nil
}

// formulaNumber は字句が数なら値を返します（全角数字・桁区切り・小数点を受ける）。
func formulaNumber(w string) (float64, bool) {
	t := strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '．':
			return '.'
		case r == ',' || r == '，':
			return -1
		}
		return r
	}, w)
	if t == "" {
		return 0, false
	}
	for _, r := range t {
		if (r < '0' || r > '9') && r != '.' {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	return f, err == nil
}

// parseFormula は `=` の後ろを木にします（expr := term (±term)*・term := factor (×÷factor)*・
// factor := ±factor | 数 | 名前 | 合計(…) | ROUND(expr) | (expr)）。
//
// ⚠ 長さと入れ子に上限がある——上限が無いと、「=((((…」を数百万字書いた1つのセルで、読むたびに（保存・表示・DB の作り直し）
// スタックが溢れてサーバーが落ちる（Go では拾えない・2026-10-22 の見直し）。
func parseFormula(s string) (fNode, error) {
	if len(s) > maxFormulaBytes {
		return nil, fmt.Errorf("式が長すぎます（%d バイトまで）", maxFormulaBytes)
	}
	toks, err := tokenizeFormula(s)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, errors.New("式が空です")
	}
	p := &fParser{toks: toks}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.toks) {
		t := p.toks[p.i]
		if t.kind == 'i' || t.kind == 'n' || t.kind == '(' {
			return nil, errors.New("式が読めません（演算の記号が抜けています——名前に括弧や空白があるなら「」で囲む）")
		}
		return nil, errFormulaSyntax
	}
	return n, nil
}

// 式の長さと入れ子の上限です（表のセルの式には十分）。
const (
	maxFormulaBytes = 4000
	maxFormulaDepth = 100
)

type fParser struct {
	toks  []fTok
	i     int
	depth int // factor の入れ子の深さ
}

func (p *fParser) peek() rune {
	if p.i < len(p.toks) {
		return p.toks[p.i].kind
	}
	return 0
}

func (p *fParser) peekAt(k int) rune {
	if p.i+k < len(p.toks) {
		return p.toks[p.i+k].kind
	}
	return 0
}

func (p *fParser) expr() (fNode, error) {
	l, err := p.term()
	if err != nil {
		return nil, err
	}
	for p.peek() == '+' || p.peek() == '-' {
		op := p.toks[p.i].kind
		p.i++
		r, err := p.term()
		if err != nil {
			return nil, err
		}
		l = fBin{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *fParser) term() (fNode, error) {
	l, err := p.factor()
	if err != nil {
		return nil, err
	}
	for p.peek() == '*' || p.peek() == '/' {
		op := p.toks[p.i].kind
		p.i++
		r, err := p.factor()
		if err != nil {
			return nil, err
		}
		l = fBin{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *fParser) factor() (fNode, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxFormulaDepth {
		return nil, errors.New("式の入れ子が深すぎます")
	}
	switch p.peek() {
	case '-':
		p.i++
		x, err := p.factor()
		if err != nil {
			return nil, err
		}
		return fNeg{x: x}, nil
	case '+':
		p.i++
		return p.factor()
	case 'n':
		t := p.toks[p.i]
		p.i++
		return fNum(t.num), nil
	case 'i':
		t := p.toks[p.i]
		if t.axis == "" && t.name == formulaSumName && p.peekAt(1) == '(' {
			return p.sum()
		}
		if t.axis == "" && strings.EqualFold(t.name, formulaRoundName) && p.peekAt(1) == '(' {
			p.i += 2 // ROUND (
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			if p.peek() != ')' {
				return nil, errors.New("式が読めません（ROUND の中は式1つです。括弧が閉じていません）")
			}
			p.i++
			return fRound{x: x}, nil
		}
		p.i++
		return fName{name: t.name, axis: t.axis}, nil
	case '(':
		p.i++
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, errors.New("式が読めません（括弧が閉じていません）")
		}
		p.i++
		return x, nil
	}
	return nil, errFormulaSyntax
}

// sum は `合計(…)` を読みます（p.i は「合計」の上）。
func (p *fParser) sum() (fNode, error) {
	p.i += 2 // 合計 (
	bad := errors.New("式が読めません（合計の中は、空・「上」・名前・名前:名前 のどれかです）")
	name := func() (fName, bool) {
		if p.peek() != 'i' {
			return fName{}, false
		}
		t := p.toks[p.i]
		p.i++
		return fName{name: t.name, axis: t.axis}, true
	}
	var n fSum
	switch {
	case p.peek() == ')':
		n.kind = ""
	case p.peek() == 'i' && p.toks[p.i].axis == "" && p.toks[p.i].name == "上" && p.peekAt(1) == ')':
		p.i++
		n.kind = "上"
	default:
		a, ok := name()
		if !ok {
			return nil, bad
		}
		n.kind, n.a = "one", a
		if p.peek() == ':' {
			p.i++
			b, ok := name()
			if !ok {
				return nil, bad
			}
			n.kind, n.b = "range", b
		}
	}
	if p.peek() != ')' {
		return nil, bad
	}
	p.i++
	return n, nil
}

// ── 閲覧の表示へ ─────────────────────────────────────────────────────────

// formulaCellRe は本文に式のセルがありそうかの早道です（無ければパースしない）。
// ⚠ 頭の空白は、セルを読むときの strings.TrimSpace と同じだけ許す——全角の空白・NBSP（`&nbsp;` も）。正規表現の `\s` は
// ASCII の空白だけなので、それまでは「　=個数*単価」のセルが早道で落ちて、計算されなかった（【考察】コードの矛盾 §2.3・2026-10-29）。
var formulaCellRe = regexp.MustCompile(`<td[^>]*>(?:[\s\x{00A0}\x{3000}]|&nbsp;|&#160;|<[^>]+>)*(?:=|＝)`)

// renderFormulaCells は本文の表の式のセルに、計算した結果を属性で添えます——`data-w-value`（値・空のセルを
// 参照したら空）か `data-w-error`（理由）。画面は閲覧モードでこれを見せ、書ける形では式のまま（app.js
// 「表のセルの式」）。⚠ **属性は保存されない**——`td` に許す属性は colspan・rowspan・headers だけで、画面の
// 書き出しもサーバーのサニタイズも落とす。本文に入るのは式だけ。
func renderFormulaCells(bodyHTML string) string {
	if !formulaCellRe.MatchString(bodyHTML) {
		return bodyHTML
	}
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}
	for _, n := range nodes {
		WalkElements(n, func(t *html.Node) {
			if t.Data != "table" || inVocabChrome(t) {
				return
			}
			rows := tableRows(t)
			if len(rows) < 2 {
				return
			}
			var headers []string
			for _, c := range rowCells(rows[0]) {
				headers = append(headers, strings.TrimSpace(TextWithBreaks(c)))
			}
			cellsOf := make([][]*html.Node, len(rows)-1)
			texts := make([][]string, len(rows)-1)
			for r, tr := range rows[1:] {
				cellsOf[r] = rowCells(tr)
				texts[r] = make([]string, len(cellsOf[r]))
				for i, c := range cellsOf[r] {
					texts[r][i] = strings.TrimSpace(TextWithBreaks(c))
				}
			}
			for r, res := range EvalTableFormulas(headers, texts) {
				for i, x := range res {
					if !x.Formula || cellsOf[r][i].Data != "td" {
						continue
					}
					if x.Err != "" {
						setAttr(cellsOf[r][i], "data-w-error", x.Err)
					} else {
						setAttr(cellsOf[r][i], "data-w-value", x.Value)
					}
				}
			}
		})
	}
	return htmldoc.Render(nodes)
}
