package cms

// ─────────────────────────────────────────────────────────────────────────
// 表を探す——検索画面と AI の口（2026-09-25・docs/【考察】DBの日本語化.md §7 の3段目）
//
// 利用者:「一般的にはw－cmsの検索画面、ClaudeなどのAI、ユーザーがSQLを書くの順に多い」。
// 検索画面（assets/tables.html）と AI は**この口だけ**を使います。自由な SQL は受けません。
//
//	GET  /api/tables        … 探せる表と列（読めるページに行のある表だけ）
//	POST /api/tables/query  … 表・出す列・条件（等しい・含む・以上・以下）→ 行
//
// ⚠ **守りは2重です**（利用者の問い「SQLite には個別に値を入れる API があるはずですが」への答え）:
//
//   - **値は `?` で渡す**——条件の値は SQL の文に入りません。入れる前に、**DB に入れたときと
//     同じ正規化**を掛けます（答え4——`ａ１００－ｂ０１－００１` で探しても `A100-B01-001` に当たる）。
//   - **名前は `?` で渡せない**（SQL の決まり）ので、**DB に実際にある名前と照らして**
//     一致したものだけを使い、quoteIdent で囲みます。条件の種類はコードが持つ4つから選ぶだけで、
//     利用者の文字は SQL の文に入りません。
//
// ⚠ **読めるページの行だけ**返します（見せ分け・C案）——表には全ページの行が入っているので、
// 絞らないと読めない発注書の単価や仕入先が引けます。表と列の一覧も、読めるページに値のある
// ものだけです（キャプションや見出しそのものが、読めないページの中身を語ることがあるため）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 検索の行数の既定と上限。
const (
	tableQueryDefaultLimit = 200
	tableQueryMaxLimit     = 1000
)

// tableQueryOps は条件の種類です（2026-09-25 利用者:「条件の種類は4つで足ります」）。
// **SQL の文に入るのはこの右側だけ**で、利用者の文字ではありません。
var tableQueryOps = map[string]string{
	"eq":       "= ?",
	"contains": `LIKE ? ESCAPE '\'`,
	"gte":      ">= ?",
	"lte":      "<= ?",
}

// TableInfo は探せる表1つです。
type TableInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int      `json:"rows"` // 読めるページの行数
}

// TableCondition は条件1つです。
type TableCondition struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq / contains / gte / lte
	Value  string `json:"value"`
}

// TableQuery は検索の依頼です。
type TableQuery struct {
	Table   string           `json:"table"`
	Columns []string         `json:"columns"` // 空なら全部
	Where   []TableCondition `json:"where"`
	Limit   int              `json:"limit"`
}

// TableResultRow は結果の1行です。
type TableResultRow struct {
	PageID  string `json:"page_id"`
	Title   string `json:"title"`
	TableID int    `json:"table_id"`
	RowID   int    `json:"row_id"`
	Values  []any  `json:"values"`
}

// TableResult は検索の結果です。
type TableResult struct {
	Table     string           `json:"table"`
	Columns   []string         `json:"columns"`
	Rows      []TableResultRow `json:"rows"`
	Truncated bool             `json:"truncated"` // 上限で打ち切った
	// SQL は**運用者が DB を直接開いて同じことを引く**ときの文です（表示用・値は埋め込み済み）。
	// ⚠ 読めるページの絞りは入っていません——DB を直接開けるのは運用者だけ（答え2）。
	SQL string `json:"sql"`
}

// errTableQuery は利用者に見せてよい検索の誤りです（400 で返す）。
type errTableQuery struct{ msg string }

func (e errTableQuery) Error() string { return e.msg }

// viewableFunc は閲覧者の可視判定を、ページごとに1度だけ引く形で返します。
func viewableFunc(user *auth.User) func(int) bool {
	seen := map[int]bool{}
	return func(id int) bool {
		if v, ok := seen[id]; ok {
			return v
		}
		v := page.CanView(user, id)
		seen[id] = v
		return v
	}
}

