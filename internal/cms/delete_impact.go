package cms

import (
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// 消す前の確かめ（2026-10-23・【考察】消すと壊れるページと表 §5 の案A・§7.2）
//
//	2026-10-23 利用者:「すべてのページに効く仕組みとして、消す前に①参照のタグと表のセル②本文のリンクとファイル表示（保存のときに索引へ入れる）
//	③そのページの表が集計に使われているか、を調べて警告する形をコアに作ってください。警告が出ても、それでも消せるのは管理者だけにしてください。」
//
// ページを消す前に、消すと何が起きるかを索引から調べます。どのページにも同じに効きます（業務の言葉は使いません）。
//
//  1. このページを指しているほかのページ——参照の型のタグ・表のセル（page_tags・vocab_index）と、
//     本文のリンク・画像など・ファイル表示（page_links）。referrers.go。
//  2. このページ自身の表——索引に入っている表（vocab_index）は、ほかのページの表示や集計に使われることがあります。
//
// どちらかがあれば、ふつうの削除（/api/delete-page）は断り、確かめ（/api/delete-check）の中身を返します。
// 「それでも消す」（force=1）は管理者だけです。子ページがあるときは、管理者でも消せません（今までどおり）。
// 調べるのは索引だけなので、1回あたり数十ミリ秒です（職場のデータで、参照の引きは約10ミリ秒）。
// ─────────────────────────────────────────────────────────────────────────

// DeleteImpactRef は、消すページを指しているページ1枚です。
type DeleteImpactRef struct {
	PageID string   `json:"page_id"`
	Title  string   `json:"title"`
	How    []string `json:"how"` // 指し方（タグ「…」・表「…」の「…」・リンク・画像など・ファイル表示）
}

// DeleteImpactTable は、消すページ自身の索引に入っている表1つです。
type DeleteImpactTable struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

// DeleteImpact は、ページを消すと起きることです。
type DeleteImpact struct {
	Refs   []DeleteImpactRef   `json:"refs"`
	Hidden int                 `json:"hidden"` // 指しているが、確かめる人には見られないページの数
	Tables []DeleteImpactTable `json:"tables"`
}

// Empty は、消しても何も起きないかです。
func (d DeleteImpact) Empty() bool { return len(d.Refs) == 0 && d.Hidden == 0 && len(d.Tables) == 0 }

// Lines は、確かめのダイアログに出す文です。
func (d DeleteImpact) Lines() []string {
	var out []string
	if n := len(d.Refs) + d.Hidden; n > 0 {
		out = append(out, "このページを指しているページが "+strconv.Itoa(n)+" 枚あります。消すと、そこから見えなくなります。")
		for i, r := range d.Refs {
			if i == 10 {
				out = append(out, "  ほか "+strconv.Itoa(len(d.Refs)-10)+" 枚")
				break
			}
			out = append(out, "  /"+r.PageID+" "+r.Title+"（"+strings.Join(r.How, "・")+"）")
		}
		if d.Hidden > 0 {
			out = append(out, "  あなたが見られないページ "+strconv.Itoa(d.Hidden)+" 枚")
		}
	}
	if len(d.Tables) > 0 {
		var ts []string
		for _, t := range d.Tables {
			ts = append(ts, t.Name+"（"+strconv.Itoa(t.Rows)+" 行）")
		}
		out = append(out, "このページの表 "+strings.Join(ts, "・")+" は、ほかのページの表示や集計に使われることがあります。消すと、その数えから外れます。")
	}
	return out
}

// DeleteImpactOf は、利用者 user がページ id を消すと起きることを調べます。
func DeleteImpactOf(db ReadOnlyDB, user *auth.User, id string) (DeleteImpact, error) {
	var d DeleteImpact
	self, ok := page.NormalizeID(id)
	if !ok {
		return d, nil
	}
	pid, _ := strconv.Atoi(self)
	hits, err := refHitsTo(db, self)
	if err != nil {
		return d, err
	}
	at := map[int]int{}
	for _, h := range hits {
		if i, ok := at[h.PageID]; ok {
			if i >= 0 {
				d.Refs[i].How = append(d.Refs[i].How, h.How)
			}
			continue
		}
		if user == nil || !page.CanView(user, h.PageID) {
			at[h.PageID] = -1
			d.Hidden++
			continue
		}
		var title string
		_ = db.QueryRow(`SELECT title FROM pages WHERE id = ?`, h.PageID).Scan(&title)
		at[h.PageID] = len(d.Refs)
		d.Refs = append(d.Refs, DeleteImpactRef{PageID: page.FormatID(h.PageID), Title: title, How: []string{h.How}})
	}
	// 自分の表（字のあるセルを持つ行の数）。
	rows, err := db.Query(`SELECT data_type, COUNT(DISTINCT block_no || '-' || row_no) FROM vocab_index
		WHERE page_id = ? AND TRIM(value) != '' GROUP BY data_type ORDER BY MIN(block_no)`, pid)
	if err != nil {
		return d, err
	}
	type tc struct {
		t string
		n int
	}
	var found []tc
	for rows.Next() {
		var x tc
		if err := rows.Scan(&x.t, &x.n); err != nil {
			rows.Close()
			return d, err
		}
		found = append(found, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	for _, x := range found {
		if x.n > 0 {
			d.Tables = append(d.Tables, DeleteImpactTable{Name: vocabDisplayName(x.t), Rows: x.n})
		}
	}
	return d, nil
}

// DeleteCheckAPIHandler は GET /api/delete-check?id= です。ページを消すと起きることを返します（消す前の確かめ）。
//
//	{"success":true, "empty":false, "lines":["…"], "impact":{refs, hidden, tables}, "children":0, "can_force":true}
//
// can_force は「警告があっても消せるか」（管理者だけ）。見られないページは 404（存在を明かさない）。
func DeleteCheckAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, pageID, ok := queryPageID(w, r)
	if !ok {
		return
	}
	user := auth.CurrentUser(r)
	if user == nil || !page.CanView(user, pageID) {
		http.NotFound(w, r)
		return
	}
	imp, err := DeleteImpactOf(database.DB, user, id)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "消す前の確かめができません: "+err.Error())
		return
	}
	var children int
	_ = database.DB.QueryRow("SELECT COUNT(*) FROM pages WHERE parent_id = ?", pageID).Scan(&children)
	WriteJSON(w, map[string]any{
		"success": true, "empty": imp.Empty(), "lines": imp.Lines(), "impact": imp,
		"children": children, "can_force": user.IsAdmin,
	})
}
