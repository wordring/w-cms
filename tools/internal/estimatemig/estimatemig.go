// Package estimatemig は、見積計算表を 2026-10-15 の形へ移す変換です（見出し「見積もり計算」・ロットごとの見出し・
// 見積計算表〔工程｜価格｜備考〕・見積表〔ロット｜表面処理｜原価｜利益率｜単価｜備考〕——ext/toho/estimate.go・【要求】見積 §4a）。
//
// 職場の235ページを移した道具（デスクトップの w-cms道具見積の形_20261015）の変換を、ワンノートの製造（tools/onenote/build）
// でも使えるように寄せたものです。本文の文字列を受けて、移した本文を返すだけ（サーバーには触らない）。
//
// 移し方（節 <section><h2>見積計算表</h2>…</section> ごと）:
//   - 節の中の見積計算表が全部「移せる形」なら、節を外して <h2>見積もり計算</h2> と、表ごとに
//     <h3>ロットN</h3>・見積計算表・見積表にする。節のほかの中身（段落・ファイル表示）はその場に残す。
//     すぐ前が移した節なら見出し「見積もり計算」は重ねない。
//   - 移せる形: 工程｜数｜単位｜（備考）の縦の表と、工程｜ロットN…｜備考 の列にロットを並べた表（列ごとに1組）。
//     それ以外の表がある節は、節ごと触らない（Skipped に出す）。
//   - ロット・単価・弊社利益・総計の行は見積表へ: ロット→ロット、単価（空なら総計、それも空なら価格の合計）→原価、
//     弊社利益→利益率（無ければ 10）、単価＝原価＋原価×利益率（円未満四捨五入）。単価の行の備考→表面処理（「、」の後ろは備考）。
//     原価を書いた見積表の備考に「移行中」。
package estimatemig

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

const defaultRate = 10.0

func text(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.TrimSpace(b.String())
}

func elem(tag string, kids ...*html.Node) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag}
	for _, k := range kids {
		n.AppendChild(k)
	}
	return n
}

func txt(s string) *html.Node { return &html.Node{Type: html.TextNode, Data: s} }

func clone(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace}
	for _, a := range n.Attr {
		if a.Key != "data-id" {
			c.Attr = append(c.Attr, a)
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.AppendChild(clone(ch))
	}
	return c
}

// cellOf は src の中身を写したセル（src が nil なら空のセル）。
func cellOf(tag string, src *html.Node) *html.Node {
	td := elem(tag)
	if src != nil {
		for ch := src.FirstChild; ch != nil; ch = ch.NextSibling {
			td.AppendChild(clone(ch))
		}
	}
	return td
}

func textCell(s string) *html.Node {
	td := elem("td")
	if s != "" {
		td.AppendChild(txt(s))
	}
	return td
}

func isTable(n *html.Node, caption string) bool {
	if n.Type != html.ElementNode || n.Data != "table" {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "caption" && text(c) == caption {
			return true
		}
	}
	return false
}

// trsOf は表の行と、行ごとのセル（td/th の要素）を返します。
func trsOf(t *html.Node) [][]*html.Node {
	var out [][]*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			var r []*html.Node
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					r = append(r, c)
				}
			}
			out = append(out, r)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(t)
	return out
}

var lotHeadRe = regexp.MustCompile(`^ロット\s*([0-9０-９]+)\s*(.*)$`)
var dotThousandsRe = regexp.MustCompile(`^\d{1,3}(\.\d{3})+$`)

// num は表の数を読みます（円・カンマ・全角・「28.000」の点の桁区切り）。
func num(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '．':
			return '.'
		case r == ',' || r == '，' || r == '円' || r == '%' || r == '％' || r == ' ':
			return -1
		}
		return r
	}, s)
	if dotThousandsRe.MatchString(s) {
		s = strings.ReplaceAll(s, ".", "")
	}
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// lotBlock は移したあとの1組の材料です。
type lotBlock struct {
	lot, surface, note string
	steps              [][3]*html.Node // 工程・価格・備考のセル（元のセルの中身を写す）
	unit, total        string          // 元の単価・総計の値
	rate               string
	rateSet            bool
}

