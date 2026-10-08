package toho

// ─────────────────────────────────────────────────────────────────────────
// 整理の「ほかのファイル」——メールの添付を、行き先を探して加工製品ページ・装置のページに足す（2026-10-08）
//
// 利用者:「図面以外のファイルでも、ここで相手を検索して追加できるとありがたいですね」（メールの整理の欄で——
// 仕様書・3Dデータなど、🤖 解析で加工製品ページにならない添付）。
//
//   - 整理の提案（`/api/filing-proposal`）に、その記録の添付の一覧（受信原本の .eml は除く）を足す。
//   - 欄を開いたときだけ、どのページがもうその添付を表示しているかを探す（`/api/filing-files-shown`——本文を
//     読んで `data-ref` を探すので重い。開くたびではなく欄を開いたときに1回）。
//   - 行き先は「行き先を探す」（`/api/filing-search?folders=1`——加工製品ページと装置のページ）で選び、
//     「＋ ここに足す」で、そのページの**末尾**にファイル表示（`<section data-type="file-view" data-ref="記録-添付">`）を
//     足す（`/api/filing-attach`）。添付はメールに置いたまま（写さない——指すだけ）。同じページに同じ添付は足さない。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// filingFile は整理の欄に出す添付1つです。
type filingFile struct {
	ID   string `json:"id"`   // 添付ID（保存名から拡張子を除いたもの）
	Name string `json:"name"` // 届いたときの名前
	Ref  string `json:"ref"`  // ファイル表示の参照（記録のページID-添付ID）
}

// mailFilesOf は記録の添付を、届いた順（同じなら名前の順）に返します（受信原本の .eml は除く）。
func mailFilesOf(recordID string) []filingFile {
	metas := cms.ReadAttachmentMetas(recordID)
	stored := make([]string, 0, len(metas))
	for s := range metas {
		if strings.HasSuffix(strings.ToLower(s), ".eml") {
			continue
		}
		stored = append(stored, s)
	}
	sort.Slice(stored, func(i, j int) bool {
		a, b := metas[stored[i]], metas[stored[j]]
		if a.SavedAt != b.SavedAt {
			return a.SavedAt < b.SavedAt
		}
		return a.Name < b.Name
	})
	out := make([]filingFile, 0, len(stored))
	for _, s := range stored {
		id := strings.TrimSuffix(s, extOf(s))
		name := metas[s].Name
		if name == "" {
			name = s
		}
		out = append(out, filingFile{ID: id, Name: name, Ref: recordID + "-" + id})
	}
	return out
}

// extOf は保存名の拡張子（点を含む）です。
func extOf(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i:]
	}
	return ""
}

// shownPage は添付を表示しているページ1つです。
type shownPage struct {
	PageID string `json:"page_id"`
	Title  string `json:"title"`
}

// pagesShowing は refs のそれぞれを、どのページ（読めるものだけ）がファイル表示で指しているかを返します。
// 本文を全部読むので重い——整理の欄を開いたときだけ呼ぶ。
func pagesShowing(user *auth.User, refs []string) map[string][]shownPage {
	out := map[string][]shownPage{}
	if len(refs) == 0 {
		return out
	}
	rows, err := database.DB.Query("SELECT id, file_path FROM pages")
	if err != nil {
		return out
	}
	type pg struct {
		id   int
		path string
	}
	var all []pg
	for rows.Next() {
		var p pg
		if rows.Scan(&p.id, &p.path) == nil {
			all = append(all, p)
		}
	}
	rows.Close()
	for _, p := range all {
		if p.path == "" || !page.CanView(user, p.id) {
			continue
		}
		b, err := os.ReadFile(p.path)
		if err != nil || !strings.Contains(string(b), `data-ref="`) {
			continue
		}
		body := string(b)
		for _, ref := range refs {
			if strings.Contains(body, `data-ref="`+ref+`"`) {
				id := page.FormatID(p.id)
				out[ref] = append(out[ref], shownPage{PageID: id, Title: pageTitleOf(id)})
			}
		}
	}
	return out
}

// FilingFilesShownAPIHandler は GET /api/filing-files-shown?page_id=記録 です（{success, shown: {ref: [{page_id, title}]}}）。
func FilingFilesShownAPIHandler(w http.ResponseWriter, r *http.Request) {
	recordID, _, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	files := mailFilesOf(recordID)
	refs := make([]string, len(files))
	for i, f := range files {
		refs[i] = f.Ref
	}
	cms.WriteJSON(w, map[string]any{"success": true, "shown": pagesShowing(user, refs)})
}

// FilingAttachAPIHandler は POST /api/filing-attach です（入力: {page_id（記録）, attach（添付ID）, target（足す先のページ）}）。
// 足す先の末尾にファイル表示を1つ足します（同じ添付が既に表示されていれば断る）。
func FilingAttachAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Attach string `json:"attach"`
		Target string `json:"target"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	recordID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	targetID, ok := cms.PageIDOrFail(w, req.Target)
	if !ok {
		return
	}
	recordInt, _ := strconv.Atoi(recordID)
	targetInt, _ := strconv.Atoi(targetID)
	if !page.CanView(user, recordInt) {
		cms.JSONFail(w, http.StatusNotFound, "記録が見つかりません")
		return
	}
	var file *filingFile
	for _, f := range mailFilesOf(recordID) {
		if f.ID == strings.TrimSpace(req.Attach) {
			f := f
			file = &f
			break
		}
	}
	if file == nil {
		cms.JSONFail(w, http.StatusBadRequest, "この記録にその添付はありません")
		return
	}
	if !canWritePage(user, targetInt) {
		cms.JSONFail(w, http.StatusForbidden, "足す先のページへ書き込む権限がありません")
		return
	}
	if holder, open := editlock.Locks.EditorOpen(targetInt); open {
		cms.JSONFail(w, http.StatusConflict, "足す先のページは編集中です（"+holder+"）。閉じてからもう一度お試しください")
		return
	}
	body, err := cms.ReadPageBody(targetID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "足す先のページを読めません")
		return
	}
	title := pageTitleOf(targetID)
	if strings.Contains(body, `data-ref="`+file.Ref+`"`) {
		cms.JSONFail(w, http.StatusConflict, "「"+title+"」には、このファイルの表示が既にあります")
		return
	}
	if err := cms.RewriteBody(targetID, user.Username, func(cur string) string {
		return strings.TrimRight(cur, "\n") + "\n" + `<section data-type="file-view" data-ref="` + file.Ref + `"></section>` + "\n"
	}); err != nil {
		cms.JSONFail(w, http.StatusConflict, "足せません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "filing.attach", file.Ref+" -> "+targetID)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": targetID, "title": title})
}
