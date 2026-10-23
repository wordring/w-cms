package toho

// 製造ページの「この製造の書類」（2026-10-21・【考察】製造単位の部材発注 §6.2）。
//
// 2026-10-21 利用者:「製造ページが作る書類は、記録以下に保存し、製造ページはそこへのリンクとミラーの簡易表示を持ち」
//
// 製造ページから作る書類（見積計算書・見積書・部材見積依頼書・部材発注書・納品書）は、記録の年月に置きます（§6.1）。
// 製造ページの製造明細表の足元に、この製造の書類を種類ごとに並べます（保存しない、表示のたびに引く欄）。
//   - 「この製造の書類」は、明細の「製造」の列がこの製造ページを指す書類（見積計算書はタグ「製造」）と、
//     2026-10-20 までに製造ページの子に作った書類です。
//   - 1行は、書類へのリンク・相手（仕入先・客先・見積先）・作った日・状態（送った・発行した など）です。
//   - 見られない書類は出しません。テンプレートの中も出しません。

import (
	stdhtml "html"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// mfgDocKind は「この製造の書類」に並べる書類の種類です（並べる順）。
type mfgDocKind struct {
	Kind      string // ページ種類
	ItemsType string // 明細表の形式（空なら明細表が無い——タグ「製造」で引く）
	Icon      string
}

func mfgDocKinds() []mfgDocKind {
	return []mfgDocKind{
		{Kind: PageKindOurEstimateCalc, Icon: "🧮"},
		{Kind: PageKindOurEstimate, ItemsType: EstimateItemsType, Icon: "📄"},
		{Kind: PageKindOurRFQ, ItemsType: RFQItemsType, Icon: "📨"},
		{Kind: PageKindOurPurchase, ItemsType: ourOrderItemsType, Icon: "📦"},
		{Kind: PageKindOurDelivery, ItemsType: DeliveryItemsType, Icon: "🚚"},
	}
}

// mfgDoc は「この製造の書類」の1行です。
type mfgDoc struct {
	PageID  int
	Kind    string
	Icon    string
	Title   string
	Party   string // 仕入先・客先・見積先
	Created string // 作った日（YYYY-MM-DD・この機械の時刻）
	State   string // 状態の短い言葉
}

// mfgDocs は、製造ページ mfg の書類を、種類の順・作った日の順に返します（user が見られるものだけ）。
func mfgDocs(user *auth.User, mfg int) []mfgDoc {
	if user == nil {
		return nil
	}
	kinds := mfgDocKinds()
	ids := map[int]bool{}
	for _, k := range kinds {
		if k.ItemsType == "" {
			if found, err := cms.PagesByTag(database.DB, MfgRefTag, page.FormatID(mfg)); err == nil {
				for _, id := range found {
					ids[id] = true
				}
			}
			continue
		}
		rows, err := cms.VocabRowsOfType(database.DB, k.ItemsType)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if !ids[r.PageID] && refersTo(r.Values["mfg"], mfg) {
				ids[r.PageID] = true
			}
		}
	}
	// 2026-10-20 までに製造ページの子に作った書類。
	if kids, err := cms.ChildPages(database.DB, mfg); err == nil {
		for _, c := range kids {
			ids[c.ID] = true
		}
	}
	order := map[string]int{}
	icon := map[string]string{}
	for i, k := range kinds {
		order[k.Kind] = i
		icon[k.Kind] = k.Icon
	}
	canView := viewCheck(user)
	var out []mfgDoc
	for id := range ids {
		kind := pageKindOf(id)
		if _, ok := order[kind]; !ok || !canView(id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		out = append(out, describeMfgDoc(id, kind, icon[kind]))
	}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].PageID < out[j].PageID
	})
	return out
}

