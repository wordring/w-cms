package toho

// ─────────────────────────────────────────────────────────────────────────
// 表の末尾へ行を足す口（2026-10-04・管理者だけ——移しの道具が使う）
//
// 利用者:「見積依頼を基に、加工製品ページの『材料』『外注加工』『購入部品』を埋められますか？」→ 塗装の見積から外注加工に
// 「塗装」の行を足す（推奨業者はいちばん新しい見積の業者・個数は空欄・行のある表にも足す——問いへの答え）。どの行を足すかは
// 道具（実名を含む一覧を読むのでリポジトリの外）が決め、この口は**足すことだけ**をする:
//
//   - 表はキャプションで選ぶ（1枚目）。列は**見出しの言葉**で合わせる——表に無い見出しの値が来たら断る（黙って落とさない）。
//   - 中身の無い行（テンプレートの取っ掛かりの空の行）は取り除いてから足す。**ある行には触らない**。
//   - 書けない・編集中のページは断る（gateWritablePage）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// appendTableRows は本文の、キャプションが caption の1枚目の表の末尾へ rows を足します（足した行の数・断る理由）。
func appendTableRows(body, caption string, rows []map[string]string) (string, int, string) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0, "本文を読めません"
	}
	t := findElement(nodes, func(n *html.Node) bool {
		if n.Data != "table" {
			return false
		}
		c := lastChild(n, "caption")
		return c != nil && strings.TrimSpace(textOf(c)) == caption
	})
	if t == nil {
		return body, 0, "「" + caption + "」の表がありません"
	}
	trs := rowsOf(t)
	if len(trs) == 0 {
		return body, 0, "「" + caption + "」の表に見出しの行がありません"
	}
	labels := cellTexts(trs[0])
	known := map[string]bool{}
	for _, l := range labels {
		known[l] = true
	}
	for _, r := range rows {
		for l := range r {
			if !known[l] {
				return body, 0, "「" + caption + "」の表に「" + l + "」の列がありません"
			}
		}
	}
	// 中身の無い行は取り除く（取っ掛かりの空の行）。
	for _, tr := range trs[1:] {
		empty := true
		for _, s := range cellTexts(tr) {
			if s != "" {
				empty = false
				break
			}
		}
		if empty && tr.Parent != nil {
			tr.Parent.RemoveChild(tr)
		}
	}
	host := lastChild(t, "tbody")
	if host == nil {
		host = t
	}
	for _, r := range rows {
		tr := &html.Node{Type: html.ElementNode, Data: "tr"}
		for _, l := range labels {
			td := &html.Node{Type: html.ElementNode, Data: "td"}
			if v := strings.TrimSpace(r[l]); v != "" {
				td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
			}
			tr.AppendChild(td)
		}
		host.AppendChild(tr)
	}
	return htmldoc.Render(nodes), len(rows), ""
}

// AppendTableRowsAPIHandler は POST /api/table/append-rows です（管理者だけ）。入力: {page_id, caption, rows: [{見出し: 値}]}。
func AppendTableRowsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "管理者だけが使えます")
		return
	}
	var req struct {
		PageID  string              `json:"page_id"`
		Caption string              `json:"caption"`
		Rows    []map[string]string `json:"rows"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Caption) == "" || len(req.Rows) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "表のキャプションと行が要ります")
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if cms.IsTemplateArea(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "テンプレートには足しません")
		return
	}
	why, added := "", 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, n, reason := appendTableRows(cur, strings.TrimSpace(req.Caption), req.Rows)
		why, added = reason, n
		if reason != "" {
			return cur
		}
		return out
	}) {
		return
	}
	if why != "" {
		cms.JSONFail(w, http.StatusConflict, "足していません: "+why)
		return
	}
	auth.Audit(user.Username, "table.append-rows", pageID+" "+req.Caption+" "+strconv.Itoa(added)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "rows": added})
}
