package toho

// ─────────────────────────────────────────────────────────────────────────
// 手配の帳簿——必要と手当てを**受注ごとに**突き合わせる（2026-10-01）
//
// 利用者への問い（2026-10-01）:「必要部材表は『買った数』を加工製品ごとに通算して引いています。同じ加工製品の注文が
// 2回目以降だと、前の注文のために買った材料まで引かれ、新しい注文の部材が出ないことがあります（2つの注文が同時に残って
// いても、買った数を両方から引く）。直しますか？」→「受注ごとに数える」。
//
//	必要   … 開いている受注の行（完了でない・出し終えていない・移行中でない）× 加工製品の構成部品の表
//	手当て … 部材発注明細表（部材発注書の行・取消も含む）・発注部材表（入れた分）・手配不要表（不要にした分）
//
//   - 手当ての行が `受注`（受注ページ。2026-10-19 から列の名前は `案件`・機械キーは for-order のまま）を持っていれば、**まずその受注の必要に**当てます（受注が閉じていても——納め
//     終えた注文のために買った分は、その注文で使った）。
//   - **余りは在庫**です（2026-10-01 利用者:「部材は在庫がある場合があります」「必要数より購入数のほうが多ければ、在庫に
//     なります」）——その受注の必要を超えて買った分（部材発注明細表・発注部材表）は、ほかの受注の必要へ回ります。
//   - `受注` の無い行（2026-10-01 より前の部材発注書・発注部材表に手で書いた行。2026-10-19 より前は、発注部材表から
//     作った部材発注書でもこの列が落ちていた——our_order_new.go）も、在庫と同じく回ります。
//   - 回すのは、開いている受注の残りへ**納期の早い順に1回だけ**（同じ手当てを2つの受注に二重に当てない）。
//   - 当てる順は 部材発注明細表 → 発注部材表 → 手配不要表（紙になったものから）。⚠ 手配不要の余りは在庫にしない（買っていない）。
//
// ⚠ 未手配の数（unordered.go の UnorderedItems——必要部材表の表は 2026-10-21 に外し、いまは製造ページの「📦」が使う）と受注ページの手配状況（procurement.go）は、この帳簿を通して数えます——**別々に引き算
// すると数が食い違います**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"
	"time"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 手当ての種類（当てる順）。
const (
	supplyOrdered = iota // 部材発注明細表
	supplyDrafted        // 発注部材表
	supplySkipped        // 手配不要表
)

// supply は手当て1行です。
type supply struct {
	kind     int
	forOrder int // 受注ページ（0＝書かれていない）
	qty      int // 行の数
	left     int // まだ当てていない数
	doc      ProcurementOrder
	stock    bool // いま当てているのが、ほかの受注のために買った余り（在庫）か
	// src は手当ての元の行、product はどの加工製品のぶんか（在庫表へ入れるとき・stock_in.go）。
	src     cms.VocabRow
	product int
	// excess は、その案件の必要（開いている行・閉じた行）に当てたあとの余りです（手順2のあと・stock_in.go が在庫表へ入れる数）。
	excess int
}

// orderRowRef は受注の行の名札です（ページ・何番目のブロック・何行目）。
type orderRowRef struct{ page, block, row int }

func refOf(r cms.VocabRow) orderRowRef { return orderRowRef{r.PageID, r.BlockNo, r.RowNo} }

// openOrderRow は手配の対象になる受注の行です（必要部材表に並べる元）。
type openOrderRow struct {
	row    cms.VocabRow
	pid    int    // 加工製品ページ
	due    string // 行の納期（無ければページの納期）
	client string
}

// coverage は受注の行×部材1つに当てた手当てです。
type coverage struct {
	ordered, drafted, skipped int
	orders                    []ProcurementOrder // 当てた部材発注書（数は当てた分）
}

func (c coverage) total() int { return c.ordered + c.drafted + c.skipped }

// procLedger は帳簿です。
type procLedger struct {
	supplies map[string][]*supply                 // procKey(加工製品, 鍵) → 手当て
	cover    map[orderRowRef]map[string]*coverage // 受注の行 → 部材の鍵 → 当てた分
	needs    map[orderRowRef][]ProcurementItem    // 受注の行 → 必要（当てる前）
	closed   map[int]map[string]int               // 閉じた受注ページ → 部材 → まだ引いていない必要
}

