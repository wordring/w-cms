package cms

// ─────────────────────────────────────────────────────────────────────────
// 表の列の名前を置き換える（管理者・2026-10-15）
//
// 利用者:「受注フォルダの表の「出荷済み」を出荷済数量に変更します」→ 受注明細表の列ごと替える（同日の答え）。
//
// 設定の語彙で列の名前を変えると（`出荷済み` → `出荷済数量`）、**既にあるページの表の見出しは古い名前のまま**残り、
// 新しい名前では読めなくなります（項目の鍵は見出しの表示文字）。どの業務でも起きることなのでコアに置きます
// （語の中身は知らない）。作法は「タグの値を置き換える」（tag_rename.go）にそろえます。
//
//   - 当てるのは、**キャプションが caption の表の見出しのセル（`<th>`）だけ**——本文の地の文・ほかの表・タグは触らない。
//   - 探すのは全ページの本文（キャプションで名乗る表は索引に載らないものもあるので、索引には頼らない）。
//     書くのは正本のファイル（`RewriteBody`——版が残るので戻せる・索引も直る）。
//   - 同じ表に新しい名前の列が既にあれば、その表は書き換えない（列が2つになる）——Conflict で返す。
//   - 編集中のページ・テンプレートの中は飛ばして返す（tag_rename.go と同じ）。
//     ⚠ 2026-10-12 から画面は編集権を取らないので、「編集中」で飛ばすのは道具が /api/lock で握った直後だけです
//     （tag_rename.go の注記）。人が開いて書いているページへも書き、重なりは RewriteBody の錠が1つずつ通します。
//   - `dry` なら数えるだけ（画面は数を見せてから確かめる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	stdhtml "html"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// columnRenameResult は置き換えた結果です（ページIDは6桁の文字列）。
type columnRenameResult struct {
	Changed  []string `json:"changed"`  // 書き換えたページ
	Editing  []string `json:"editing"`  // 編集中で飛ばしたページ
	Template []string `json:"template"` // テンプレートの中で飛ばしたページ
	Conflict []string `json:"conflict"` // 同じ表に新しい名前の列が既にあって飛ばしたページ
}

var (
	crTableRe   = regexp.MustCompile(`(?is)<table\b[^>]*>.*?</table>`)
	crCaptionRe = regexp.MustCompile(`(?is)<caption\b[^>]*>(.*?)</caption>`)
	crTagRe     = regexp.MustCompile(`<[^>]+>`)
)

// thPattern は見出しのセル name に当たる形です（属性と前後の空白は問わない）。
func thPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(<th\b[^>]*>)\s*` + regexp.QuoteMeta(stdhtml.EscapeString(name)) + `\s*(</th>)`)
}

// tableCaptionText は表の断片のキャプションの文字です（タグを外して前後の空白を落とす）。
func tableCaptionText(table string) string {
	m := crCaptionRe.FindStringSubmatch(table)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(stdhtml.UnescapeString(crTagRe.ReplaceAllString(m[1], "")))
}

// renameColumn は、キャプションが caption の表の見出しのセル from を to に置き換えた本文を返します。
// conflict は、新しい名前の列が既にあって書き換えなかった表があったか。
func renameColumn(body, caption, from, to string) (out string, conflict bool) {
	oldTh, newTh := thPattern(from), thPattern(to)
	out = crTableRe.ReplaceAllStringFunc(body, func(table string) string {
		if tableCaptionText(table) != caption || !oldTh.MatchString(table) {
			return table
		}
		if newTh.MatchString(table) {
			conflict = true
			return table
		}
		return oldTh.ReplaceAllString(table, `${1}`+strings.ReplaceAll(stdhtml.EscapeString(to), "$", "$$")+`${2}`)
	})
	return out, conflict
}

// renameColumnEverywhere は、キャプションが caption の表の列 from を、全ページで to に置き換えます（dry なら数えるだけ）。
func renameColumnEverywhere(author, caption, from, to string, dry bool) (columnRenameResult, error) {
	var res columnRenameResult
	var ids []string
	if err := eachPageBody(func(id string, _ int, _ *html.Node) { ids = append(ids, id) }); err != nil {
		return res, err
	}
	for _, pid := range ids {
		body, err := ReadPageBody(pid)
		if err != nil {
			continue // 読めないページは一覧と同じく飛ばす
		}
		got, conflict := renameColumn(body, caption, from, to)
		if conflict {
			res.Conflict = append(res.Conflict, pid)
		}
		if got == body {
			continue
		}
		if IsTemplateArea(pid) {
			res.Template = append(res.Template, pid)
			continue
		}
		if n, ok := pageNumber(pid); ok {
			if _, open := editlock.Locks.EditorOpen(n); open {
				res.Editing = append(res.Editing, pid)
				continue
			}
		}
		if dry {
			res.Changed = append(res.Changed, pid)
			continue
		}
		if err := RewriteBody(pid, author, func(cur string) string { s, _ := renameColumn(cur, caption, from, to); return s }); err != nil {
			return res, err
		}
		res.Changed = append(res.Changed, pid)
	}
	return res, nil
}

// ColumnRenameAPIHandler は POST /api/admin/column-rename です（admin 限定）。
// 入力: {caption, from, to, dry}——キャプションが caption の表の列 from を、全ページで to に置き換えます。
func ColumnRenameAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !page.RequireAdmin(w, r) {
		return
	}
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Caption string `json:"caption"`
		From    string `json:"from"`
		To      string `json:"to"`
		Dry     bool   `json:"dry"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	req.Caption, req.From, req.To = strings.TrimSpace(req.Caption), strings.TrimSpace(req.From), strings.TrimSpace(req.To)
	if req.Caption == "" || req.From == "" || req.To == "" {
		JSONFail(w, http.StatusBadRequest, "表のキャプション・今の列の名前・新しい列の名前を書いてください")
		return
	}
	if req.From == req.To {
		JSONFail(w, http.StatusBadRequest, "今の名前と新しい名前が同じです")
		return
	}
	res, err := renameColumnEverywhere(user.Username, req.Caption, req.From, req.To, req.Dry)
	if err != nil {
		// 途中まで書いたものは残る（版があるので戻せる）——どこまで進んだかを添える。
		json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "置き換えの途中で止まりました: " + err.Error(), "result": res})
		return
	}
	if !req.Dry && len(res.Changed) > 0 {
		auth.Audit(user.Username, "column.rename", req.Caption+": "+req.From+" → "+req.To+"（"+strings.Join(res.Changed, ",")+"）")
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "dry": req.Dry, "result": res})
}
