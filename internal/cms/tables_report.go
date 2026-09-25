package cms

// ─────────────────────────────────────────────────────────────────────────
// 「列の揃っていない表」の一覧（2026-09-25・docs/【考察】DBの日本語化.md §3.4・§7 の2段目）
//
// 同じキャプションの表は**列を統合**して1つの表に入れます（利用者の決定）。代償は、
// **打ち間違えた見出しが黙って新しい列になる**ことです——`品番` のつもりの `品 番` は別の列。
// そこで運用者に、キャプションごとに**各列を何ページの表が持っているか**を見せます。
// 50枚のうち1枚だけの列は、打ち間違いの疑いが濃い。
//
// ⚠ **数えるのは本文の見出し**です（値ではない）——`備考` のように空の多い列を、値の数で
// 数えると「めったに使われない列」に見えてしまいます。本文を読み直すので、管理ページを
// 開いたときだけ計算します（保存のたびには数えない）。
//
// 本文に無いのに data/tables.db に残っている表・列（見出しを直した跡）も言います
// ——DB再構築で消えます（tables_db.go は表も列も消さないため）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TableReport はキャプション（＝DBの表）1つぶんの報告です。
type TableReport struct {
	Name       string         `json:"name"`
	Pages      int            `json:"pages"`       // この名前の表を持つページの数（本文）
	Rows       int            `json:"rows"`        // data/tables.db の行数
	NeedsQuote bool           `json:"needs_quote"` // SQL で引用符が要る名前か
	Leftover   bool           `json:"leftover"`    // 本文にはもう無い（DB再構築で消える）
	Columns    []ColumnReport `json:"columns"`
	Suspects   int            `json:"suspects"` // 打ち間違いの疑いのある列の数
}

// ColumnReport は列1つぶんの報告です。
type ColumnReport struct {
	Name       string `json:"name"`
	Pages      int    `json:"pages"`       // この見出しを持つページの数
	NeedsQuote bool   `json:"needs_quote"` // SQL で引用符が要る名前か
	Suspect    bool   `json:"suspect"`     // 打ち間違いの疑い（ほかのページに無い）
	Leftover   bool   `json:"leftover"`    // 本文にはもう無い見出し（DB再構築で消える）
}

// TablesReport は、本文のキャプションのある表を数え直して、表ごとの列の揃い方を返します。
//
// 並びは**気になる点の多い表が先**（疑いのある列が多い順）、同じなら名前順。
func TablesReport() ([]TableReport, error) {
	type colCount struct {
		name  string
		pages map[int]bool
	}
	type tableCount struct {
		name  string
		pages map[int]bool
		cols  map[string]*colCount // asciiFold → 列
		order []string
	}
	found := map[string]*tableCount{} // asciiFold → 表

	err := eachPageBody(func(id string, pageID int, root *html.Node) {
		if excludedFromTables(id, root) {
			return
		}
		for _, ct := range captionTablesOf(root) {
			k := asciiFold(ct.Name)
			t := found[k]
			if t == nil {
				t = &tableCount{name: ct.Name, pages: map[int]bool{}, cols: map[string]*colCount{}}
				found[k] = t
			}
			t.pages[pageID] = true
			for _, h := range ct.Headers {
				if h == "" {
					continue
				}
				if isSystemColumn(h) {
					h += systemColumnSuffix
				}
				ck := asciiFold(h)
				c := t.cols[ck]
				if c == nil {
					c = &colCount{name: h, pages: map[int]bool{}}
					t.cols[ck] = c
					t.order = append(t.order, ck)
				}
				c.pages[pageID] = true
			}
		}
	})
	if err != nil {
		return nil, err
	}

	// data/tables.db の側（行数と、本文にはもう無い表・列）。
	inDB := map[string][]string{} // asciiFold(表) → 列（出どころの列を除く）
	rowsOf := map[string]int{}
	names := map[string]string{}
	if db := database.TablesDB; db != nil {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		existing, err := existingTables(tx)
		if err != nil {
			return nil, err
		}
		for k, t := range existing {
			if !t.Ours {
				continue
			}
			names[k] = t.Name
			cols, err := tableColumnNames(tx, t.Name)
			if err != nil {
				return nil, err
			}
			inDB[k] = cols
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(t.Name)).Scan(&n); err != nil {
				return nil, err
			}
			rowsOf[k] = n
		}
	}

	var out []TableReport
	keys := map[string]bool{}
	for k := range found {
		keys[k] = true
	}
	for k := range inDB {
		keys[k] = true
	}
	for k := range keys {
		r := TableReport{Rows: rowsOf[k]}
		if t := found[k]; t != nil {
			r.Name, r.Pages = t.name, len(t.pages)
			for _, ck := range t.order {
				c := t.cols[ck]
				cr := ColumnReport{Name: c.name, Pages: len(c.pages), NeedsQuote: identNeedsQuote(c.name)}
				// ⚠ **疑い**: 2ページ以上にある表で、その見出しが1ページにしか無い。
				cr.Suspect = r.Pages >= 2 && cr.Pages == 1
				if cr.Suspect {
					r.Suspects++
				}
				r.Columns = append(r.Columns, cr)
			}
		} else {
			r.Name, r.Leftover = names[k], true
		}
		r.NeedsQuote = identNeedsQuote(r.Name)
		// 本文にはもう無いのに DB に残っている列（見出しを直した跡）。
		have := map[string]bool{}
		for _, c := range r.Columns {
			have[asciiFold(c.Name)] = true
		}
		for _, c := range inDB[k] {
			if !have[asciiFold(c)] {
				r.Columns = append(r.Columns, ColumnReport{Name: c, Leftover: true,
					NeedsQuote: identNeedsQuote(c)})
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Suspects != out[j].Suspects {
			return out[i].Suspects > out[j].Suspects
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// tableColumnNames は表の列の名前を、出どころの列を除いて並び順で返します。
func tableColumnNames(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		switch name {
		case colPageID, colTableID, colRowID:
			continue
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// eachPageBody は data/master のページ本文を1枚ずつ読んで fn へ渡します（版置き場は除く）。
// 読めないページは飛ばします（一覧のための読み直しで、止めるほどではない）。
func eachPageBody(fn func(id string, pageID int, root *html.Node)) error {
	err := filepath.Walk(page.MasterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if info.IsDir() {
			if info.Name() == versionsDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".html") {
			return nil
		}
		id := strings.TrimSuffix(info.Name(), ".html")
		pageID, ok := pageNumber(id)
		if !ok {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		root, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return nil
		}
		fn(id, pageID, root)
		return nil
	})
	return err
}

// pageNumber はページID（ゼロ詰め6桁）を数にします。
func pageNumber(id string) (int, bool) {
	n, err := strconv.Atoi(id)
	return n, err == nil && n >= 0
}

// TablesReportAPIHandler は GET /api/admin/tables——「列の揃っていない表」の一覧（admin だけ）。
//
// ⚠ **admin だけ**です——全ページの表の名前と見出しを数えるので、読めないページの
// 表の名前まで見えます。
func TablesReportAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !page.RequireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	list, err := TablesReport()
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "表の一覧を作れません: "+err.Error())
		return
	}
	if list == nil {
		list = []TableReport{}
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "tables": list})
}
