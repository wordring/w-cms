package toho

// 外注加工表の「最新単価」（2026-10-23）。
//
// 材料表の最新単価（material_price.go）と同じく、出した部材発注書（発注済・納品済）の行から、いちばん新しい単価を
// 時点と仕入先つきで出します。違うのは鍵で、外注加工は加工製品ごとの部材なので、弊社品番と種類の鍵（設定の order_kinds の
// key——番号・加工内容）で当てます。業者の納品書から作った部材発注書の行も、加工製品の行に結べばここに出ます（order_bind.go）。
//
// > 2026-10-23 利用者:「加工製品の行への結びは人が選び、外注加工表にも最新単価を出してください。」

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// latestPartPrices は全社の部材発注明細表から、種類 kindName の部材の最新単価を、procKey（加工製品＋種類の鍵）で集めます。
// 日付は部材発注書の部材発注日（無ければ納品日）。読めないページ・出していない紙・単価の無い行は数えません。
func latestPartPrices(db cms.ReadOnlyDB, viewer *auth.User, kindName string) map[string]materialPrice {
	out := map[string]materialPrice{}
	rows, err := cms.VocabRowsOfType(db, ourOrderItemsType)
	if err != nil || len(rows) == 0 {
		return out
	}
	tags, err := cms.TagRowsNamed(db, PurchasedAtTag, DeliveredAtTag, SupplierTag)
	if err != nil {
		return out
	}
	ordered, delivered, supplierOf := map[int]string{}, map[int]string{}, map[int]string{}
	for _, t := range tags {
		m := map[string]map[int]string{PurchasedAtTag: ordered, DeliveredAtTag: delivered, SupplierTag: supplierOf}[t.Name]
		if _, dup := m[t.PageID]; !dup && strings.TrimSpace(t.Value) != "" {
			m[t.PageID] = strings.TrimSpace(t.Value)
		}
	}
	def, _ := cms.VocabDefByType(ourOrderItemsType)
	canView := viewCheck(viewer)
	for _, r := range rows {
		if !canView(r.PageID) || !orderLineSent(r.Values["status"]) {
			continue
		}
		k, vals, ok := orderLineKindOf(def, r)
		if !ok || k.Kind != kindName {
			continue
		}
		key := k.keyOf(vals)
		cost, _ := estimateNumber(r.Values["cost"])
		if key == "" || cost <= 0 {
			continue
		}
		date := ordered[r.PageID]
		if date == "" {
			date = delivered[r.PageID]
		}
		if n, ok := cms.NormalizeValue(cms.ColDate, date); ok {
			date = n
		}
		p := materialPrice{Cost: cost, Date: date, Supplier: supplierOf[r.PageID], PageID: r.PageID}
		// 1行がいくつかの加工製品のぶんを持つことがある（内訳）——どの加工製品の行にも同じ単価を出す。
		seen := map[int]bool{}
		for _, e := range rowSupplies(r) {
			pid := pageNum(e.Product)
			if pid == 0 || seen[pid] {
				continue
			}
			seen[pid] = true
			pk := procKey(pid, key)
			if cur, ok := out[pk]; ok && !newerPrice(p, cur) {
				continue
			}
			out[pk] = p
		}
	}
	return out
}

// renderPartPrices は加工製品ページの部材の表 tbl（種類 kindName）の右に「最新単価」の列を足します（鏡）。
func renderPartPrices(ctx *cms.MirrorContext, tbl *html.Node, kindName string) {
	rows := rowsOf(tbl)
	if len(rows) < 2 {
		return
	}
	k, ok := orderKindByName(kindName)
	if !ok {
		return
	}
	head := rows[0]
	// 鍵の列（発注の表の名前）を、この表の見出しへ引き直す（columns の右側・`@タグ` は表に無いので飛ばす）。
	cols := map[string]int{}
	for _, l := range k.Key {
		for _, src := range strings.Split(k.Columns[l], "|") {
			if src = strings.TrimSpace(src); src == "" || strings.HasPrefix(src, "@") {
				continue
			}
			if i := headerIndexOf(head, src); i >= 0 {
				cols[l] = i
				break
			}
		}
	}
	if len(cols) == 0 {
		return // この種類の表に見えない（人が列を入れ替えた等）
	}
	if isMigrating(ctx.DB, ctx.PageID) {
		appendMigratingRow(tbl, headerCellCount(tbl))
		return
	}
	prices := latestPartPrices(ctx.DB, ctx.Viewer, kindName)
	appendPriceHead(head, priceColLabel)
	for _, tr := range rows[1:] {
		vals := map[string]string{}
		for l, i := range cols {
			vals[l] = cellText(tr, i)
		}
		key := k.keyOf(vals)
		p, ok := prices[procKey(ctx.PageID, key)]
		if key == "" || !ok {
			appendPriceEmpty(tr) // 空欄＝出した部材発注書の記録が無い
			continue
		}
		appendPriceValue(tr, commaDecimal(p.Cost)+"円", strings.TrimSpace(p.Date), strings.TrimSpace(p.Supplier))
	}
}