// readableTable は、表のうち**読めるページ**と、そのページに値のある列を返します。
func readableTable(tx *sql.Tx, table string, canView func(int) bool) (pages []int, cols []string, rows int, err error) {
	r, err := tx.Query(`SELECT DISTINCT ` + colPageID + ` FROM ` + quoteIdent(table))
	if err != nil {
		return nil, nil, 0, err
	}
	var all []int
	for r.Next() {
		var id int
		if err := r.Scan(&id); err != nil {
			r.Close()
			return nil, nil, 0, err
		}
		all = append(all, id)
	}
	r.Close()
	for _, id := range all {
		if canView(id) {
			pages = append(pages, id)
		}
	}
	if len(pages) == 0 {
		return nil, nil, 0, nil
	}
	names, err := tableColumnNames(tx, table)
	if err != nil {
		return nil, nil, 0, err
	}
	// 1回の問い合わせで、読めるページの中の各列の値の数と行数を数える。
	sel := []string{`COUNT(*)`}
	for _, c := range names {
		sel = append(sel, `COUNT(`+quoteIdent(c)+`)`)
	}
	in, args := inClause(pages)
	counts := make([]any, len(sel))
	nums := make([]int, len(sel))
	for i := range counts {
		counts[i] = &nums[i]
	}
	if err := tx.QueryRow(`SELECT `+strings.Join(sel, ", ")+` FROM `+quoteIdent(table)+
		` WHERE `+colPageID+` IN `+in, args...).Scan(counts...); err != nil {
		return nil, nil, 0, err
	}
	for i, c := range names {
		if nums[i+1] > 0 {
			cols = append(cols, c)
		}
	}
	return pages, cols, nums[0], nil
}

// inClause は `(?, ?, …)` と束ねる値を返します。
func inClause(ids []int) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return `(` + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + `)`, args
}