func (b *lotBlock) build(report *[]string) []*html.Node {
	// 見出し
	h := "ロット" + b.lot
	if b.lot == "" {
		h = "ロット（未定）"
	}
	if b.surface != "" {
		h += "・" + b.surface
	}
	h3 := elem("h3", txt(h))
	// 見積計算表
	tb := elem("tbody", elem("tr", elem("th", txt("工程")), elem("th", txt("価格")), elem("th", txt("備考"))))
	sum, any, bad := 0.0, false, false
	for _, s := range b.steps {
		tb.AppendChild(elem("tr", cellOf("td", s[0]), cellOf("td", s[1]), cellOf("td", s[2])))
		if s[1] != nil && text(s[1]) != "" {
			if v, ok := num(text(s[1])); ok {
				sum += v
				any = true
			} else {
				bad = true
			}
		}
	}
	calc := elem("table", elem("caption", txt("見積計算表")), tb)
	// 見積表
	cost, costOK := 0.0, false
	switch {
	case b.unit != "":
		cost, costOK = num(b.unit)
		if !costOK {
			*report = append(*report, "単価が数でない: "+b.unit)
		}
	case b.total != "":
		cost, costOK = num(b.total)
	case any && !bad:
		cost, costOK = sum, true
	}
	rate := defaultRate
	if b.rateSet {
		if v, ok := num(b.rate); ok {
			rate = v
		}
	}
	rec := []string{b.lot, b.surface, "", "", "", b.note}
	if costOK {
		rec[2] = fmtNum(cost)
		rec[3] = fmtNum(rate)
		rec[4] = strconv.Itoa(int(math.Round(cost)) + int(math.Round(cost*rate/100)))
		rec[5] = strings.TrimSpace(strings.TrimPrefix(b.note+"　移行中", "　"))
	}
	head := elem("tr")
	for _, c := range []string{"ロット", "表面処理", "原価", "利益率", "単価", "備考"} {
		head.AppendChild(elem("th", txt(c)))
	}
	row := elem("tr")
	for _, c := range rec {
		row.AppendChild(textCell(c))
	}
	record := elem("table", elem("caption", txt("見積表")), elem("tbody", head, row))
	return []*html.Node{h3, calc, record}
}

// splitSurface は単価の行の備考を、表面処理と備考に分けます（「塗装無し、試作価格」→ 塗装無し／試作価格）。
func splitSurface(s string) (surface, note string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	parts := strings.SplitN(s, "、", 2)
	surfaceWord := false
	for _, w := range []string{"塗装", "メッキ", "鍍金", "生地", "焼付", "黒染", "アルマイト"} {
		if strings.Contains(parts[0], w) {
			surfaceWord = true
		}
	}
	if surfaceWord {
		if len(parts) == 2 {
			return parts[0], strings.TrimSpace(parts[1])
		}
		return parts[0], ""
	}
	return "", s
}

// convert は見積計算表1枚を組にします（移せない形なら nil）。
func convert(t *html.Node, report *[]string) []*html.Node {
	rows := trsOf(t)
	if len(rows) == 0 {
		return nil
	}
	head := make([]string, len(rows[0]))
	for i, c := range rows[0] {
		head[i] = text(c)
	}
	at := func(r []*html.Node, i int) *html.Node {
		if i >= 0 && i < len(r) {
			return r[i]
		}
		return nil
	}
	tx := func(r []*html.Node, i int) string {
		if c := at(r, i); c != nil {
			return text(c)
		}
		return ""
	}
	// 縦の表: 工程｜数｜単位｜（備考）
	if len(head) >= 3 && head[0] == "工程" && head[1] == "数" && head[2] == "単位" {
		b := &lotBlock{}
		noteCol := -1
		if len(head) >= 4 {
			noteCol = 3
		}
		for _, r := range rows[1:] {
			name := tx(r, 0)
			switch name {
			case "ロット":
				b.lot = tx(r, 1)
				if u := tx(r, 2); u != "" && u != "個" && b.lot != "" {
					b.note = strings.TrimSpace(b.note + " ロットの単位: " + u)
				}
			case "単価":
				b.unit = tx(r, 1)
				s, n := splitSurface(tx(r, noteCol))
				b.surface = s
				if n != "" {
					b.note = strings.TrimSpace(b.note + " " + n)
				}
			case "総計":
				b.total = tx(r, 1)
				if n := tx(r, noteCol); n != "" {
					s, n2 := splitSurface(n)
					if b.surface == "" {
						b.surface = s
					}
					if n2 != "" {
						b.note = strings.TrimSpace(b.note + " " + n2)
					}
				}
			case "弊社利益":
				b.rate, b.rateSet = tx(r, 1), true
			default:
				var note *html.Node
				if noteCol >= 0 {
					note = at(r, noteCol)
				}
				if name == "" && tx(r, 1) == "" && (note == nil || text(note) == "") {
					continue // 空の行
				}
				if u := tx(r, 2); u != "" && u != "円" {
					*report = append(*report, "円でない単位の行: "+name+" "+tx(r, 1)+" "+u)
				}
				b.steps = append(b.steps, [3]*html.Node{at(r, 0), at(r, 1), note})
			}
		}
		if _, ok := num(b.lot); !ok && b.lot != "" {
			*report = append(*report, "ロットが数でない: "+b.lot)
		}
		return b.build(report)
	}
	// ロットを列に並べた表: 工程｜ロットN…｜備考
	if len(head) >= 3 && head[0] == "工程" && head[len(head)-1] == "備考" {
		var cols []int
		var blocks []*lotBlock
		for i := 1; i < len(head)-1; i++ {
			m := lotHeadRe.FindStringSubmatch(head[i])
			if m == nil {
				return nil
			}
			lot := strings.Map(func(r rune) rune {
				if r >= '０' && r <= '９' {
					return r - '０' + '0'
				}
				return r
			}, m[1])
			cols = append(cols, i)
			blocks = append(blocks, &lotBlock{lot: lot, surface: strings.TrimSpace(m[2])})
		}
		noteCol := len(head) - 1
		for _, r := range rows[1:] {
			name := tx(r, 0)
			for k, ci := range cols {
				b := blocks[k]
				switch name {
				case "ロット":
				case "単価", "総計":
					v := tx(r, ci)
					if name == "単価" {
						b.unit = v
					} else {
						b.total = v
					}
					if n := tx(r, noteCol); n != "" {
						s, n2 := splitSurface(n)
						if b.surface == "" {
							b.surface = s
						}
						if n2 != "" && !strings.Contains(b.note, n2) {
							b.note = strings.TrimSpace(b.note + " " + n2)
						}
					}
				case "弊社利益":
					b.rate, b.rateSet = tx(r, ci), true
				default:
					if name == "" && tx(r, ci) == "" {
						continue
					}
					b.steps = append(b.steps, [3]*html.Node{at(r, 0), at(r, ci), at(r, noteCol)})
				}
			}
		}
		var out []*html.Node
		for _, b := range blocks {
			out = append(out, b.build(report)...)
		}
		return out
	}
	return nil
}

