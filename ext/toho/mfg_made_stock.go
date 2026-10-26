package toho

// 製造完了で加工製品を在庫表へ入れる（2026-10-23・【考察】在庫の記録 §6.4・§6.6 の4番）。
//
// 製造明細表の「✓ 製造完了」で、その行の `製造数`（実際に作った数）を加工製品在庫表に入れ、同じときに、
// 納める案件のぶん（行の `数量`）を引きます。残り＝多く作った分だけが、在庫表の数を増やします。
//
//   - `製造数` が空なら、製造完了のときに `数量` を入れます（多く作ったときは、人が先に直してから押す・問い9）。
//   - 自社案件（`ページ種類：自社案件`）の行は、納める案件が無いので、作った数が全部在庫に残ります。
//   - 製造完了を外したときは、同じ数を在庫表から引きます。引く数はそのときの `製造数` で数え直すので、あいだに `製造数` を
//     直すと入れた数とずれる——ずれたら人が在庫表を直す（2026-10-26・【考察】在庫の記録 §8）。
//
// 利用者（2026-10-23）:「その形で始めて様子を見ましょう」（問い9——加工製品の入り）。

import (
	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// MfgMadeQtyLabel は製造明細表の、実際に作った数の列です。
const MfgMadeQtyLabel = "製造数"

// setMfgMade は製造明細表の行 row の「製造完了」を newVal にし、製造完了にしたときは空の `製造数` に `数量` を入れます。
// 書いたあとの行の値（列の名前 → 値）も返します（在庫表へ入れる数を決めるため）。
func setMfgMade(cur string, row int, itemNo, oldVal, newVal string) (string, map[string]string, error) {
	out, err := setItemTableCell(cur, MfgItemsType, row, itemNo, "製造完了", oldVal, newVal)
	if err != nil {
		return "", nil, err
	}
	vals := itemTableRowValues(out, MfgItemsType, row)
	if q, has := vals[MfgMadeQtyLabel]; newVal == MarkDone && has && q == "" && vals["数量"] != "" {
		if filled, err := setItemTableCell(out, MfgItemsType, row, itemNo, MfgMadeQtyLabel, "", vals["数量"]); err == nil {
			out = filled
			vals[MfgMadeQtyLabel] = vals["数量"]
		}
	}
	return out, vals, nil
}

// itemTableRowValues は形式 vocabType の明細表（ページの1つ目）の行 row（データ行・0から）を、列の名前 → 値で返します。
func itemTableRowValues(body, vocabType string, row int) map[string]string {
	out := map[string]string{}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return out
	}
	tables := tablesOfType(nodes, vocabType)
	if len(tables) == 0 {
		return out
	}
	rows := rowsOf(tables[0])
	if row < 0 || row+1 >= len(rows) {
		return out
	}
	labels, texts := cellTexts(rows[0]), cellTexts(rows[row+1])
	for i, l := range labels {
		if l == "" {
			continue
		}
		if _, dup := out[l]; dup {
			continue
		}
		if i < len(texts) {
			out[l] = texts[i]
		} else {
			out[l] = ""
		}
	}
	return out
}

// stockOnMade は、製造完了にした（外した）行の、納める案件を超えて作った分を加工製品在庫表へ足し（引き）ます。
func stockOnMade(user *auth.User, pageID string, vals map[string]string, value string) (int, []string) {
	id, ok := page.NormalizeID(trimSlash(vals[OurItemNoTag]))
	if !ok || id == "" {
		return 0, nil // どの加工製品か分からない行は在庫に入れない
	}
	qty, okQ := estimateNumber(vals["数量"])
	made, okM := estimateNumber(vals[MfgMadeQtyLabel])
	if !okM {
		made, okM = qty, okQ
	}
	if !okM {
		return 0, nil
	}
	deliver := qty
	if c := trimSlash(vals["案件"]); c != "" && pageKindOf(pageNum(c)) == PageKindOwnCase {
		deliver = 0 // 自社案件は納める先が無い——作った数が全部在庫
	}
	delta := made - deliver
	if delta <= 0 {
		return 0, nil
	}
	if value != MarkDone {
		delta = -delta
	}
	var sk stockKind
	for _, k := range stockKinds {
		if k.Type == StockProductType {
			sk = k
		}
	}
	cells := map[string]string{OurItemNoTag: id, ItemNoTag: vals[ItemNoTag], "品名": vals["品名"]}
	mv := stockMove{kind: sk, key: stockRowKey(sk, cells), cells: cells, qty: delta}
	written, notes := writeStockMoves(user, []stockMove{mv})
	if !written {
		return 0, notes
	}
	auth.Audit(user.Username, "stock.mfg-made", pageID+" "+stockMoveName(mv)+" "+formatStockQty(delta))
	return 1, notes
}