// TablesFor は、閲覧者が探せる表と列を名前順に返します。
func TablesFor(user *auth.User) ([]TableInfo, error) {
	db := database.TablesDB
	if db == nil {
		return []TableInfo{}, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	existing, err := existingTables(tx)
	if err != nil {
		return nil, err
	}
	canView := viewableFunc(user)
	out := []TableInfo{}
	for _, t := range existing {
		if !t.Ours {
			continue
		}
		pages, cols, rows, err := readableTable(tx, t.Name, canView)
		if err != nil {
			return nil, err
		}
		if len(pages) == 0 {
			continue // 読めるページに行が無い表は、名前も見せない
		}
		if cols == nil {
			cols = []string{}
		}
		out = append(out, TableInfo{Name: t.Name, Columns: cols, Rows: rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// QueryTable は表を探します（読めるページの行だけ）。
func QueryTable(user *auth.User, q TableQuery) (TableResult, error) {
	db := database.TablesDB
	if db == nil {
		return TableResult{}, errTableQuery{"表の写し（data/tables.db）が開いていません"}
	}
	tx, err := db.Begin()
	if err != nil {
		return TableResult{}, err
	}
	defer tx.Rollback()
	existing, err := existingTables(tx)
	if err != nil {
		return TableResult{}, err
	}
	// ⚠ 名前は**DB に実際にある名前と照らす**——一致しない名前は使わない。
	t, ok := existing[asciiFold(TableName(q.Table))]
	if !ok || !t.Ours {
		return TableResult{}, errTableQuery{"表「" + q.Table + "」はありません"}
	}
	canView := viewableFunc(user)
	pages, cols, _, err := readableTable(tx, t.Name, canView)
	if err != nil {
		return TableResult{}, err
	}
	if len(pages) == 0 {
		return TableResult{}, errTableQuery{"表「" + q.Table + "」はありません"}
	}
	colOf := map[string]string{}
	for _, c := range cols {
		colOf[asciiFold(c)] = c
	}
	resolve := func(name string) (string, error) {
		if c, ok := colOf[asciiFold(TableName(name))]; ok {
			return c, nil
		}
		return "", errTableQuery{"表「" + t.Name + "」に列「" + name + "」はありません"}
	}

	show := cols
	if len(q.Columns) > 0 {
		show = nil
		for _, name := range q.Columns {
			c, err := resolve(name)
			if err != nil {
				return TableResult{}, err
			}
			show = append(show, c)
		}
	}

	in, args := inClause(pages)
	where := []string{colPageID + ` IN ` + in}
	var shown []string // 表示用の SQL の条件（値は埋め込み・読めるページの絞りは無し）
	for _, cond := range q.Where {
		if strings.TrimSpace(cond.Value) == "" {
			continue // 値の無い条件は無いものとする（画面で欄だけ足した）
		}
		op, ok := tableQueryOps[cond.Op]
		if !ok {
			return TableResult{}, errTableQuery{"条件の種類「" + cond.Op + "」はありません（eq・contains・gte・lte）"}
		}
		c, err := resolve(cond.Column)
		if err != nil {
			return TableResult{}, err
		}
		// ⚠ **DB に入れたときと同じ正規化**を掛けてから比べる（答え4）。
		v := tableCellValue(t.Name, c, strings.TrimSpace(cond.Value))
		if cond.Op == "contains" {
			s := likeEscape(fmt.Sprint(v))
			v = "%" + s + "%"
		}
		where = append(where, quoteIdent(c)+` `+op)
		args = append(args, v)
		shown = append(shown, displayIdent(c)+` `+strings.Replace(op, "?", sqlLiteral(v), 1))
	}

	limit := q.Limit
	if limit <= 0 {
		limit = tableQueryDefaultLimit
	}
	if limit > tableQueryMaxLimit {
		limit = tableQueryMaxLimit
	}
	sel := []string{colPageID, colTableID, colRowID}
	for _, c := range show {
		sel = append(sel, quoteIdent(c))
	}
	order := ` ORDER BY ` + colPageID + `, ` + colTableID + `, ` + colRowID
	rows, err := tx.Query(`SELECT `+strings.Join(sel, ", ")+` FROM `+quoteIdent(t.Name)+
		` WHERE `+strings.Join(where, " AND ")+order+` LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return TableResult{}, err
	}
	defer rows.Close()

	res := TableResult{Table: t.Name, Columns: show, Rows: []TableResultRow{}}
	titles := map[int]string{}
	for rows.Next() {
		if len(res.Rows) == limit {
			res.Truncated = true
			break
		}
		var pid, tid, rid int
		vals := make([]any, len(show))
		dest := []any{&pid, &tid, &rid}
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return TableResult{}, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		title, ok := titles[pid]
		if !ok {
			title = PageTitleByID(pid)
			titles[pid] = title
		}
		res.Rows = append(res.Rows, TableResultRow{PageID: page.FormatID(pid), Title: title,
			TableID: tid, RowID: rid, Values: vals})
	}
	if err := rows.Err(); err != nil {
		return TableResult{}, err
	}

	// 表示用は**見たまま**に近づける——引用符の要らない名前は囲まない（走らせる SQL は全部囲む）。
	dispSel := []string{colPageID, colTableID, colRowID}
	for _, c := range show {
		dispSel = append(dispSel, displayIdent(c))
	}
	disp := `SELECT ` + strings.Join(dispSel, ", ") + ` FROM ` + displayIdent(t.Name)
	if len(shown) > 0 {
		disp += ` WHERE ` + strings.Join(shown, " AND ")
	}
	res.SQL = disp + order + `;`
	return res, nil
}

// likeEscape は LIKE の `%`・`_`・`\` をただの文字にします（ESCAPE '\' と組で使う）。
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// sqlLiteral は表示用の SQL に値を埋め込みます（文字は '…'・数はそのまま）。
// ⚠ **表示用だけ**です——実際に走らせる SQL には値を埋め込みません（`?` で渡す）。
func sqlLiteral(v any) string {
	switch x := v.(type) {
	case int, int64, float64:
		return fmt.Sprint(x)
	}
	return `'` + strings.ReplaceAll(fmt.Sprint(v), `'`, `''`) + `'`
}

// displayIdent は表示用の SQL の名前です（引用符の要らない名前はそのまま・見たまま）。
func displayIdent(name string) string {
	if identNeedsQuote(name) {
		return quoteIdent(name)
	}
	return name
}

// ── HTTP ────────────────────────────────────────────────────────────────

// TablesAPIHandler は GET /api/tables——探せる表と列。
func TablesAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	list, err := TablesFor(auth.CurrentUser(r))
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "表の一覧を作れません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "tables": list})
}

// TableQueryAPIHandler は POST /api/tables/query——表を探す。
func TableQueryAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "POST だけです")
		return
	}
	var q TableQuery
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&q); err != nil {
		JSONFail(w, http.StatusBadRequest, "依頼を読めません: "+err.Error())
		return
	}
	res, err := QueryTable(auth.CurrentUser(r), q)
	if err != nil {
		if e, ok := err.(errTableQuery); ok {
			JSONFail(w, http.StatusBadRequest, e.msg)
			return
		}
		JSONFail(w, http.StatusInternalServerError, "探せませんでした: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "result": res})
}
