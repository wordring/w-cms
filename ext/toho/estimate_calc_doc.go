package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積計算書——見積計算を記録のページに置く（2026-10-16）
//
// 利用者:「いまは見積もり計算の記録を加工製品のページに持っていますが、見積もり計算は変わっていくものなので、記録ページに
// 記録すべきもので、加工製品ページには（読み書き量対応の）鏡が必要なのかもしれません」「鏡は、最新の計算や過去の価格を
// 見るのは必須です。書き込みは別の鑑かもしくは、新しい見積計算書へのリンクになると思います。今はまだ決められませんが、、
// 新しい見積計算書を作るボタンから始めてみましょう」「見積もり計算の記録は製造記録傘下になると思います」
// （【考察】全体の設計 §4.5・直す順の6番）。
//
//   - **見積計算書**は1回の計算です。置き場は `記録／年／月／製造記録`（月は作った日）、題は「【弊社】見積計算書　<品物の題>」。
//     タグは `ページ種類：弊社見積計算書`・`弊社品番`（加工製品ページへの参照）・`見積計算日`（テンプレート「見積計算書」が持つ）。
//   - 中身は加工製品ページと同じ形（見出し「見積もり計算」・ロットごとの見積計算表と見積表）——鏡と「📌 この単価を記録」
//     「⧉ このロットを写す」は、置かれたページの表だけを扱うので、そのまま働きます（estimate.go）。
//   - **「新しい見積計算書を作る」**は、その品物の**いちばん新しい見積計算書**を写します。まだ無ければ、加工製品ページの
//     見出し「見積もり計算」の節（次の H2 の手前まで）を写します。⚠ **加工製品ページの見積計算表は消しません**
//     （利用者:「合わないもの・受注が無いものは、加工製品ページに今のまま残します」——§4.5 の今の見積計算表を移す決まり）。
//   - 加工製品ページの「🧮 見積計算書」の鏡が、最新の計算と過去の計算（ロットと単価）を並べます（受注・見積の単価の上）。
//
// まだのもの（§4.5）: 書き込みの鏡、見積書を作る・赤字の警告・「計算表へ」の読み先を見積計算書へ替えること、受注した価格と
// 合う今の見積計算表を記録へ移すこと。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// EstimateCalcTemplate は見積計算書ページのテンプレートの題です。
	EstimateCalcTemplate = "見積計算書"
	// EstimateCalcDateTag は見積計算書の計算した日のタグです（見積書の `見積日` とは別）。
	EstimateCalcDateTag = "見積計算日"
	// PageKindOurEstimateCalc は見積計算書の `ページ種類` の値です。
	PageKindOurEstimateCalc = "弊社見積計算書"
	// estimateSectionHeading は加工製品ページで見積計算表を並べる見出し（H2）です（2026-10-15 の形）。
	estimateSectionHeading = "見積もり計算"
)

func init() {
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     EstimateCalcTemplate,
		Extension: "toho",
		Why: "加工製品ページの「➕ 新しい見積計算書を作る」が、見積計算書ページ（記録／年／月／製造記録）を作るときに写します。" +
			"タグ ページ種類（弊社見積計算書）・弊社品番・見積計算日を持たせてください（値は機械が入れます）。中身の見積計算表と見積表は、" +
			"その品物のいちばん新しい見積計算書か、加工製品ページの見出し「見積もり計算」の節から写します。",
	})
}

// estimateCalc は見積計算書ページ1枚です。
type estimateCalc struct {
	PageID int
	Title  string
	Date   string
	Lots   []estimateCalcLot
}

// estimateCalcLot は見積計算書の見積表の1行（ロットと単価）です。
type estimateCalcLot struct {
	Lot, Surface, Price string
}

// estimateCalcsOf は、加工製品 productID の見積計算書（読めるものだけ）を新しい順に返します（見積計算日・ページ番号の順）。
func estimateCalcsOf(user *auth.User, productID int) []estimateCalc {
	db := database.DB
	want := page.FormatID(productID)
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない・:memory: の罠）。
	rows, err := db.Query(`SELECT t.page_id FROM page_tags t
		  JOIN page_tags k ON k.page_id = t.page_id AND k.name = ? AND k.value = ?
		 WHERE t.name = ? AND t.value = ?`, PageKindTag, PageKindOurEstimateCalc, OurItemNoTag, want)
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	canView := viewCheck(user)
	var out []estimateCalc
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] || !canView(id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		seen[id] = true
		tags, _ := cms.TagsOfPage(db, id)
		c := estimateCalc{PageID: id, Title: cms.PageTitleByID(id), Date: strings.TrimSpace(cms.FirstTag(tags, EstimateCalcDateTag))}
		if recs, err := cms.VocabTableRowsOf(db, id, estimateRecordType); err == nil {
			for _, r := range recs {
				c.Lots = append(c.Lots, estimateCalcLot{Lot: strings.TrimSpace(r.Values["lot"]),
					Surface: strings.TrimSpace(r.Values["surface"]), Price: strings.TrimSpace(r.Values["price"])})
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].PageID > out[j].PageID
	})
	return out
}

