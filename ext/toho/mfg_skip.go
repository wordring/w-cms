package toho

// 手配不要——製造ページの中で、要らない部材を手配から外す（2026-10-21・【考察】製造単位の部材発注 §6.3 の7番）。
//
// 2026-10-21 利用者（§4 問い5「手配不要の置き場: 製造ページの中（製造ごと）でよいですか」に）:「良いです、」
//
//   - 製造ページの発注部材表の行の「🚫 不要」で、その行を、この製造ページの「手配不要表」へ移します（理由と日付つき）。
//     在庫で足りる・自社で作る・電話で買った などで、この製造のぶんはもう買わない、という記録です。
//   - 手配済みの数え方（procure_ledger.go）は、手配不要表の行も発注部材表と同じく引きます（内訳があれば案件と加工製品ごと）。
//     だから「📦 この製造の部材を発注部材表へ」を押し直しても、また出てきません。
//   - 手配不要表の行の「↩ 戻す」で外すと、また発注部材表へ出せるようになります（数え方は毎回計算するので、外せば戻る）。
//   - 2026-10-01〜10-21 朝の手配不要表（トップ直下の「製造」にあった・必要部材表から入れた）と同じ形式・同じ列です。
//     置き場が製造ページに替わり、入れる道が発注部材表の行になりました。

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// SkipType は手配不要表の形式です。
const SkipType = "procure-skip"

func init() {
	cms.RegisterMirror(SkipType, cms.MirrorHandlerFunc(renderSkipTable))
}

// skipColumns は手配不要表の列です——発注の列のうち部材を言い当てるもの（単価・状態・仕様・支給・備考は除く）と、理由・日付。
func skipColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range orderItemColumns() {
		switch c.Field {
		case "cost", "status", "spec", "supplied", "note":
			continue
		}
		out = append(out, c)
	}
	return append(out,
		cms.VocabColumn{Field: "reason", Label: "理由", Type: cms.ColText},
		cms.VocabColumn{Field: "date", Label: "日付", Type: cms.ColDate})
}

// skipLineValue は手配不要表のセルの値です（理由・日付のほかは発注の行と同じ）。
func skipLineValue(ln ourOrderLine, field, reason, date string) string {
	switch field {
	case "reason":
		return reason
	case "date":
		return date
	}
	return orderLineValue(ln, field)
}

// addSkipRows は本文の手配不要表へ行を足します（無ければ、見出し「部材作業」の下に作る・見出しも無ければ末尾に足す）。
func addSkipRows(body string, lines []ourOrderLine, reason, date string) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	tables := tablesOfType(nodes, SkipType)
	if len(tables) == 0 {
		var b strings.Builder
		b.WriteString(`<table><caption>` + stdhtml.EscapeString(displayNameOf(SkipType)) + `</caption><tbody>` + headerRowHTML(SkipType))
		for _, ln := range lines {
			b.WriteString(`<tr>`)
			for _, c := range columnsOf(SkipType) {
				b.WriteString(`<td>` + stdhtml.EscapeString(skipLineValue(ln, c.Field, reason, date)) + `</td>`)
			}
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</tbody></table>`)
		return placeUnderHeading(body, MfgHeadParts, b.String()), len(lines) // 「部材作業」の見出しの下（2026-10-21 夕）
	}
	table := tables[0]
	tbody := lastChild(table, "tbody")
	if tbody == nil {
		tbody = table
	}
	fields := fieldsOfHeader(table, SkipType)
	for _, ln := range lines {
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for _, f := range fields {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			if v := skipLineValue(ln, f, reason, date); v != "" {
				td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
			}
			tr.AppendChild(td)
		}
		tbody.AppendChild(tr)
	}
	return htmldoc.Render(nodes), len(lines)
}

// takeSkipRow は手配不要表の row 行目（見出しを除く・1始まり）を外します。最後の1行なら表ごと消す。
func takeSkipRow(body string, row int) (string, bool) {
	if row < 1 {
		return body, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, SkipType)
	if len(tables) == 0 {
		return body, false
	}
	table := tables[0]
	rows := rowsOf(table)
	if row >= len(rows) {
		return body, false
	}
	rows[row].Parent.RemoveChild(rows[row])
	if len(rowsOf(table)) <= 1 {
		return spliceNodes(nodes, table, nil, false)
	}
	return htmldoc.Render(nodes), true
}

// MfgSkipAPIHandler は POST /api/mfg/skip です。入力: {page_id, table, row, reason}——page_id は製造ページ、
// table は何枚目の発注部材表か（1始まり）、row は見出しを除いた何行目か（1始まり）。その行を手配不要表へ移します。
func MfgSkipAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Table  int    `json:"table"`
		Row    int    `json:"row"`
		Reason string `json:"reason"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if !requireMfgPage(w, pageID) {
		return
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	date := time.Now().Format("2006-01-02")
	done := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, ln, ok := takeDraftRow(cur, req.Table, req.Row)
		if !ok {
			return cur
		}
		out, _ = addSkipRows(out, []ourOrderLine{ln}, reason, date)
		done = true
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict, "その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "mfg.skip", pageID+" 表"+strconv.Itoa(req.Table)+" 行"+strconv.Itoa(req.Row)+" "+reason)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// MfgSkipRemoveAPIHandler は POST /api/mfg/skip/remove です。入力: {page_id, row}——手配不要表の row 行目を外します。
func MfgSkipRemoveAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	done := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, ok := takeSkipRow(cur, req.Row)
		done = ok
		if !ok {
			return cur
		}
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict, "その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "mfg.skip-remove", pageID+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// renderSkipTable は手配不要表の行に「↩ 戻す」を足します（書ける人にだけ・鏡なので本文には残らない）。
func renderSkipTable(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	if ctx.Viewer == nil || !canWritePage(ctx.Viewer, ctx.PageID) {
		return false, nil
	}
	// 2026-10-21 夕から、行の先頭にチェック・「↩ 戻す」は表の下（表の中にボタンを置かない）。
	pageID := page.FormatID(ctx.PageID)
	prependRowChromeCells(el, "skip-pick-cell", func(n int, _ *html.Node) string {
		return pickHTML("skip-pick", map[string]string{"row": strconv.Itoa(n + 1)})
	})
	appendBarAfter(el, "mfg-skip-bar", `<span data-mfg="`+stdhtml.EscapeString(pageID)+`"></span>`+
		barButton("skip-sel-back", "↩ 戻す", "選んだ行を手配不要表から外します（製造明細表の「部材」で、また集められます）")+`<span class="mfg-bar-say"></span>`)
	return false, nil
}
