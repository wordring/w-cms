package toho

// ─────────────────────────────────────────────────────────────────────────
// 赤字の警告——受注の単価が、見積の単価を下回る（2026-10-17）
//
// 利用者（2026-10-16）:「加工製品ページには見積もり計算表があり、材料や外注加工の価格を集計できます。ここで決まる弊社利益を
// 足した単価が発注書の単価を超えた場合赤字です。赤字は警告して欲しいです」「注文の数量以下でいちばん大きいロットで比べたら
// 良いと思います。」「2か所で良いと思います。」（【考察】全体の設計 §3.5・直す順の7番）。
//
//   - 比べるのは、**見積表の単価**（原価＋利益）と、**受注明細の単価**。見積表の単価が上なら赤字。
//   - 見積表は、その品物の**いちばん新しい見積計算書**のもの（無ければ加工製品ページ・estimateSourceOf）。
//   - ロットは、**受注の数量以下でいちばん大きいロット**。それより小さいロットが無ければ、いちばん小さいロットと比べ、
//     そう添えます（筆者の案・利用者はまだ確かめていない）。同じロットに表面処理の違う見積表が何枚もあれば、
//     **いちばん安い単価**と比べます（どれでも赤字のときだけ言う——言い過ぎない）。
//   - 出す所は2つ: 受注ページの受注明細表の行（⚠ と差額のセル・checksum_mirror.go）と、受注残表の行（薄赤と ⚠・backlog.go）。
//     （2026-10-19 から、案件ページの案件明細表と案件残表。見積の依頼の案件〔`ページ種類：顧客見積依頼`〕には出さない——quoteless・
//     renderOrderChecksum の早期戻り。）
//   - ⚠ **鏡です**（本文に書かない）。見積計算書を直せば、次に開いたときに消えます。
//   - 単価・数量・弊社品番のどれかが読めない行、記録した単価の無い見積表は比べません（黙る——赤字かどうか分からない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"math"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// orderLoss は赤字の見立て1つです。
type orderLoss struct {
	Diff     float64 // 見積の単価 − 受注の単価（正なら赤字）
	Estimate float64 // 比べた見積の単価
	Lot      float64 // 比べたロット
	Fallback bool    // 数量以下のロットが無く、いちばん小さいロットと比べた
	Source   int     // 見積表のあったページ（見積計算書か加工製品ページ）
}

// lossLookup は描画1回ぶんの控えです（同じ品物の見積表を何度も読まない）。
type lossLookup struct {
	db     cms.ReadOnlyDB
	user   *auth.User
	byProd map[int][]estimateLotPrice
	src    map[int]int
}

// estimateLotPrice は見積表の1行（ロットと記録した単価）です。
type estimateLotPrice struct{ Lot, Price float64 }

func newLossLookup(db cms.ReadOnlyDB, user *auth.User) *lossLookup {
	return &lossLookup{db: db, user: user, byProd: map[int][]estimateLotPrice{}, src: map[int]int{}}
}

func (l *lossLookup) records(productID int) ([]estimateLotPrice, int) {
	if v, ok := l.byProd[productID]; ok {
		return v, l.src[productID]
	}
	src := estimateSourceOf(l.user, productID)
	var out []estimateLotPrice
	if rows, err := cms.VocabTableRowsOf(l.db, src, estimateRecordType); err == nil {
		for _, r := range rows {
			lot, okL := estimateNumber(r.Values["lot"])
			price, okP := estimateNumber(r.Values["price"])
			if okL && okP && lot > 0 {
				out = append(out, estimateLotPrice{Lot: lot, Price: price})
			}
		}
	}
	l.byProd[productID], l.src[productID] = out, src
	return out, src
}

