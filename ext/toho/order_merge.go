package toho

// 発注部材表の行をまとめる（2026-10-21・【考察】製造単位の部材発注 §6.3 の5番）。
//
// 2026-10-21 利用者:「備考も含め一致するなら一行にたす。紙には合計を刷って良い。備考が違うなどの場合は、上下に行を並べるような配慮が欲しい。」
// 2026-10-21 利用者:「仕入れ先は加工製品ページの表に推奨業者として書かれているので、これをデフォルトで入れ、必要なら修正してからPDFの作成ボタンを押せば良いと思う」
//
//   - 製造ページの「🧾 部材作業」が集めた行を、案件・加工製品をまたいで、残りの列が全部同じなら1行にまとめて数を足します。
//     まとめた行の「内訳」に `案件/弊社品番:数` を「、」で並べます（手配済みの数え方が案件・加工製品ごとに分けて数える——procure_ledger.go）。
//   - 少しでも違う行はまとめず、同じ部材が上下に並ぶように並べます（種類 → 材質 → 形状 → 寸法 → 品名 → 番号 → 加工内容 → 表面 → 仕様 → 備考）。
//   - 推奨業者ごとに発注部材表を分けます（推奨業者の無い行は最後の表）。

import (
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

const (
	breakdownLabel = "内訳"
	vendorLabel    = "推奨業者"
)

// breakdownEntry は内訳の1つ（どの案件の、どの加工製品のための、いくつか）です。
type breakdownEntry struct {
	Order, Product string // ページ番号（6桁）
	Qty            int
}

// formatBreakdown は内訳を `001626/000777:20、001630/000778:10` の形に書きます。
func formatBreakdown(es []breakdownEntry) string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Order+"/"+e.Product+":"+strconv.Itoa(e.Qty))
	}
	return strings.Join(parts, "、")
}

// parseBreakdown は内訳を読みます。空なら (nil, false)。1つでも読めなければ (nil, false)——呼び手はまとめていない行として読む。
// 案件は空でもよい（`/000777:20`——案件の無い手当て）。
func parseBreakdown(s string) ([]breakdownEntry, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var out []breakdownEntry
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '、' || r == ',' || r == '，' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		slash := strings.Index(p, "/")
		colon := strings.LastIndex(p, ":")
		if slash < 0 || colon < slash {
			return nil, false
		}
		order := strings.TrimSpace(p[:slash])
		prod, okP := page.NormalizeID(strings.TrimSpace(p[slash+1 : colon]))
		qty, err := strconv.Atoi(strings.TrimSpace(p[colon+1:]))
		if !okP || err != nil || qty < 0 {
			return nil, false
		}
		if order != "" {
			id, okO := page.NormalizeID(order)
			if !okO {
				return nil, false
			}
			order = id
		}
		out = append(out, breakdownEntry{Order: order, Product: prod, Qty: qty})
	}
	return out, len(out) > 0
}

// entriesOf は1行の内訳です（内訳が無ければ、案件と弊社品番の列と数量の1つ）。数量が整数でなければ (nil, false)。
func entriesOf(ln ourOrderLine) ([]breakdownEntry, bool) {
	if es, ok := parseBreakdown(ln.Breakdown); ok {
		return es, true
	}
	qty, err := strconv.Atoi(strings.TrimSpace(ln.Quantity))
	prod, okP := refPageID(ln.ProductID)
	if err != nil || !okP {
		return nil, false
	}
	order := ""
	if id, ok := refPageID(ln.ForOrder); ok {
		order = id
	}
	return []breakdownEntry{{Order: order, Product: prod, Qty: qty}}, true
}

// sameLineKey は「数量・案件・弊社品番・製造・内訳のほかが全部同じ」かを見る鍵です（表記の揺れは畳む）。
func sameLineKey(ln ourOrderLine) string {
	parts := []string{ln.Kind, ln.No, ln.ItemID, ln.ItemName, ln.Work, ln.Material, ln.Shape, ln.Size, ln.Color,
		ln.Spec, ln.Supplied, ln.Unit, ln.Cost, ln.Note, ln.Vendor}
	for i, p := range parts {
		parts[i] = cms.NormalizeText(strings.TrimSpace(p))
	}
	return strings.Join(parts, "\x1f")
}

