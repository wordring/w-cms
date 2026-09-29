package main

// ─────────────────────────────────────────────────────────────────────────
// 表の行き先と取り消し線（2026-09-29）
//
// 利用者:「○○は、材料を変えたので最初の表が古くなったようです。
// もともとは文字を取り消し線で打ち消していました」「○○は、下の表が購入部品のようです」
// 「○○は下の二個の表が外注加工のようです」「○○は下の表が購入部品のようです」。
//
// それまでは ■材料 などの節の**最初の表だけ**をテンプレートの表へ入れ、2つ目からはキャプションの無い表で
// 残していた——古い読み方（節の見出しで表の種類を決める）では、それも材料として読まれていた。
// ワンノートの表には、同じ節の下に事情の違う表が並んでいる:
//
//   - **取り消し線の行**——使わなくなった行（区分を「廃版」にする・手配に数えない・行は残す）
//   - **見出しの無い2列の表**（品名｜〇個）——購入部品（ウェルドナット・ピン・Eリング…）
//   - **同じ見出しの表**——同じ表の続き（取り消した表の代わりの新しい表など）
//   - それ以外——人が決める（`製造の設定.json` の「表の行き先」・w-cms のページ番号 → ■節 → 表ごとの行き先）
//
// 行はページ全体で行き先の表ごとに集めてから、最後に1回で埋める（テンプレートの表を埋める口は、
// 2回呼ぶと前の行を消して入れ直すため）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	stdhtml "html"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

const (
	obsoleteColumn = "区分" // 構成部品の表の列（現行／廃版）
	obsoleteValue  = "廃版"
	// keepPlain は「表の行き先」でテンプレートの表へ入れず、キャプションの無い表のまま残す印です。
	keepPlain = "表のまま"
)

// strikeCount は T の中身（HTML の断片）の見える文字を、取り消し線の中と外に分けて数えます
// （ワンノートは `<span style='text-decoration:line-through'>` で書く）。
func strikeCount(raw string) (in, out int) {
	nodes, err := htmldoc.ParseFragment(raw)
	if err != nil {
		return 0, 0
	}
	var walk func(n *html.Node, s bool)
	walk = func(n *html.Node, s bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "s", "strike", "del":
				s = true
			}
			for _, a := range n.Attr {
				if a.Key == "style" && strings.Contains(strings.ReplaceAll(a.Val, " ", ""), "line-through") {
					s = true
				}
			}
		}
		if n.Type == html.TextNode {
			k := len([]rune(strings.Join(strings.Fields(strings.ReplaceAll(n.Data, "&nbsp;", " ")), "")))
			if s {
				in += k
			} else {
				out += k
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, s)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}
	return in, out
}

