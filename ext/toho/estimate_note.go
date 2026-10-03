package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積書の備考を見積明細の下で書く（2026-10-03）
//
// 利用者:「見積書ページの見積明細テーブルの下に備考入力欄を付けると良いと思います」。
//
// 備考の正本は見積書ページの「備考」の節です（PDF はそこを刷る——estimate_pdf.go の readEstimateDoc）。
// それまでは編集モードで節を直すしかなく、⚠ PDF を作るとファイル表示が表の直後に入るので、節は PDF の枠の下になり
// 閲覧モードでは見つけにくかった。欄は鏡（見積明細の足元・合計の下・書ける人にだけ）で、いまの備考を入れて出し、
// 「備考を保存」でその節を書き換える。編集モードでは隠す（節を直接直せる——app.css）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/database"
)

// estimateNoteOf は見積書ページのいまの備考を、行を改行でつないで返します（読めなければ空）。
func estimateNoteOf(pageID string) string {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return ""
	}
	_, _, note, err := readEstimateDoc(body)
	if err != nil {
		return ""
	}
	return strings.Join(note, "\n")
}

// estimateNoteBoxHTML は見積明細の足元の備考の欄です。
func estimateNoteBoxHTML(pid, note string) string {
	return `<div class="estimate-note-box"><span class="estimate-note-label">備考（紙では明細の下に刷ります）</span>` +
		`<textarea class="estimate-note-input" rows="3">` + stdhtml.EscapeString(note) + `</textarea>` +
		`<button type="button" class="chip-btn estimate-note-save" data-estimate-page="` + pid + `">備考を保存</button> ` +
		`<span class="estimate-note-say"></span></div>`
}

// withEstimateNote は本文の「備考」の節の中身を note に替えます（見出しは残す・1行1段落・空なら空の段落）。
// 節が無ければ本文の末尾に作ります。
func withEstimateNote(body, note string) (string, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	sec := findElement([]*html.Node{root}, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == estimateNoteHeading
	})
	if sec == nil {
		sec = &html.Node{Type: html.ElementNode, Data: "section"}
		h := &html.Node{Type: html.ElementNode, Data: "h2"}
		h.AppendChild(&html.Node{Type: html.TextNode, Data: estimateNoteHeading})
		sec.AppendChild(h)
		root.AppendChild(sec)
	}
	isHeading := func(c *html.Node) bool {
		return c.Type == html.ElementNode && len(c.Data) == 2 && c.Data[0] == 'h' && c.Data[1] >= '1' && c.Data[1] <= '6'
	}
	kept := false
	for c := sec.FirstChild; c != nil; {
		next := c.NextSibling
		if !kept && isHeading(c) {
			kept = true
		} else {
			sec.RemoveChild(c)
		}
		c = next
	}
	added := 0
	for _, ln := range strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		p := &html.Node{Type: html.ElementNode, Data: "p"}
		p.AppendChild(&html.Node{Type: html.TextNode, Data: ln})
		sec.AppendChild(p)
		added++
	}
	if added == 0 {
		p := &html.Node{Type: html.ElementNode, Data: "p"}
		p.AppendChild(&html.Node{Type: html.ElementNode, Data: "br"})
		sec.AppendChild(p)
	}
	var out []*html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return htmldoc.Render(out), nil
}

// EstimateNoteAPIHandler は POST /api/estimate/note です。入力: {page_id, note}——見積書ページの「備考」の節を書き換えます。
func EstimateNoteAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Note   string `json:"note"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if cms.IsTemplateArea(pageID) || cms.PageTagValue(database.DB, pageNum(pageID), EstimateNoTag) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "見積書ページではありません")
		return
	}
	var failed error
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, err := withEstimateNote(cur, req.Note)
		if err != nil {
			failed = err
			return cur
		}
		return out
	}) {
		return
	}
	if failed != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "備考を書けません: "+failed.Error())
		return
	}
	auth.Audit(user.Username, "estimate.note", pageID)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}
