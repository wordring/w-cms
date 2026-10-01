package toho

// ─────────────────────────────────────────────────────────────────────────
// 手配不要——必要部材表から「要らなくなった」部材を外す（2026-10-01）
//
// 利用者:「必要部材表はチェックして発注部材表に入れますが、必要なくなった時に消すのはどうしましょう？」→（案を聞いて）
// 「不要にする」ボタン。在庫で足りる・自社で作る・電話で買った——理由はさまざまで、どれも「この受注のぶんはもう買わない」。
//
//   - 必要部材表で行を選び「🚫 不要にする」（理由を書ける）→ 発注フォルダの「手配不要」の表に行が入る（受注・弊社品番・
//     部材・数量・理由・日付）。必要部材表は発注部材表と同じく、この数を**受注ごとに**引く（procure_ledger.go）。
//   - 表の行の「↩ 戻す」で外すと、必要部材表へ戻る（一覧は毎回計算する鏡なので、外せば戻る）。弊社品番の無い行
//     （臨時部材表から来た行）は臨時部材表へ戻す。
//   - ⚠ 本文の表なので、何を買わなかったか・なぜか、が残る（DB にも入る）。
// ─────────────────────────────────────────────────────────────────────────

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

// SkipType は手配不要の表の形式です。
const SkipType = "procure-skip"

func init() {
	cms.RegisterMirror(SkipType, cms.MirrorHandlerFunc(renderSkipTable))
}

// skipColumns は手配不要の表の列です——発注の列のうち部材を言い当てるもの（単価・状態・仕様・支給・備考は除く）と、
// 理由・日付。
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

// skipLineValue は手配不要の表のセルの値です（理由・日付のほかは発注の行と同じ）。
func skipLineValue(ln ourOrderLine, field, reason, date string) string {
	switch field {
	case "reason":
		return reason
	case "date":
		return date
	}
	return orderLineValue(ln, field)
}

// addSkipRows は本文の手配不要の表へ行を足します（無ければ必要部材表の目印の直後に作る・無ければ末尾）。
func addSkipRows(body string, lines []ourOrderLine, reason, date string) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	tables := tablesOfType(nodes, SkipType)
	if len(tables) == 0 {
		var b strings.Builder
		b.WriteString(`<table><caption>` + stdhtml.EscapeString(displayNameOf(SkipType)) + `</caption><tbody>` +
			headerRowHTML(SkipType))
		for _, ln := range lines {
			b.WriteString(`<tr>`)
			for _, c := range columnsOf(SkipType) {
				b.WriteString(`<td>` + stdhtml.EscapeString(skipLineValue(ln, c.Field, reason, date)) + `</td>`)
			}
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</tbody></table>`)
		repl, perr := htmldoc.ParseFragment(b.String())
		if perr != nil || len(repl) == 0 {
			return body, 0
		}
		marker := findElement(nodes, isRequiredPartsMarker)
		if marker == nil {
			return htmldoc.Render(append(nodes, repl...)), len(lines)
		}
		out, ok := spliceNodes(nodes, marker, repl, true)
		if !ok {
			return body, 0
		}
		return out, len(lines)
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

// takeSkipRow は手配不要の表の row 行目（見出しを除く・1始まり）を外し、外した行を返します。最後の1行なら表ごと消す。
func takeSkipRow(body string, row int) (string, ourOrderLine, bool) {
	if row < 1 {
		return body, ourOrderLine{}, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, ourOrderLine{}, false
	}
	tables := tablesOfType(nodes, SkipType)
	if len(tables) == 0 {
		return body, ourOrderLine{}, false
	}
	table := tables[0]
	rows := rowsOf(table)
	if row >= len(rows) {
		return body, ourOrderLine{}, false
	}
	tr := rows[row]
	line := lineOfRow(rows[0], tr)
	tr.Parent.RemoveChild(tr)
	if len(rowsOf(table)) <= 1 {
		out, ok := spliceNodes(nodes, table, nil, false)
		return out, line, ok
	}
	return htmldoc.Render(nodes), line, true
}

// SkipAPIHandler は POST /api/our-order/skip です。入力: {page_id, lines, reason}——page_id は必要部材表のあるページ
// （発注フォルダ）。行を手配不要の表へ入れ、臨時部材表から来た行は臨時部材表から消します（移す）。
func SkipAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string         `json:"page_id"`
		Lines  []ourOrderLine `json:"lines"`
		Reason string         `json:"reason"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "不要にする行を選んでください")
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	date := time.Now().Format("2006-01-02")
	added := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		if rows := tempRowsOn(req.Lines, pageID); len(rows) > 0 {
			cur, _ = removeTempPartRows(cur, rows)
		}
		var out string
		out, added = addSkipRows(cur, req.Lines, reason, date)
		return out
	}) {
		return
	}
	auth.Audit(user.Username, "procure.skip", pageID+" "+strconv.Itoa(added)+"行 "+reason)
	out := map[string]any{"success": true, "page_id": pageID, "rows": added}
	if note := takeTempRowsElsewhere(user, req.Lines, pageID); note != "" {
		out["temp_note"] = note
	}
	cms.WriteJSON(w, out)
}

// SkipRemoveAPIHandler は POST /api/our-order/skip/remove です。入力: {page_id, row}——手配不要の表から行を外す
// （必要部材表へ戻る）。弊社品番の無い行は臨時部材表へ戻す（同じ保存で）。
func SkipRemoveAPIHandler(w http.ResponseWriter, r *http.Request) {
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
		out, ln, ok := takeSkipRow(cur, req.Row)
		done = ok
		if !ok {
			return cur
		}
		if needsTempParts(ln) {
			out, _ = addTempParts(out, []ourOrderLine{ln})
		}
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict, "その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "procure.skip-remove", pageID+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// renderSkipTable は手配不要の表の各行に「↩ 戻す」を足します（鏡・本文には残らない）。
func renderSkipTable(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	if ctx.Viewer == nil || !canWritePage(ctx.Viewer, ctx.PageID) {
		return false, nil
	}
	pageID := page.FormatID(ctx.PageID)
	addRowChromeCells(el, "skip-row-act", func(row int, _ *html.Node) string {
		return `<button type="button" class="chip-btn skip-row-back" data-skip-page="` + stdhtml.EscapeString(pageID) +
			`" data-skip-row="` + strconv.Itoa(row) + `" title="必要部材表へ戻します（この行を外します）">↩ 戻す</button>`
	})
	return false, nil
}