// estimateCalcsHTML は加工製品ページの「🧮 見積計算書」の鏡です（最新の計算と過去の計算・新しく作るボタン）。
func estimateCalcsHTML(productID int, list []estimateCalc) string {
	esc := stdhtml.EscapeString
	var b strings.Builder
	b.WriteString(`<div class="vocab-chrome estimate-calcs" data-ecalc-product="` + page.FormatID(productID) + `">`)
	b.WriteString(`<h3 class="materials-title">🧮 見積計算書</h3>`)
	lots := func(c estimateCalc) string {
		if len(c.Lots) == 0 {
			return `<span class="materials-empty">（見積表がありません）</span>`
		}
		var parts []string
		for _, l := range c.Lots {
			s := "ロット" + esc(l.Lot)
			if l.Surface != "" {
				s += "（" + esc(l.Surface) + "）"
			}
			price := l.Price
			if price == "" {
				price = "（未記録）"
			} else {
				price = esc(price) + "円"
			}
			parts = append(parts, s+": <strong>"+price+"</strong>")
		}
		return strings.Join(parts, "・")
	}
	if len(list) == 0 {
		b.WriteString(`<p class="materials-empty">見積計算書はまだありません（このページの見積計算表は、加工製品ページに置いた計算です）。</p>`)
	} else {
		latest := list[0]
		date := latest.Date
		if date == "" {
			date = "（日付なし）"
		}
		b.WriteString(`<p class="estimate-calc-latest">最新: <a href="/` + page.FormatID(latest.PageID) + `">` + esc(date) + `</a>　` + lots(latest) + `</p>`)
		if len(list) > 1 {
			b.WriteString(`<table class="materials-table estimate-calc-table"><thead><tr><th>見積計算日</th><th>ロットと単価</th></tr></thead><tbody>`)
			for _, c := range list[1:] {
				d := c.Date
				if d == "" {
					d = "（日付なし）"
				}
				b.WriteString(`<tr><td><a href="/` + page.FormatID(c.PageID) + `">` + esc(d) + `</a></td><td>` + lots(c) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table>`)
		}
	}
	src := "いちばん新しい見積計算書"
	if len(list) == 0 {
		src = "このページの「" + estimateSectionHeading + "」"
	}
	b.WriteString(`<div class="matsearch-form no-print"><button type="button" class="estimate-calc-new" data-ecalc-new="` + page.FormatID(productID) +
		`">➕ 新しい見積計算書を作る</button> <span class="estimate-calc-say"></span>` +
		`<span class="unorder-help">（` + esc(src) + `を写して、記録／年／月／製造記録 に作ります）</span></div>`)
	b.WriteString(`</div>`)
	return b.String()
}

// ── 作る口 ─────────────────────────────────────────────────────────────

var h2Re = regexp.MustCompile(`(?is)<h2\b[^>]*>(.*?)</h2>`)

// estimateSectionOf は加工製品ページの本文から、見出し「見積もり計算」（H2）から次の H2 の手前までを返します（無ければ空）。
func estimateSectionOf(body string) string {
	locs := h2Re.FindAllStringSubmatchIndex(body, -1)
	for i, l := range locs {
		if strings.TrimSpace(cms.NormalizeText(stripTags(body[l[2]:l[3]]))) != estimateSectionHeading {
			continue
		}
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return body[l[0]:end]
	}
	return ""
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// stripTags は HTML の字だけを返します（見出しの文字を比べるため）。
func stripTags(s string) string { return stdhtml.UnescapeString(tagRe.ReplaceAllString(s, "")) }

var firstTagListRe = regexp.MustCompile(`(?is)^\s*<dl\b[^>]*data-type="tags"[^>]*>.*?</dl>`)

// calcContentOf は見積計算書の本文から、題と、題のすぐ下のタグを除いた中身を返します。
func calcContentOf(body string) string {
	if loc := regexp.MustCompile(`(?is)<h1\b[^>]*>.*?</h1>`).FindStringIndex(body); loc != nil {
		body = body[loc[1]:]
	}
	return firstTagListRe.ReplaceAllString(body, "")
}

// EstimateCalcNewAPIHandler は POST /api/estimate-calc/new です。入力: {product}。新しい見積計算書を作り、そのページIDを返します。
func EstimateCalcNewAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Product string `json:"product"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pid, okID := page.NormalizeID(strings.TrimSpace(req.Product))
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "加工製品のページ番号が読めません")
		return
	}
	pidInt := pageNum(pid)
	if !page.CanView(user, pidInt) {
		cms.JSONFail(w, http.StatusNotFound, "加工製品のページがありません")
		return
	}
	if !isProductPage(pidInt) {
		cms.JSONFail(w, http.StatusBadRequest, "加工製品のページ（"+PageKindTag+"："+PageKindProduct+"）ではありません")
		return
	}
	// 写す中身——いちばん新しい見積計算書、無ければ加工製品ページの「見積もり計算」の節。
	content := ""
	from := ""
	if list := estimateCalcsOf(user, pidInt); len(list) > 0 {
		body, err := cms.ReadPageBody(page.FormatID(list[0].PageID))
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "見積計算書を読めません: "+err.Error())
			return
		}
		content, from = calcContentOf(body), page.FormatID(list[0].PageID)
	} else {
		body, err := cms.ReadPageBody(pid)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "加工製品のページを読めません: "+err.Error())
			return
		}
		content, from = estimateSectionOf(body), pid
	}
	if strings.TrimSpace(content) == "" {
		content = "<h2>" + estimateSectionHeading + "</h2><p><br/></p>"
	}
	tmpl, err := cms.PageTemplateBody(EstimateCalcTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, err.Error())
		return
	}
	now := time.Now()
	d := cms.NewPageDraft(EstimateCalcTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindOurEstimateCalc, "見積計算書　"+cms.PageTitleByID(pidInt)))
	d.SetTag(OurItemNoTag, pid)
	d.SetTag(EstimateCalcDateTag, now.Format("2006-01-02"))
	d.SetTag(PageKindTag, PageKindOurEstimateCalc) // テンプレートも持っているが、機械が作る種類なので値を確かに書く
	body := d.HTML() + content
	monthID, err := recordFolder(user.Username, RecordWorkFolder, now)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+recordPath(RecordWorkFolder, now)+"」を用意できません: "+err.Error())
		return
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, body)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積計算書を作れません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "estimate-calc.new", newID+" for "+pid+" from "+from)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "from": from})
}

// ── 読む先（2026-10-17）─────────────────────────────────────────────────
//
// 見積の値段を読む所は、**その品物のいちばん新しい見積計算書**を読みます。無ければ、いままでどおり加工製品ページ
// （利用者:「合わないもの・受注が無いものは、加工製品ページに今のまま残します」——移していない品物はそのまま動く）。
//   - 見積書を作る（装置フォルダから・estimate_folder.go）の一覧と作る口
//   - 「💴 見積回答」の「計算表へ」（estimate_put.go の EstimateRowsAPIHandler——写す先のページを返す）
//   - 再見積依頼のロット（rfq_api.go の estimateLotsOf の呼び手）
// 見積表の「見積書に入れる」は、**押した表のあるページ**のままです（見ている表から入れる）。

// estimateSourceOf は、加工製品 productID の見積を読むページを返します——いちばん新しい見積計算書、無ければ productID。
func estimateSourceOf(user *auth.User, productID int) int {
	if list := estimateCalcsOf(user, productID); len(list) > 0 {
		return list[0].PageID
	}
	return productID
}

// productOfEstimatePage は、見積表のあるページが表す加工製品のページ番号を返します——見積計算書なら `弊社品番` のページ、
// それ以外（加工製品ページ）はそのページ。
func productOfEstimatePage(pageID int) int {
	if pageKindOf(pageID) != PageKindOurEstimateCalc {
		return pageID
	}
	tags, err := cms.TagsOfPage(database.DB, pageID)
	if err != nil {
		return pageID
	}
	if id, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(cms.FirstTag(tags, OurItemNoTag)), "/")); ok {
		return pageNum(id)
	}
	return pageID
}