// cellStruck は、セルの見える文字が**全部**取り消し線の中かです（一部だけなら打ち消していない）。
func cellStruck(n *xnode) bool {
	in, out := 0, 0
	var walk func(x *xnode)
	walk = func(x *xnode) {
		for i := range x.Nodes {
			c := &x.Nodes[i]
			if c.XMLName.Local == "T" {
				a, b := strikeCount(c.Text)
				in, out = in+a, out+b
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return in > 0 && out == 0
}

// rowStruck は、行の文字のあるセルが全部打ち消されているかです。
func rowStruck(cells []string, struck []bool) bool {
	any := false
	for i, t := range cells {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if i >= len(struck) || !struck[i] {
			return false
		}
		any = true
	}
	return any
}

// markObsolete は取り消し線の行の「区分」を「廃版」にします（区分の列が無ければ足す）。行は消しません——
// w-cms では、使わなくなった構成部品は行を残して区分を「廃版」にする（手配に数えない・社内コードの指し先は残る）。
func markObsolete(rows [][]string, struck [][]bool) ([][]string, int) {
	if len(rows) < 2 {
		return rows, 0
	}
	out := make([][]string, len(rows))
	copy(out, rows)
	idx := -1
	for i, h := range rows[0] {
		if strings.TrimSpace(h) == obsoleteColumn {
			idx = i
		}
	}
	n := 0
	for r := 1; r < len(rows); r++ {
		var s []bool
		if r < len(struck) {
			s = struck[r]
		}
		if !rowStruck(rows[r], s) {
			continue
		}
		if idx < 0 {
			idx = len(rows[0])
			out[0] = append(append([]string{}, rows[0]...), obsoleteColumn)
		}
		row := append([]string{}, rows[r]...)
		for len(row) <= idx {
			row = append(row, "")
		}
		row[idx] = obsoleteValue
		out[r] = row
		n++
	}
	return out, n
}

var countRe = regexp.MustCompile(`^([0-9]+)\s*(個|本|枚|セット|組|ヶ|ケ|式|コ)?$`)

// partsList は、見出しの無い2列の表（品名｜〇個）を購入部品の行にします——ワンノートの ■材料 などの下に、
// 買う部品をこの形で書いていた（利用者:「○○は、下の表が購入部品のようです」）。
func partsList(rows [][]string) ([]string, [][]string, bool) {
	var out [][]string
	for _, r := range rows {
		var cells []string
		for _, v := range r {
			if v = strings.TrimSpace(v); v != "" {
				cells = append(cells, v)
			}
		}
		if len(cells) == 0 {
			continue
		}
		if len(r) != 2 || len(cells) != 2 {
			return nil, nil, false
		}
		m := countRe.FindStringSubmatch(cms.NormalizeNameForIngest(cells[1]))
		if m == nil {
			return nil, nil, false
		}
		out = append(out, []string{cms.NormalizeNameForIngest(cells[0]), m[1]})
	}
	if len(out) == 0 {
		return nil, nil, false
	}
	return []string{"品名", "個数"}, out, true
}

// sharesColumns は2つの表の見出しが min 個以上重なるかです（同じ表の続きと見なす）。
func sharesColumns(a, b []string, min int) bool {
	in := map[string]bool{}
	for _, c := range a {
		if c = strings.TrimSpace(c); c != "" {
			in[c] = true
		}
	}
	n := 0
	for _, c := range b {
		if in[strings.TrimSpace(c)] {
			n++
		}
	}
	return n >= min
}

// tableByCaption は行き先の表（キャプション）の読み替えを引きます。
func tableByCaption(caption string) (struct {
	Caption string
	Rename  map[string]string
	Number  bool
}, bool) {
	for _, m := range tableMap {
		if m.Caption == caption {
			return m, true
		}
	}
	return tableMap[""], false
}

// forOutsourcing は、外注加工へ入れる表に「加工内容」の列が無ければ「備考」を加工内容に読み替えます
// （利用者:「○○は下の二個の表が外注加工のようです」——材料の形の表で、備考に「レーザー加工」「溶断」）。
func forOutsourcing(rows [][]string) [][]string {
	if len(rows) == 0 {
		return rows
	}
	for _, h := range rows[0] {
		if h = strings.TrimSpace(h); h == "加工内容" || h == "加工" {
			return rows
		}
	}
	out := make([][]string, len(rows))
	copy(out, rows)
	head := append([]string{}, rows[0]...)
	for i, h := range head {
		if strings.TrimSpace(h) == "備考" {
			head[i] = "加工内容"
		}
	}
	out[0] = head
	return out
}

// tableFill はページ全体で1つの表へ入れる行です（列は入れた表の見出しの和集合）。
type tableFill struct {
	cols   []string
	rows   [][]string
	number bool // 番号（1から）を振り直す
}

func (f *tableFill) add(cols []string, rows [][]string) {
	at := map[string]int{}
	for i, c := range f.cols {
		at[c] = i
	}
	for _, c := range cols {
		if _, ok := at[c]; !ok {
			at[c] = len(f.cols)
			f.cols = append(f.cols, c)
		}
	}
	for _, r := range rows {
		out := make([]string, len(f.cols))
		for ci, c := range cols {
			if ci < len(r) && out[at[c]] == "" {
				out[at[c]] = r[ci]
			}
		}
		f.rows = append(f.rows, out)
	}
}

// tableFills は行き先の表（キャプション）ごとの行です。
type tableFills struct {
	order []string
	by    map[string]*tableFill
}

func (t *tableFills) add(caption string, cols []string, rows [][]string, number bool) {
	if t.by == nil {
		t.by = map[string]*tableFill{}
	}
	f, ok := t.by[caption]
	if !ok {
		f = &tableFill{number: number}
		t.by[caption] = f
		t.order = append(t.order, caption)
	}
	f.add(cols, rows)
}

// fill はテンプレートの表を埋めます（番号の列があれば1から振り直す——表をまたいで集めたため）。
func (t *tableFills) fill(d *cms.PageDraft, note *pageNote) {
	for _, caption := range t.order {
		f := t.by[caption]
		if f.number {
			for ci, c := range f.cols {
				if c == "番号" {
					for i := range f.rows {
						f.rows[i][ci] = fmt.Sprint(i + 1)
					}
				}
			}
		}
		if _, err := d.FillTable(caption, f.cols, f.rows); err != nil {
			note.warn("⚠ 「" + caption + "」を埋められません: " + err.Error())
		}
	}
}

// plainTableS はキャプションの無い表です。取り消し線のセルは `<s>` で残します（見た目で古いと分かるように）。
func plainTableS(rows [][]string, struck [][]bool) string {
	var b strings.Builder
	b.WriteString("<table><tbody>")
	for i, r := range rows {
		b.WriteString("<tr>")
		cell := "td"
		if i == 0 {
			cell = "th"
		}
		for j, v := range r {
			s := stdhtml.EscapeString(v)
			if i < len(struck) && j < len(struck[i]) && struck[i][j] && strings.TrimSpace(v) != "" {
				s = "<s>" + s + "</s>"
			}
			b.WriteString("<" + cell + ">" + s + "</" + cell + ">")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}