// lossOf は、弊社品番 ourItemNo の品物を数量 qty・単価 price で受けたときの赤字の見立てを返します（赤字でなければ ok=false）。
func (l *lossLookup) lossOf(ourItemNo string, qty, price float64) (orderLoss, bool) {
	id, okID := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(ourItemNo), "/"))
	if !okID || qty <= 0 || price <= 0 {
		return orderLoss{}, false
	}
	pid := pageNum(id)
	if !page.CanView(l.user, pid) {
		return orderLoss{}, false
	}
	recs, src := l.records(pid)
	if len(recs) == 0 {
		return orderLoss{}, false
	}
	// 数量以下でいちばん大きいロット。無ければいちばん小さいロット。
	best, fallback := -1.0, false
	for _, r := range recs {
		if r.Lot <= qty && r.Lot > best {
			best = r.Lot
		}
	}
	if best < 0 {
		fallback = true
		for _, r := range recs {
			if best < 0 || r.Lot < best {
				best = r.Lot
			}
		}
	}
	// そのロットのいちばん安い単価。
	est := math.Inf(1)
	for _, r := range recs {
		if r.Lot == best && r.Price < est {
			est = r.Price
		}
	}
	if math.IsInf(est, 1) || est <= price {
		return orderLoss{}, false
	}
	return orderLoss{Diff: est - price, Estimate: est, Lot: best, Fallback: fallback, Source: src}, true
}

// lossText は赤字の見立ての1文です（⚠ と差額・比べた見積の単価とロット）。
func lossText(x orderLoss) string {
	s := "⚠ 赤字 " + comma(int(math.Round(x.Diff))) + "円（見積 " + comma(int(math.Round(x.Estimate))) + "円・" +
		strconv.FormatFloat(x.Lot, 'f', -1, 64) + "個"
	if x.Fallback {
		s += "——数量より小さい個数の見積が無いので、いちばん小さい個数の見積と比べた"
	}
	return s + "）"
}

// appendLossColumn は受注明細表の行の末尾へ「見積との差」のセルを足します（赤字の行が1つも無ければ足さない——雑音にしない）。
func appendLossColumn(ctx *cms.MirrorContext, el *html.Node) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	head := rows[0]
	oi, qi, pi := headerIndexOf(head, OurItemNoTag), headerIndexOf(head, "数量"), headerIndexOf(head, "単価")
	if oi < 0 || qi < 0 || pi < 0 {
		return
	}
	look := newLossLookup(ctx.DB, ctx.Viewer)
	found := map[*html.Node]orderLoss{}
	for _, tr := range rows[1:] {
		qty, okQ := estimateNumber(cellText(tr, qi))
		price, okP := estimateNumber(cellText(tr, pi))
		if !okQ || !okP {
			continue
		}
		if x, ok := look.lossOf(cellText(tr, oi), qty, price); ok {
			found[tr] = x
		}
	}
	if len(found) == 0 {
		return
	}
	priceCell(head, "th", "order-loss").AppendChild(&html.Node{Type: html.TextNode, Data: "見積との差"})
	for _, tr := range rows[1:] {
		x, ok := found[tr]
		td := priceCell(tr, "td", "order-loss")
		if !ok {
			continue
		}
		td.Attr[0].Val += " order-loss-ng"
		td.AppendChild(&html.Node{Type: html.TextNode, Data: lossText(x)})
	}
}

// backlogLossText は受注残表の1行の赤字の見立てです（赤字でなければ空）。
func backlogLossText(l *lossLookup, ourItemNo string, qty int, price float64, hasPrice bool) string {
	if !hasPrice {
		return ""
	}
	if x, ok := l.lossOf(ourItemNo, float64(qty), price); ok {
		return lossText(x)
	}
	return ""
}

// （赤字の行の印〔薄赤・backlog-loss〕は、2026-10-21 から backlog.go の backlogRowAttr が、製造に入った行の打ち消し線と一緒に付けます。）

// lossNoteHTML は品名の後ろに添える赤字の文です（紙には出さない）。
func lossNoteHTML(r backlogRow) string {
	if r.Loss == "" {
		return ""
	}
	return ` <span class="backlog-loss-note no-print">` + stdhtml.EscapeString(r.Loss) + `</span>`
}
