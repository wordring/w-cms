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
// ⚠ 関数（合計・丸めなど）は持たない——表計算ソフトの再発明にしない（§4.1）。要るものが出てから足す。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// FormulaResult は、行の1つのセルの式を計算した結果です。
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

// EvalRowFormulas は、表の1行の式のセルを計算します。headers は見出しの行の文字、cells はその行のセルの文字
// （どちらも左から）。返す配列は cells と同じ長さで、式でないセルは Formula=false です。
//
// 式は同じ行のほかのセル（式のセルも）を見出しの言葉で指します。見出しが重なったら左のもの（先勝ち——
// 表を読むほかの道具と同じ）。
func EvalRowFormulas(headers, cells []string) []FormulaResult {
	out := make([]FormulaResult, len(cells))
	has := false
	for i, c := range cells {
		if IsFormula(c) {
			out[i].Formula = true
			has = true
		}
	}
	if !has {
		return out
	}
	col := map[string]int{}
	for i, h := range headers {
		k := formulaNameKey(h)
		if _, dup := col[k]; !dup && k != "" {
			col[k] = i
		}
	}
	ev := &rowEval{cells: cells, col: col, state: map[int]int{}, val: map[int]formulaValue{}}
	for i := range cells {
		if !out[i].Formula {
			continue
		}
		v := ev.cell(i)
		switch {
		case v.err != "":
			out[i].Err = v.err
		case v.blank:
			out[i].Value = ""
		default:
			out[i].Value = FormatFormulaNumber(v.num)
		}
	}
	return out
}

// FormulaCellValues は cells の式のセルを計算した値に置き換えた写しを返します（DB に入れる値）。
// 計算できなかったセルは**書いたまま**（式の文字）にします——畳めない値を落とさない、表の写しの約束と同じ。
func FormulaCellValues(headers, cells []string) []string {
	res := EvalRowFormulas(headers, cells)
	out := append([]string(nil), cells...)
	for i, r := range res {
		if r.Formula && r.Err == "" {
			out[i] = r.Value
		}
	}
	return out
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

// formulaNameKey は列の名前を比べる形へ畳みます（NFKC・空白を除く）。
func formulaNameKey(s string) string {
	return strings.Join(strings.Fields(NormalizeText(s)), "")
}

// ── 行の中の計算 ─────────────────────────────────────────────────────────

type formulaValue struct {
	num   float64
	blank bool
	err   string
}

type rowEval struct {
	cells []string
	col   map[string]int
	state map[int]int // 0 まだ・1 計算中・2 済み
	val   map[int]formulaValue
}

// cell は i 番目のセルの値です（式なら計算する・循環は止める）。
func (e *rowEval) cell(i int) formulaValue {
	switch e.state[i] {
	case 1:
		return formulaValue{err: errFormulaCycle}
	case 2:
		return e.val[i]
	}
	e.state[i] = 1
	var v formulaValue
	text := ""
	if i < len(e.cells) {
		text = strings.TrimSpace(e.cells[i])
	}
	switch {
	case IsFormula(text):
		expr, err := parseFormula(strings.TrimSpace(text)[len(firstRuneString(text)):])
		if err != nil {
			v = formulaValue{err: err.Error()}
		} else {
			v = expr.eval(e)
		}
	case text == "":
		v = formulaValue{blank: true}
	default:
		v = formulaValue{err: "not-number"} // 呼び手が列の名前を添えて言い直す
		if n, ok := NormalizeValue(ColNumber, text); ok && n != "" {
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				v = formulaValue{num: f}
			}
		}
	}
	e.state[i] = 2
	e.val[i] = v
	return v
}

func firstRuneString(s string) string {
	t := strings.TrimSpace(s)
	for _, r := range t {
		return string(r)
	}
	return ""
}

// ── 式の読み手（小さく閉じる——四則と括弧と名前と数だけ） ─────────────────────

type fNode interface{ eval(e *rowEval) formulaValue }

type fNum float64
type fName string
type fNeg struct{ x fNode }
type fBin struct {
	op   rune
	l, r fNode
}

func (n fNum) eval(*rowEval) formulaValue { return formulaValue{num: float64(n)} }