// coverOf は受注の行×部材の当てた分です（無ければ空）。
func (l *procLedger) coverOf(r cms.VocabRow, key string) coverage {
	if m := l.cover[refOf(r)]; m != nil {
		if c := m[key]; c != nil {
			return *c
		}
	}
	return coverage{}
}

// isOpen は受注の行が帳簿の当て先（開いている）かです。
func (l *procLedger) isOpen(r cms.VocabRow) bool {
	_, ok := l.needs[refOf(r)]
	return ok
}

// collectSupplies は手当ての行を全部読みます（読めるページの行だけ・弊社品番のある行だけ）。
func collectSupplies(db cms.ReadOnlyDB, canView func(int) bool) map[string][]*supply {
	out := map[string][]*supply{}
	read := func(vocabType string, kind int) {
		rows, err := cms.VocabRowsOfType(db, vocabType)
		if err != nil {
			return
		}
		def, _ := cms.VocabDefByType(vocabType)
		for _, r := range rows {
			if !canView(r.PageID) {
				continue
			}
			key := orderRowKey(def, r)
			if key == "" {
				continue
			}
			// 1行が、いくつかの案件・加工製品のぶんを持つことがある（内訳・2026-10-21・order_merge.go）——ぶんごとに手当てを分ける。
			for _, e := range rowSupplies(r) {
				s := &supply{kind: kind, qty: e.Qty, left: e.Qty, src: r, product: pageNum(e.Product)}
				if e.Order != "" {
					s.forOrder = pageNum(e.Order)
				}
				if kind == supplyOrdered {
					s.doc = ProcurementOrder{PageID: r.PageID, Title: cms.PageTitleByID(r.PageID)}
				}
				k := procKey(pageNum(e.Product), key)
				out[k] = append(out[k], s)
			}
		}
	}
	read(ourOrderItemsType, supplyOrdered)
	read(OrderDraftType, supplyDrafted)
	// 手配不要表（2026-10-21 午後に製造ページの中へ作り直した・mfg_skip.go）。
	read(SkipType, supplySkipped)
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool { return list[i].kind < list[j].kind })
	}
	return out
}

// rowSupplies は部材発注明細・発注部材表の1行を、案件×加工製品のぶんに分けます（数は行の数量）。
//
//   - 内訳（`案件/弊社品番:数`）が読めれば、そのぶんごと。人が数量を内訳の合計より増やしたら（定尺に丸めた など）、
//     増えたぶんは案件の無い手当て（在庫）として最初の加工製品に足す。減らしたら、後ろのぶんから減らす。
//   - 内訳が無ければ（読めなければ）、弊社品番と案件の列の1つ。弊社品番の無い行は、どの加工製品のぶんか語らないので数えない（混ぜない）。
func rowSupplies(r cms.VocabRow) []breakdownEntry {
	// 追加の発注の行（備考「追加」・2026-10-21・order_unsend.go）は数えない——作り直しに使うぶんで、余りとして回さない。
	if isExtraLine(r.Values["note"]) {
		return nil
	}
	q := cms.VocabQuantity(r)
	if es, ok := parseBreakdown(r.Values["breakdown"]); ok {
		sum := 0
		for _, e := range es {
			sum += e.Qty
		}
		switch {
		case q > sum:
			es = append(es, breakdownEntry{Product: es[0].Product, Qty: q - sum})
		case q < sum:
			cut := sum - q
			for i := len(es) - 1; i >= 0 && cut > 0; i-- {
				d := es[i].Qty
				if d > cut {
					d = cut
				}
				es[i].Qty -= d
				cut -= d
			}
		}
		return es
	}
	productID, ok := page.NormalizeID(strings.TrimSpace(r.Values["our-item-id"]))
	if !ok || productID == "" {
		return nil
	}
	e := breakdownEntry{Product: productID, Qty: q}
	if id, ok := page.NormalizeID(strings.TrimSpace(r.Values["for-order"])); ok && id != "" {
		e.Order = id
	}
	return []breakdownEntry{e}
}

