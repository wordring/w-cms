package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品ページの「見積回答」（2026-10-04）
//
// 利用者:「進めてください」（筆者の勧め——加工製品ページに「貰った見積」の一覧を置き、そのページの見積依頼明細の行を業者・
// 日付・ロット・単価で並べる）。業者の値段は見積依頼明細の単価の列1か所にそろえた（過去の見積もりを移したものも、これから
// 届く返事も）ので、ここはそれを**加工製品の側から引いて並べるだけ**（鏡——本文には書かない）。
// 見出しは同じ日に「見積回答」へ改めた（利用者:「ほかと衝突が無ければ、貰った見積もりの見出しを見積回答に変えたい」——
// コード・設定・文書に同じ語は無く、見積依頼明細の状態〔未回答・回答あり〕・回答日と言葉がそろう）。
//
//   - 置き場所は**部品の表の最後（支給部品の表）の下**——部品の表（材料・外注加工・購入部品・支給部品）の鏡（drawing_mirror.go）に
//     相乗りする。ページに支給部品の表が無ければ出ない（テンプレートには在る）。
//   - 並べるのは、弊社品番がこのページの行（読める見積依頼書ページだけ）。新しい順（回答日、無ければ見積依頼日）。
//     返事の来ていない行（未回答）も「回答待ち」として並べる——依頼中だと分かるように。辞退は「辞退」。
//   - 相見積もりは1つにまとめない（業者ごとに1行——【要求】見積依頼 §3「買ってもいない会社の値段が最新単価を上書きしないように」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// quoteRow は「見積回答」の1行です。
type quoteRow struct {
	RFQPage int
	Date    string // 回答日（無ければ見積依頼日）
	Vendor  string
	Kind    string
	What    string // 何の値段か（加工内容・材質 形状 寸法・品名）
	Color   string
	Lot     string
	Price   string
	Status  string
}

// quotesForProduct は、弊社品番が productID の見積依頼明細の行を新しい順に返します（読める見積依頼書ページだけ）。
func quotesForProduct(db cms.ReadOnlyDB, user *auth.User, productID int) []quoteRow {
	rows, err := cms.VocabRowsOfType(db, RFQItemsType)
	if err != nil {
		return nil
	}
	want := page.FormatID(productID)
	canView := viewCheck(user)
	heads := map[int]map[string][]string{}
	var out []quoteRow
	for _, r := range rows {
		if strings.TrimSpace(r.Values["our-item-id"]) != want || !canView(r.PageID) || cms.IsTemplateArea(page.FormatID(r.PageID)) {
			continue
		}
		h, ok := heads[r.PageID]
		if !ok {
			h, _ = cms.TagsOfPage(db, r.PageID)
			heads[r.PageID] = h
		}
		date := strings.TrimSpace(cms.FirstTag(h, RFQAnsweredTag))
		if date == "" {
			date = strings.TrimSpace(cms.FirstTag(h, RFQDateTag))
		}
		v := r.Values
		what := strings.TrimSpace(v["work"])
		if spec := strings.TrimSpace(strings.Join([]string{v["material"], v["shape"], v["size"]}, " ")); spec != "" {
			what = strings.TrimSpace(what + " " + spec)
		}
		if what == "" {
			what = strings.TrimSpace(v["item-name"])
		}
		out = append(out, quoteRow{RFQPage: r.PageID, Date: date, Vendor: strings.TrimSpace(cms.FirstTag(h, SupplierTag)),
			Kind: strings.TrimSpace(v["kind"]), What: what, Color: strings.TrimSpace(v["color"]),
			Lot: strings.TrimSpace(v["quantity"]), Price: strings.TrimSpace(v["cost"]), Status: strings.TrimSpace(v["status"])})
	}
	// 日付は暦で並べる（`2024/1/5` と `2024-01-22` が混ざっても）——読めない日付は後ろ。
	key := func(d string) string {
		if n, ok := cms.NormalizeValue(cms.ColDate, d); ok {
			return n
		}
		return ""
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := key(out[i].Date), key(out[j].Date); a != b {
			return a > b
		}
		return out[i].Vendor < out[j].Vendor
	})
	return out
}

// quotesListHTML は「見積回答」を描きます（行が無ければ短い断り）。
func quotesListHTML(list []quoteRow) string {
	var b strings.Builder
	b.WriteString(`<div class="vocab-chrome rfq-quotes" contenteditable="false"><p class="materials-title">💴 見積回答</p>`)
	if len(list) == 0 {
		b.WriteString(`<p class="materials-empty">まだありません（見積依頼書に業者の返事〔単価〕が入ると、ここに並びます）。</p></div>`)
		return b.String()
	}
	b.WriteString(`<div class="rfq-quotes-scroll"><table class="materials-table rfq-quotes-table"><thead><tr><th>日付</th><th>業者</th><th>種類</th><th>何の値段か</th>` +
		`<th>表面</th><th class="num">ロット</th><th class="num">単価</th><th>状態</th></tr></thead><tbody>`)
	for _, q := range list {
		status := q.Status
		switch status {
		case rfqLineUnanswered:
			status = "回答待ち"
		case "":
			status = "—"
		}
		price := q.Price
		if price != "" && !strings.HasSuffix(price, "円") {
			price += "円"
		}
		// 日付がそのまま見積依頼書へのリンク（列を1つ減らす——欄は最大 666px）。日付が無ければページ番号。
		id := page.FormatID(q.RFQPage)
		label := q.Date
		if label == "" {
			label = "/" + id
		}
		b.WriteString(`<tr><td><a href="/` + id + `">` + stdhtml.EscapeString(label) + `</a></td><td>` + stdhtml.EscapeString(q.Vendor) + `</td><td>` +
			stdhtml.EscapeString(q.Kind) + `</td><td class="what">` + stdhtml.EscapeString(q.What) + `</td><td>` + stdhtml.EscapeString(q.Color) +
			`</td><td class="num">` + stdhtml.EscapeString(q.Lot) + `</td><td class="num">` + stdhtml.EscapeString(price) +
			`</td><td>` + stdhtml.EscapeString(status) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

// appendQuotesList は支給部品の表の下に「見積回答」を出します（1ページに1回）。表を包む節があれば節の末尾、
// 無ければ表の直後——⚠ 表の中へ `<div>` を入れない（HTMLパーサが表の外へ追い出す）。
func appendQuotesList(ctx *cms.MirrorContext, tbl *html.Node) {
	if ctx.Counter("rfq-quotes") > 0 || cms.IsTemplateArea(page.FormatID(ctx.PageID)) {
		return
	}
	frag := quotesListHTML(quotesForProduct(ctx.DB, ctx.Viewer, ctx.PageID))
	if p := tbl.Parent; p != nil && p.Data == "section" {
		appendHTML(p, frag)
		return
	}
	if tbl.Parent == nil {
		return
	}
	nodes, err := htmldoc.ParseFragment(frag)
	if err != nil {
		return
	}
	next := tbl.NextSibling
	for _, n := range nodes {
		tbl.Parent.InsertBefore(n, next)
	}
}