func (n fName) eval(e *rowEval) formulaValue {
	i, ok := e.col[formulaNameKey(string(n))]
	if !ok {
		return formulaValue{err: "「" + string(n) + "」の列がありません"}
	}
	v := e.cell(i)
	switch {
	case v.err == "not-number":
		return formulaValue{err: "「" + string(n) + "」が数ではありません"}
	case v.err != "" && v.err != errFormulaCycle && i < len(e.cells) && IsFormula(e.cells[i]):
		return formulaValue{err: "「" + string(n) + "」の式がエラーです"}
	}
	return v
}

func (n fNeg) eval(e *rowEval) formulaValue {
	v := n.x.eval(e)
	if v.err != "" || v.blank {
		return v
	}
	return formulaValue{num: -v.num}
}

func (n fBin) eval(e *rowEval) formulaValue {
	l := n.l.eval(e)
	if l.err != "" {
		return l
	}
	r := n.r.eval(e)
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

var errFormulaSyntax = errors.New("式が読めません")

// errFormulaCycle は式がたどって自分に戻ったときの理由です（`=価格*2` を価格の列に書いた など）。
const errFormulaCycle = "式が循環しています"

type fTok struct {
	kind rune // 'n' 数・'i' 名前・演算と括弧はその字（+ - * / ( )）
	num  float64
	name string
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
		}
		return 0
	}
	isSpace := func(r rune) bool { return r == ' ' || r == '　' || r == '\t' || r == '\n' || r == '\r' }
	closer := map[rune]rune{'「': '」', '[': ']', '［': '］', '『': '』'}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case isSpace(r):
			i++
		case isOp(r) != 0:
			out = append(out, fTok{kind: isOp(r)})
			i++
		case closer[r] != 0:
			end := closer[r]
			j := i + 1
			for j < len(rs) && rs[j] != end {
				j++
			}
			if j >= len(rs) {
				return nil, errors.New("式が読めません（「" + string(r) + "」が閉じていません）")
			}
			name := strings.TrimSpace(string(rs[i+1 : j]))
			if name == "" {
				return nil, errFormulaSyntax
			}
			out = append(out, fTok{kind: 'i', name: name})
			i = j + 1
		default:
			// 名前か数——演算・括弧・空白・囲みの手前まで。全部が数の字なら数（`1.1`・`１，０００`）。
			j := i
			for j < len(rs) && !isSpace(rs[j]) && isOp(rs[j]) == 0 && closer[rs[j]] == 0 {
				j++
			}
			word := string(rs[i:j])
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
// factor := ±factor | 数 | 名前 | (expr)）。
func parseFormula(s string) (fNode, error) {
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

type fParser struct {
	toks []fTok
	i    int
}

func (p *fParser) peek() rune {
	if p.i < len(p.toks) {
		return p.toks[p.i].kind
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
		p.i++
		return fName(t.name), nil
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

// ── 閲覧の表示へ ─────────────────────────────────────────────────────────

// formulaCellRe は本文に式のセルがありそうかの早道です（無ければパースしない）。
var formulaCellRe = regexp.MustCompile(`<td[^>]*>\s*(?:<[^>]+>\s*)*(?:=|＝)`)

// renderFormulaCells は本文の表の式のセルに、計算した結果を属性で添えます——`data-w-value`（値・空のセルを
// 参照したら空）か `data-w-error`（理由）。画面は閲覧モードでこれを見せ、編集モードでは式のまま（app.js
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
				headers = append(headers, strings.TrimSpace(cellText(c)))
			}
			for _, tr := range rows[1:] {
				cells := rowCells(tr)
				texts := make([]string, len(cells))
				for i, c := range cells {
					texts[i] = strings.TrimSpace(cellText(c))
				}
				for i, r := range EvalRowFormulas(headers, texts) {
					if !r.Formula || cells[i].Data != "td" {
						continue
					}
					if r.Err != "" {
						setAttr(cells[i], "data-w-error", r.Err)
					} else {
						setAttr(cells[i], "data-w-value", r.Value)
					}
				}
			}
		})
	}
	return htmldoc.Render(nodes)
}