func isCalcSection(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "section" {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c.Data == "h2" && text(c) == "見積計算表"
		}
	}
	return false
}

// Result は1ページを移した結果です。
type Result struct {
	Body    string   // 移した本文（Changed でなければ元のまま）
	Changed bool     // 移した節があった
	Tables  int      // 見た見積計算表の数
	Blocks  int      // 作った組（見出し・見積計算表・見積表）の数
	Skipped []string // 触らなかった節の理由
	Notes   []string // 気づいたこと（ロットが数でない など）
}

// ConvertPage は本文の見積計算表の節を移します。
func ConvertPage(body string) (Result, error) {
	res := Result{Body: body}
	if !strings.Contains(body, "<caption>見積計算表</caption>") {
		return res, nil
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return res, err
	}
	var next []*html.Node
	prevConverted := false
	for _, n := range nodes {
		if !isCalcSection(n) {
			next = append(next, n)
			if !(n.Type == html.TextNode && strings.TrimSpace(n.Data) == "") {
				prevConverted = false
			}
			continue
		}
		// 節の中の見積計算表が全部移せるか（表は節の直下だけ）。
		var parts []*html.Node
		ok := true
		var why, rep []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if isTable(c, "見積計算表") {
				res.Tables++
				got := convert(c, &rep)
				if got == nil {
					ok = false
					hs := []string{}
					if rs := trsOf(c); len(rs) > 0 {
						for _, h := range rs[0] {
							hs = append(hs, text(h))
						}
					}
					why = append(why, "移せない形の表: "+strings.Join(hs, "|"))
					continue
				}
				res.Blocks += len(got) / 3
				parts = append(parts, got...)
				continue
			}
			if c.Type == html.ElementNode && c.Data == "h2" && text(c) == "見積計算表" {
				continue
			}
			// 入れ子の見積計算表（節の直下でない）は移せない。
			if c.Type == html.ElementNode && strings.Contains(htmldoc.Render([]*html.Node{c}), "<caption>見積計算表</caption>") {
				ok = false
				why = append(why, "節の中に入れ子の見積計算表")
				continue
			}
			parts = append(parts, clone(c))
		}
		if !ok {
			res.Skipped = append(res.Skipped, strings.Join(why, " ／ "))
			next = append(next, n)
			prevConverted = false
			continue
		}
		if !prevConverted {
			next = append(next, elem("h2", txt("見積もり計算")))
		}
		next = append(next, parts...)
		res.Notes = append(res.Notes, rep...)
		res.Changed, prevConverted = true, true
	}
	if res.Changed {
		res.Body = htmldoc.Render(next)
	}
	return res, nil
}