// mergeOrderLines は、数量・案件・弊社品番のほかが全部同じ行を1行にまとめ（数を足し、内訳を書く）、
// 同じ部材が上下に並ぶように並べて返します。数量が整数でない行・弊社品番の読めない行はまとめません（そのまま並べる）。
func mergeOrderLines(lines []ourOrderLine) []ourOrderLine {
	type group struct {
		line    ourOrderLine
		entries []breakdownEntry
		first   int
	}
	var groups []*group
	byKey := map[string]*group{}
	var loose []ourOrderLine
	for i, ln := range lines {
		es, ok := entriesOf(ln)
		if !ok {
			loose = append(loose, ln)
			continue
		}
		k := sameLineKey(ln)
		if g := byKey[k]; g != nil {
			g.entries = append(g.entries, es...)
			g.line.Mfg = joinRefs(g.line.Mfg, ln.Mfg)
			if strings.TrimSpace(g.line.Lot) != strings.TrimSpace(ln.Lot) {
				g.line.Lot = "" // ロットの違う行をまとめたら、ロットは言えない（見積もりのときに人が書く）
			}
			continue
		}
		g := &group{line: ln, entries: append([]breakdownEntry(nil), es...), first: i}
		byKey[k] = g
		groups = append(groups, g)
	}
	out := make([]ourOrderLine, 0, len(groups)+len(loose))
	for _, g := range groups {
		ln := g.line
		// 同じ案件・同じ加工製品の内訳は足し合わせる。
		var es []breakdownEntry
		at := map[string]int{}
		total := 0
		for _, e := range g.entries {
			total += e.Qty
			k := e.Order + "/" + e.Product
			if i, ok := at[k]; ok {
				es[i].Qty += e.Qty
				continue
			}
			at[k] = len(es)
			es = append(es, e)
		}
		ln.Quantity = strconv.Itoa(total)
		if len(es) == 1 {
			ln.ForOrder, ln.ProductID, ln.Breakdown = es[0].Order, es[0].Product, ""
		} else {
			ln.ForOrder = joinEntries(es, func(e breakdownEntry) string { return e.Order })
			ln.ProductID = joinEntries(es, func(e breakdownEntry) string { return e.Product })
			ln.Breakdown = formatBreakdown(es)
		}
		out = append(out, ln)
	}
	out = append(out, loose...)
	sortOrderLines(out)
	return out
}

func joinEntries(es []breakdownEntry, f func(breakdownEntry) string) string {
	seen := map[string]bool{}
	var vs []string
	for _, e := range es {
		v := f(e)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		vs = append(vs, v)
	}
	return strings.Join(vs, "、")
}

// sortOrderLines は同じ部材が上下に並ぶように並べます（種類は先に出てきた順・そのあと材質・形状・寸法・品名・番号・加工内容・表面・仕様・備考）。
func sortOrderLines(lines []ourOrderLine) {
	kindAt := map[string]int{}
	for _, ln := range lines {
		if _, ok := kindAt[ln.Kind]; !ok {
			kindAt[ln.Kind] = len(kindAt)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if kindAt[a.Kind] != kindAt[b.Kind] {
			return kindAt[a.Kind] < kindAt[b.Kind]
		}
		ka := []string{a.Material, a.Shape, a.Size, a.ItemName, a.No, a.Work, a.Color, a.Spec, a.Note}
		kb := []string{b.Material, b.Shape, b.Size, b.ItemName, b.No, b.Work, b.Color, b.Spec, b.Note}
		for n := range ka {
			x, y := cms.NormalizeText(ka[n]), cms.NormalizeText(kb[n])
			if x != y {
				return x < y
			}
		}
		return false
	})
}

// splitByVendor は行を推奨業者ごとの組に分けます（推奨業者の出てきた順・推奨業者の無い行は最後の組）。
func splitByVendor(lines []ourOrderLine) [][]ourOrderLine {
	var order []string
	groups := map[string][]ourOrderLine{}
	for _, ln := range lines {
		v := strings.TrimSpace(ln.Vendor)
		if _, ok := groups[v]; !ok && v != "" {
			order = append(order, v)
		}
		groups[v] = append(groups[v], ln)
	}
	if len(groups[""]) > 0 {
		order = append(order, "")
	}
	out := make([][]ourOrderLine, 0, len(order))
	for _, v := range order {
		out = append(out, groups[v])
	}
	return out
}