// openOrderRows は手配の対象になる受注の行を集めます（受注残表に近い線引き。⚠ 同じではない——受注残表〔backlogScan〕はページ種類
// 〔顧客発注書・顧客見積依頼・自社案件〕で選び、ここは明細表を持つページから見積の依頼だけを外す）——移行中の受注ページ・状態が完了・
// 出し終えた（数量 − 出荷済数量 ≤ 0）・どの加工製品か分からない・読めない行は入れません。
func openOrderRows(db cms.ReadOnlyDB, canView func(int) bool) ([]openOrderRow, error) {
	orders, err := cms.VocabRowsOfType(db, clientOrderItemsType)
	if err != nil {
		return nil, err
	}
	type head struct {
		client, due string
		migrating   bool
	}
	heads := map[int]head{}
	shippedBy := newShippedLookup(db) // 出荷済数量は納品書から数える（2026-10-17・delivery_shipped.go）
	headOf := func(id int) head {
		if h, ok := heads[id]; ok {
			return h
		}
		tags, err := cms.TagsOfPage(db, id)
		_, mig := tags[MigratingTag]
		// 見積の依頼（`ページ種類：顧客見積依頼`）は、まだ注文ではないので手配の対象にしない（2026-10-19）——移行中と同じく止める。
		quote := kindOfTags(tags) == PageKindCustomerQuote
		h := head{client: cms.FirstTag(tags, OrderClientTag), due: cms.FirstTag(tags, DueDateTag),
			migrating: mig || quote || err != nil} // 読めないときも止める側（isMigrating と同じ）
		heads[id] = h
		return h
	}
	var out []openOrderRow
	for _, o := range orders {
		if !canView(o.PageID) {
			continue
		}
		h := headOf(o.PageID)
		if h.migrating {
			continue
		}
		if strings.TrimSpace(o.Values["status"]) == StatusDone {
			continue
		}
		if float64(cms.VocabQuantity(o))-shippedBy.of(page.FormatID(o.PageID), o.Values["item-id"], o.Values["item-name"]) <= 0 { // 出荷済数量は納品書から（2026-10-17）
			continue
		}
		pid, ok := productOfOrderRow(db, o)
		if !ok || !canView(pid) {
			continue
		}
		due := strings.TrimSpace(o.Values["due"])
		if due == "" {
			due = h.due
		}
		out = append(out, openOrderRow{row: o, pid: pid, due: due, client: h.client})
	}
	return out, nil
}

