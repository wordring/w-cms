package toho

// 届いた部材を在庫表へ入れる（2026-10-23・【考察】在庫の記録 §6.1・§6.6 の2番）。
//
// 部材発注書の「📦 納品済」で行が届いたとき、その行の数を在庫表に入れ、同じときに、その行を頼んだ案件の必要（作業を始めた分）を引きます。
// 引いたあとの残り＝その案件の必要を超えて届いた分だけが、在庫表の数を増やします（手配の帳簿の余り・procure_ledger.go の excess）。
// ほとんどの行は残りが 0 なので、在庫表は変わりません。
//
//   - 案件の書かれていない行（手で足した行）は、全部を在庫に入れます（頼んだ作業が分からないので）。
//   - 備考が「追加」の行（作り直しのぶん）は入れません（rowSupplies が数えない）。
//   - 「納品済」から別の状態へ戻したときは、同じ数を在庫表から引きます（押し間違いを戻せるように）。
//   - 数が 0 以下になった行は消します（§7 問い11「よいです。」）。
//   - 届いた数が発注と違うとき、在庫から使ったときは、人が在庫表を直します（§7 問い14・問い15）。

import (
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// stockMove は在庫表へ足し引きする1件です。
type stockMove struct {
	kind  stockKind
	key   string            // 在庫表の行を見分ける鍵（stockRowKey と同じ作り方）
	cells map[string]string // 新しい行に書く値（列の名前 → 値）
	qty   float64
}

// stockRowKey は在庫表の行の鍵です。部材は種類の鍵の列（設定の order_kinds の key）、
// 外注加工した部品と加工製品は、加工製品（弊社品番）も鍵に入れます。
func stockRowKey(k stockKind, vals map[string]string) string {
	key := ""
	if k.Kind != "" {
		ok, found := orderKindByName(k.Kind)
		if !found {
			return ""
		}
		key = ok.keyOf(vals)
		if key == "" {
			return ""
		}
	}
	if k.PerProduct {
		id, ok := page.NormalizeID(trimSlash(vals[OurItemNoTag]))
		if !ok || id == "" {
			return ""
		}
		key += "\x00" + id
	}
	return k.Type + "\x1e" + key
}

// orderLineKindOf は部材発注の行の種類を返します（種類の列、無ければ鍵の列が埋まる最初の種類・orderRowKey と同じ選び方）。
func orderLineKindOf(def cms.VocabDef, r cms.VocabRow) (orderKind, map[string]string, bool) {
	vals := map[string]string{}
	for _, c := range def.Columns {
		vals[c.Label] = labelValue(def, r, c.Label)
	}
	if k, ok := orderKindByName(vals["種類"]); ok {
		return k, vals, true
	}
	for _, k := range OrderKinds() {
		if k.keyOf(vals) != "" {
			return k, vals, true
		}
	}
	return orderKind{}, nil, false
}

// stockMovesOfOrderLines は部材発注書 pageID の行 picked（表の順・データ行は0から）が届いたときに、在庫表へ足す数を返します。
func stockMovesOfOrderLines(db cms.ReadOnlyDB, canView func(int) bool, pageID int, picked map[[2]int]bool) []stockMove {
	if len(picked) == 0 {
		return nil
	}
	// 索引の行の名札（ブロックの番号）を、表の順に並べ直す。
	rows, err := cms.VocabTableRowsOf(db, pageID, ourOrderItemsType)
	if err != nil {
		return nil
	}
	var blocks []int
	seen := map[int]bool{}
	for _, r := range rows {
		if !seen[r.BlockNo] {
			seen[r.BlockNo] = true
			blocks = append(blocks, r.BlockNo)
		}
	}
	sort.Ints(blocks)
	tableOf := map[int]int{}
	for i, b := range blocks {
		tableOf[b] = i
	}
	want := map[orderRowRef]bool{}
	for _, r := range rows {
		if picked[[2]int{tableOf[r.BlockNo], r.RowNo}] {
			want[refOf(r)] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	open, err := openOrderRows(db, canView)
	if err != nil {
		return nil
	}
	l := buildLedger(db, canView, open)
	def, _ := cms.VocabDefByType(ourOrderItemsType)
	merged := map[string]*stockMove{}
	var order []string
	for _, list := range l.supplies {
		for _, s := range list {
			if s.kind != supplyOrdered || !want[refOf(s.src)] {
				continue
			}
			n := s.excess
			if s.forOrder == 0 {
				n = s.qty
			}
			if n <= 0 {
				continue
			}
			ok, vals, found := orderLineKindOf(def, s.src)
			if !found {
				continue
			}
			sk, found := stockKindOf(ok.Kind)
			if !found {
				continue
			}
			cells := map[string]string{}
			for _, c := range stockColumns(sk.Type) {
				switch c.Label {
				case StockQtyLabel, StockCountedLabel, "備考":
					continue
				case OurItemNoTag:
					if s.product > 0 {
						cells[c.Label] = page.FormatID(s.product)
					}
				default:
					cells[c.Label] = vals[c.Label]
				}
			}
			key := stockRowKey(sk, cells)
			if key == "" {
				continue
			}
			m := merged[key]
			if m == nil {
				m = &stockMove{kind: sk, key: key, cells: cells}
				merged[key] = m
				order = append(order, key)
			}
			m.qty += float64(n)
		}
	}
	sort.Strings(order)
	out := make([]stockMove, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

// applyStockMoves は在庫表の本文に moves を足し引きし、書けなかったものの断りを返します。
func applyStockMoves(body string, moves []stockMove) (string, []string) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, []string{"在庫表のページの本文を読めません"}
	}
	var notes []string
	changed := false
	for _, mv := range moves {
		tables := tablesOfType(nodes, mv.kind.Type)
		if len(tables) == 0 {
			notes = append(notes, "「"+mv.kind.Caption+"」の表がありません")
			continue
		}
		t := tables[0]
		trs := rowsOf(t)
		if len(trs) == 0 {
			notes = append(notes, "「"+mv.kind.Caption+"」に見出しの行がありません")
			continue
		}
		labels := cellTexts(trs[0])
		qi := headerIndexOf(trs[0], StockQtyLabel)
		if qi < 0 {
			notes = append(notes, "「"+mv.kind.Caption+"」に「"+StockQtyLabel+"」の列がありません")
			continue
		}
		var hit, blank *html.Node
		for _, tr := range trs[1:] {
			texts := cellTexts(tr)
			vals := map[string]string{}
			empty := true
			for i, l := range labels {
				if i < len(texts) {
					vals[l] = texts[i]
					if texts[i] != "" {
						empty = false
					}
				}
			}
			if empty {
				if blank == nil {
					blank = tr
				}
				continue
			}
			if stockRowKey(mv.kind, vals) == mv.key {
				hit = tr
				break
			}
		}
		if hit != nil {
			cells := cellsOf(hit)
			cur := 0.0
			if qi < len(cells) {
				s := strings.TrimSpace(textOf(cells[qi]))
				if s != "" {
					f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
					if err != nil {
						notes = append(notes, "「"+mv.kind.Caption+"」の在庫数「"+s+"」を数として読めません（直していません）")
						continue
					}
					cur = f
				}
			}
			next := cur + mv.qty
			if next <= 0 {
				removeStockRow(t, hit, labels)
			} else if qi < len(cells) {
				setCellText(cells[qi], formatStockQty(next))
			}
			changed = true
			continue
		}
		if mv.qty <= 0 {
			notes = append(notes, "「"+mv.kind.Caption+"」に引く行がありません（"+stockMoveName(mv)+"）")
			continue
		}
		vals := map[string]string{StockQtyLabel: formatStockQty(mv.qty)}
		for k, v := range mv.cells {
			vals[k] = v
		}
		tr := blank
		if tr == nil {
			tr = &html.Node{Type: html.ElementNode, Data: "tr"}
			host := lastChild(t, "tbody")
			if host == nil {
				host = t
			}
			host.AppendChild(tr)
		}
		for tr.FirstChild != nil {
			tr.RemoveChild(tr.FirstChild)
		}
		for _, l := range labels {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			setStockCell(td, vals[l])
			tr.AppendChild(td)
		}
		changed = true
	}
	if !changed {
		return body, notes
	}
	return htmldoc.Render(nodes), notes
}

// removeStockRow は在庫表の行を消します。データ行が無くなるときは、人が書き足せるように空の行を1つ残します。
func removeStockRow(t, tr *html.Node, labels []string) {
	if len(rowsOf(t)) <= 2 {
		for _, c := range cellsOf(tr) {
			setStockCell(c, "")
		}
		return
	}
	if tr.Parent != nil {
		tr.Parent.RemoveChild(tr)
	}
}

// setStockCell はセルの中身を s にします。空のセルは、エディタと同じく `<br/>` にします（テンプレートの空の行と同じ形）。
func setStockCell(cell *html.Node, s string) {
	s = strings.TrimSpace(s)
	setCellText(cell, s)
	if s == "" {
		cell.AppendChild(&html.Node{Type: html.ElementNode, Data: "br"})
	}
}

// stockMoveName は断りの文に出す、部材の名前です（鍵の列を空白で繋ぐ）。
func stockMoveName(mv stockMove) string {
	var parts []string
	for _, c := range stockColumns(mv.kind.Type) {
		if v := strings.TrimSpace(mv.cells[c.Label]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// formatStockQty は在庫数を書く形にします（整数は小数点を付けない）。
func formatStockQty(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// negateStockMoves は moves の数の符号を返したものです（納品済を戻したとき）。
func negateStockMoves(moves []stockMove) []stockMove {
	out := make([]stockMove, len(moves))
	for i, m := range moves {
		m.qty = -m.qty
		out[i] = m
	}
	return out
}

// writeStockMoves は置き場「在庫」の在庫表に moves を書き、断りを返します（在庫の置き場が無ければ、その断りだけ）。
func writeStockMoves(author string, moves []stockMove) []string {
	if len(moves) == 0 {
		return nil
	}
	id, ok := cms.TopLevelPageByTitle(StockBoxTitle)
	if !ok {
		return []string{"置き場「" + StockBoxTitle + "」がありません（管理画面の「足りない置き場を作る」）"}
	}
	var notes []string
	if err := cms.RewriteBody(id, author, func(cur string) string {
		out, n := applyStockMoves(cur, moves)
		notes = n
		return out
	}); err != nil {
		return []string{"在庫表に書けません: " + err.Error()}
	}
	return notes
}
