package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページから部材見積依頼・見積計算書を作る（2026-10-19・【考察】製造ページ・作る順の4番の残り）
//
//	2026-10-19 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」
//
//   - 製造明細表の足元の「📨 この製造の部材を見積依頼必要部材表へ」（`/api/mfg/rfq`）——製造明細の行ごとに、その加工製品の部材を
//     ロット＝製造明細の数量で集め、この製造ページの本文の見積依頼必要部材表へ入れる（行の「案件」も写す）。あとはいつもの流れ
//     （選んで見積依頼部材表へ → 部材見積依頼書ページを作る）。部材見積依頼書は記録に作り、明細の「製造」の列にこの製造ページを書く（`/api/rfq/new`・2026-10-21。それまでは製造ページの子）。
//   - 製造明細表の右の「見積計算」の欄（鏡）——この製造の見積計算書（タグ「製造」）へのリンクと「➕」。押すと、その加工製品の
//     新しい見積計算書を記録に作り、タグ「製造」にこの製造ページを書く（`/api/estimate-calc/new` の `mfg`）。見積の値段は、いちばん新しい見積計算書から
//     読むので（estimateSourceOf）、ここで作った見積計算書が「この製造の見積書を作る」の単価になる。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// mfgRFQLines は製造ページ mfgID の製造明細の行ごとに、その加工製品の部材を、ロット＝製造明細の数量で集めます。
// 弊社品番の無い行・部材の表の無い加工製品は飛ばします（skipped に数える）。
func mfgRFQLines(user *auth.User, mfgID int) (lines []ourOrderLine, skipped int, err error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, 0, err
	}
	for _, r := range rows {
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["our-item-id"]), "/"))
		qty, okQ := estimateNumber(r.Values["quantity"])
		if !ok || !okQ || qty < 1 || !page.CanView(user, pageNum(pid)) {
			skipped++
			continue
		}
		ls := rfqLinesOfProduct(pageNum(pid), []int{int(qty)})
		if len(ls) == 0 {
			skipped++
			continue
		}
		if order, okO := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["order"]), "/")); okO {
			for i := range ls {
				ls[i].ForOrder = order
			}
		}
		lines = append(lines, withMfg(ls, page.FormatID(mfgID))...)
	}
	return lines, skipped, nil
}

// MfgRFQAPIHandler は POST /api/mfg/rfq です。入力: {page_id}（製造ページ）。
// この製造の部材を、製造ページの本文の見積依頼必要部材表へ入れます（表が無ければ作る）。
func MfgRFQAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, pageID, ok := gatePagePost(w, r)
	if !ok {
		return
	}
	if !requireMfgPage(w, pageID) {
		return
	}
	lines, skipped, err := mfgRFQLines(user, pageNum(pageID))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "製造明細を読めません: "+err.Error())
		return
	}
	if len(lines) == 0 {
		msg := "この製造で見積を頼む部材はありません（加工製品の部材の表に行がありません）"
		if skipped > 0 {
			msg += "。弊社品番の無い行・部材の表の無い行が " + strconv.Itoa(skipped) + " 行あります"
		}
		cms.JSONFail(w, http.StatusBadRequest, msg)
		return
	}
	added := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		var out string
		out, added = appendRFQNeeds(cur, lines)
		return out
	}) {
		return
	}
	auth.Audit(user.Username, "mfg.rfq", pageID+" "+strconv.Itoa(added)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": added, "skipped": skipped})
}

// mfgParentOr は、page_id が書ける製造ページならその番号を、そうでなければ空を返します（作る書類の「製造」の列に書くか）。
func mfgParentOr(user *auth.User, id string) string {
	pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(id), "/"))
	if !ok || pageKindOf(pageNum(pid)) != PageKindMfg || !canWritePage(user, pageNum(pid)) {
		return ""
	}
	return pid
}

// withMfg は、行の「製造」が空なら mfg（製造ページの番号・空なら何もしない）を入れます（2026-10-21・書類は記録に置き、製造ページとは列でつながる）。
// 人が書いた「製造」は上書きしません。
func withMfg(lines []ourOrderLine, mfg string) []ourOrderLine {
	if mfg == "" {
		return lines
	}
	for i := range lines {
		if strings.TrimSpace(lines[i].Mfg) == "" {
			lines[i].Mfg = mfg
		}
	}
	return lines
}

// calcsUnder は、加工製品 productID の見積計算書のうち、製造ページ parent の分（タグ「製造」・それより前は子）のもの（新しい順）を返します。
func calcsUnder(user *auth.User, productID int, parent string) []estimateCalc {
	var out []estimateCalc
	mfg := pageNum(parent)
	for _, c := range estimateCalcsOf(user, productID) {
		if v := cms.PageTagValue(database.DB, c.PageID, MfgRefTag); strings.TrimSpace(v) != "" {
			// 2026-10-21 から、見積計算書はタグ「製造」で製造ページを指す（置き場は記録）。
			if refersTo(v, mfg) {
				out = append(out, c)
			}
			continue
		}
		if childOf(c.PageID, mfg) { // それまでの形——製造ページの子
			out = append(out, c)
		}
	}
	return out
}

// appendMfgCalcColumn は製造明細表の右に「見積計算」の欄を足します（鏡）——この製造の見積計算書へのリンクと「➕」。
func appendMfgCalcColumn(ctx *cms.MirrorContext, el *html.Node, writable bool) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return
	}
	pi := headerIndexOf(rows[0], OurItemNoTag)
	priceCell(rows[0], "th", "mfg-calc").AppendChild(&html.Node{Type: html.TextNode, Data: "見積計算"})
	mfg := page.FormatID(ctx.PageID)
	for _, tr := range rows[1:] {
		td := priceCell(tr, "td", "mfg-calc")
		if pi < 0 {
			continue
		}
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(cellText(tr, pi)), "/"))
		if !ok || ctx.Viewer == nil || !page.CanView(ctx.Viewer, pageNum(pid)) {
			continue
		}
		var b strings.Builder
		if list := calcsUnder(ctx.Viewer, pageNum(pid), mfg); len(list) > 0 {
			label := "🧮 /" + page.FormatID(list[0].PageID)
			if list[0].Date != "" {
				label += "（" + list[0].Date + "）"
			}
			b.WriteString(`<a href="/` + page.FormatID(list[0].PageID) + `">` + stdhtml.EscapeString(label) + `</a> `)
		}
		if writable {
			b.WriteString(`<button type="button" class="chip-btn estimate-calc-new" data-ecalc-new="` + pid + `" data-mfg="` + mfg +
				`" title="この加工製品の新しい見積計算書を作ります（置き場は記録の年月・この製造ページとはタグ「製造」でつながる）">➕ 見積計算書</button><span class="estimate-calc-say"></span>`)
		}
		if b.Len() > 0 {
			appendRawChrome(td, b.String())
		}
	}
}