// buildLedger は開いている受注の行の必要に、手当てを当てます。
func buildLedger(db cms.ReadOnlyDB, canView func(int) bool, open []openOrderRow) *procLedger {
	l := &procLedger{
		supplies: collectSupplies(db, canView),
		cover:    map[orderRowRef]map[string]*coverage{},
		needs:    map[orderRowRef][]ProcurementItem{},
	}
	type demand struct {
		ref  orderRowRef
		page int
		pk   string // procKey
		key  string
		left int
		due  string
	}
	var demands []*demand
	for _, o := range open {
		items := productNeeds(db, o.pid, cms.VocabQuantity(o.row))
		ref := refOf(o.row)
		l.needs[ref] = items
		l.cover[ref] = map[string]*coverage{}
		for _, it := range items {
			l.cover[ref][it.Key] = &coverage{}
			demands = append(demands, &demand{ref: ref, page: o.row.PageID, pk: procKey(o.pid, it.Key),
				key: it.Key, left: it.Required, due: o.due})
		}
	}
	take := func(d *demand, s *supply) {
		n := s.left
		if d.left < n {
			n = d.left
		}
		if n <= 0 {
			return
		}
		s.left -= n
		d.left -= n
		c := l.cover[d.ref][d.key]
		switch s.kind {
		case supplyOrdered:
			c.ordered += n
			doc := s.doc
			doc.Qty = n
			doc.Stock = s.stock
			c.orders = append(c.orders, doc)
		case supplyDrafted:
			c.drafted += n
		case supplySkipped:
			c.skipped += n
		}
	}
	// 1. その受注のための手当て（`受注` が書いてある行）を、開いている受注の必要へ。
	for _, d := range demands {
		for _, s := range l.supplies[d.pk] {
			if s.forOrder == d.page {
				take(d, s)
			}
		}
	}
	// 2. 閉じた受注（完了・出し終えた・移行中）のために書かれた手当ては、その受注の必要で使ったものとして減らす
	//    （余りだけが在庫になる）。
	//    ⚠ **閉じた行は、行ごとに見ます**（2026-10-20）——一部の行だけ閉じた受注ページ（ある加工製品は完了・ほかは開いている）を
	//    ページごと飛ばしていたので、閉じた行のために買った分が余りとして在庫に回り、別の受注の必要に当たっていた。
	for pk, list := range l.supplies {
		for _, s := range list {
			if s.forOrder == 0 || s.left <= 0 {
				continue
			}
			s.left -= closedTake(db, s.forOrder, pk, s.left, l)
		}
	}
	for _, list := range l.supplies {
		for _, s := range list {
			s.excess = s.left
		}
	}
	// 3. 余り（在庫）と `受注` の無い手当てを、納期の早い順に（日付として読めない納期は先頭——必要部材表と同じ並び）。
	//    ⚠ 手配不要の余りは回さない（買っていない）。
	sort.SliceStable(demands, func(i, j int) bool { return dueSortKey(demands[i].due) < dueSortKey(demands[j].due) })
	for _, d := range demands {
		for _, s := range l.supplies[d.pk] {
			if s.kind == supplySkipped && s.forOrder != 0 {
				continue
			}
			if s.forOrder != 0 && s.forOrder != d.page {
				s.stock = true // ほかの受注のために買った余り
			}
			take(d, s)
			s.stock = false
		}
	}
	return l
}

// closedTake は受注ページの閉じた行（帳簿の当て先でない行・2026-10-20 からは開いている行の混ざったページも）の、その部材の必要（受注の行 × 構成部品の表）から、手当て avail を
// 当てた数を返します。同じ受注ページ・同じ部材の手当てが何行あっても、必要は1回だけ引く（残りを覚えておく）。
func closedTake(db cms.ReadOnlyDB, orderPage int, pk string, avail int, l *procLedger) int {
	if l.closed == nil {
		l.closed = map[int]map[string]int{}
	}
	m, ok := l.closed[orderPage]
	if !ok {
		m = map[string]int{}
		if rows, err := cms.VocabTableRowsOf(db, orderPage, clientOrderItemsType); err == nil {
			for _, r := range rows {
				if l.isOpen(r) {
					continue // 開いている行の必要は、手順1で当てた（ここで二度引かない）
				}
				pid, ok := productOfOrderRow(db, r)
				if !ok {
					continue
				}
				for _, it := range productNeeds(db, pid, cms.VocabQuantity(r)) {
					m[procKey(pid, it.Key)] += it.Required
				}
			}
		}
		l.closed[orderPage] = m
	}
	n := m[pk]
	if avail < n {
		n = avail
	}
	m[pk] -= n
	return n
}

// dueSortKey は納期を並べる鍵にします（日付として読めないものは空＝先頭）。
func dueSortKey(due string) string {
	if norm, ok := cms.NormalizeValue(cms.ColDate, strings.TrimSpace(due)); ok {
		if _, err := time.Parse("2006-01-02", norm); err == nil {
			return norm
		}
	}
	return ""
}

// specificCover は帳簿の当て先でない受注の行（閉じた・移行中）について、その受注のために書かれた手当てだけを数えます
// （当てはせず、見せるだけ——受注ページの手配状況）。
func (l *procLedger) specificCover(orderPage, productID int, key string) coverage {
	var c coverage
	for _, s := range l.supplies[procKey(productID, key)] {
		if s.forOrder != orderPage {
			continue
		}
		n := s.qty
		switch s.kind {
		case supplyOrdered:
			c.ordered += n
			doc := s.doc
			doc.Qty = n
			c.orders = append(c.orders, doc)
		case supplyDrafted:
			c.drafted += n
		case supplySkipped:
			c.skipped += n
		}
	}
	return c
}