// describeMfgDoc は書類1枚の、相手・作った日・状態を読みます。
func describeMfgDoc(id int, kind, icon string) mfgDoc {
	d := mfgDoc{PageID: id, Kind: kind, Icon: icon, Title: cms.PageTitleByID(id)}
	if meta, ok := page.ReadSidecar(page.FormatID(id)); ok && meta.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, meta.CreatedAt); err == nil {
			d.Created = t.In(time.Local).Format("2006-01-02")
		}
	}
	tags, _ := cms.TagsOfPage(database.DB, id)
	first := func(name string) string { return strings.TrimSpace(cms.FirstTag(tags, name)) }
	switch kind {
	case PageKindOurEstimateCalc:
		if v := first(EstimateCalcDateTag); v != "" {
			d.State = "見積計算日 " + v
		}
	case PageKindOurEstimate:
		d.Party = first(EstimateClientTag)
		switch {
		case first(EstimateSentTag) != "":
			d.State = "送付 " + first(EstimateSentTag)
		case first(EstimateNoSendTag) != "":
			d.State = "送付不要"
		default:
			d.State = "未送付"
		}
	case PageKindOurRFQ:
		d.Party = first(SupplierTag)
		switch {
		case first(RFQAnsweredTag) != "":
			d.State = "回答 " + first(RFQAnsweredTag)
		case first(EstimateSentTag) != "":
			d.State = "送付 " + first(EstimateSentTag)
		default:
			d.State = "未送付"
		}
	case PageKindOurPurchase:
		d.Party = first(SupplierTag)
		c := OrderSendStateOf(database.DB, id)
		switch {
		case c.Total == 0 && c.Cancelled > 0:
			d.State = "全部取消"
		case c.Total == 0:
			d.State = "明細なし"
		case c.Sent == 0:
			d.State = "未送付"
		case c.Sent < c.Total:
			d.State = "一部発注済"
		default:
			d.State = "発注済"
			if v := first(PurchasedAtTag); v != "" {
				d.State += " " + v
			}
		}
	case PageKindOurDelivery:
		d.Party = first(DeliveryClientTag)
		if v := first(DeliveryIssuedTag); v != "" {
			d.State = "発行 " + v
		} else {
			d.State = "未発行"
		}
	}
	return d
}

// mfgDocsHTML は「この製造の書類」の欄の中身です（製造明細表の足元の行に入れる・エスケープ済み）。
func mfgDocsHTML(docs []mfgDoc) string {
	var b strings.Builder
	b.WriteString(`<div class="mfg-docs"><strong>この製造の書類</strong>`)
	if len(docs) == 0 {
		b.WriteString(`<span class="mfg-docs-none">（まだありません）</span></div>`)
		return b.String()
	}
	b.WriteString(`<ul>`)
	for _, d := range docs {
		id := page.FormatID(d.PageID)
		title := d.Title
		if strings.TrimSpace(title) == "" {
			title = "/" + id
		}
		b.WriteString(`<li>` + d.Icon + ` <a href="/` + id + `">` + stdhtml.EscapeString(title) + `</a>`)
		var bits []string
		for _, s := range []string{d.Party, d.Created, d.State} {
			if strings.TrimSpace(s) != "" {
				bits = append(bits, stdhtml.EscapeString(s))
			}
		}
		if len(bits) > 0 {
			b.WriteString(`<span class="mfg-docs-meta">　` + strings.Join(bits, "・") + `</span>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// mfgDocHeading は、書類の種類を作る作業の見出しです（2026-10-21 夜・appendMfgPageEnd）。
// 見積書は製造明細表の「📄 見積書」で作るので「作業開始」、部材見積依頼書は「見積依頼作業」、部材発注書は「発注作業」、
// 納品書は「納品作業」。見積計算書は「🧮 見積もり計算作業」で作り、見出し「見積もり計算作業」（2026-10-23 までは「作業開始」）。
func mfgDocHeading(kind string) string {
	switch kind {
	case PageKindOurEstimate:
		return MfgHeadStart
	case PageKindOurEstimateCalc:
		return MfgHeadCalc
	case PageKindOurRFQ:
		return MfgHeadQuote
	case PageKindOurPurchase:
		return MfgHeadOrder
	case PageKindOurDelivery:
		return MfgHeadDelivery
	}
	return ""
}

// hasTopHeading は、root の直下に h2「heading」があるかです。
func hasTopHeading(root *html.Node, heading string) bool {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if headingLevel(c) == 2 && strings.TrimSpace(textOf(c)) == heading {
			return true
		}
	}
	return false
}

// insertAtSectionEnd は、root の直下の h2「heading」の節の末尾（次の h1・h2 の前）に n を置きます（placeUnderHeading の木の版）。
func insertAtSectionEnd(root *html.Node, heading string, n *html.Node) {
	var h *html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if headingLevel(c) == 2 && strings.TrimSpace(textOf(c)) == heading {
			h = c
			break
		}
	}
	if h == nil {
		root.AppendChild(n)
		return
	}
	at := h.NextSibling
	for at != nil {
		if l := headingLevel(at); l == 1 || l == 2 {
			break
		}
		at = at.NextSibling
	}
	root.InsertBefore(n, at)
}
