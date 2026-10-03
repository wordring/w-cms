package toho

// ─────────────────────────────────────────────────────────────────────────
// 行の表の道具——表の種類を引数にした、行を足す・抜く・新しく置く（2026-10-03）
//
// 発注部材表（order_draft.go）のために書いた道具を、見積依頼（rfq*.go）でも使えるよう、表の種類（`vocabType`）を引数に
// しました（【考察】見積の依頼と見積書 §2.0d——写して2本にしない。直すときに片方だけ直る食い違いを作らない）。
// 行は `ourOrderLine`（発注の1行）で運び、列の値は `orderLineValue` が決めます（材料の行に品名を書かない・単価 0 を書かない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// tableOfLinesHTML は vocabType の表を lines で組みます（キャプションで名乗る・見出し行は宣言から）。
//
// wrap なら節（`<section>`）で包みます——表の外（節の中・表の直後）に操作の欄を置くため（`draftBoxOf`）。
// ⚠ **行が0でも表の骨は出し、空の行を1つ置きます**——見出しだけの表は、エディタで行を足す取っ掛かりがありません。
func tableOfLinesHTML(vocabType string, lines []ourOrderLine, wrap bool) string {
	var b strings.Builder
	if wrap {
		b.WriteString(`<section>`)
	}
	b.WriteString(`<table><caption>` + stdhtml.EscapeString(displayNameOf(vocabType)) + `</caption><tbody>`)
	b.WriteString(headerRowHTML(vocabType))
	for _, ln := range lines {
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(vocabType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(orderLineValue(ln, c.Field)) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	if len(lines) == 0 {
		b.WriteString(emptyRowHTML(vocabType))
	}
	b.WriteString(`</tbody></table>`)
	if wrap {
		b.WriteString(`</section>`)
	}
	return b.String()
}

// appendLinesToTable は本文の n 枚目（1始まり）の vocabType の表へ行を足します。
//
// ⚠ **既にある表の見出しに合わせて**列を並べます（`fieldsOfHeader`）。⚠ 書き足す取っ掛かりの**空の行は取り除きます**。
// 戻り値は（新しい本文・足した行数・表が見つかったか）。
func appendLinesToTable(body, vocabType string, n int, lines []ourOrderLine) (string, int, bool) {
	if n < 1 {
		return body, 0, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0, false
	}
	tables := tablesOfType(nodes, vocabType)
	if n > len(tables) {
		return body, 0, false
	}
	table := tables[n-1]
	rows := rowsOf(table)
	if len(rows) > 0 && len(lines) > 0 {
		for _, tr := range rows[1:] {
			if lineIsEmpty(lineOfRow(rows[0], tr)) && tr.Parent != nil {
				tr.Parent.RemoveChild(tr)
			}
		}
	}
	tbody := lastChild(table, "tbody")
	if tbody == nil {
		tbody = table
	}
	fields := fieldsOfHeader(table, vocabType)
	added := 0
	for _, ln := range lines {
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for _, field := range fields {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			if v := orderLineValue(ln, field); v != "" {
				td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
			}
			tr.AppendChild(td)
		}
		tbody.AppendChild(tr)
		added++
	}
	return htmldoc.Render(nodes), added, true
}

// placeNewTableAfterLast は新しい表（HTML）を、**すでにある vocabType の表のうち最後のものの直後**へ置きます
// （節で包まれていれば節の直後）。無ければ本文の末尾です。
//
// ⚠ **位置をコードで決めない**（2026-09-27・テンプレート駆動）——人が並べた場所に、新しいものも並びます。
func placeNewTableAfterLast(body, vocabType, tableHTML string) string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body + tableHTML
	}
	tables := tablesOfType(nodes, vocabType)
	if len(tables) == 0 {
		return body + tableHTML
	}
	repl, err := htmldoc.ParseFragment(tableHTML)
	if err != nil || len(repl) == 0 {
		return body + tableHTML
	}
	out, ok := spliceNodes(nodes, draftBoxOf(tables[len(tables)-1]), repl, true)
	if !ok {
		return body + tableHTML
	}
	return out
}

// takeTableRows は本文の n 枚目（1始まり）の vocabType の表から、rows の行（見出しを除いた何行目か・1始まり）を
// 抜き、**抜いた行の中身**を rows の順で返します。見つからない行は飛ばします。
//
// dropEmpty なら、抜いて空になった表を節ごと消します（発注部材表・見積依頼部材表——空の表が溜まらないように）。
// そうでなければ空の行を1つ置いて表を残します（必要部材表・臨時部材表——「行が無いときも空の表を表示」）。
func takeTableRows(body, vocabType string, n int, rows []int, dropEmpty bool) (string, []ourOrderLine, bool) {
	if n < 1 || len(rows) == 0 {
		return body, nil, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, nil, false
	}
	tables := tablesOfType(nodes, vocabType)
	if n > len(tables) {
		return body, nil, false
	}
	table := tables[n-1]
	trs := rowsOf(table) // 先頭は見出し行
	if len(trs) == 0 {
		return body, nil, false
	}
	var out []ourOrderLine
	var gone []*html.Node
	for _, r := range rows {
		if r < 1 || r >= len(trs) {
			continue
		}
		out = append(out, lineOfRow(trs[0], trs[r]))
		gone = append(gone, trs[r])
	}
	if len(gone) == 0 {
		return body, nil, false
	}
	for _, tr := range gone {
		if tr.Parent != nil {
			tr.Parent.RemoveChild(tr)
		}
	}
	if len(rowsOf(table)) <= 1 {
		if dropEmpty {
			res, ok := spliceNodes(nodes, draftBoxOf(table), nil, false)
			return res, out, ok
		}
		tbody := lastChild(table, "tbody")
		if tbody == nil {
			tbody = table
		}
		tbody.AppendChild(emptyRowNode(vocabType))
	}
	return htmldoc.Render(nodes), out, true
}

// emptyRowNode は vocabType の列の数だけ空のセルを持つ行です。
func emptyRowNode(vocabType string) *html.Node {
	tr := &html.Node{Type: html.ElementNode, Data: "tr"}
	for range columnsOf(vocabType) {
		tr.AppendChild(&html.Node{Type: html.ElementNode, Data: "td"})
	}
	return tr
}
