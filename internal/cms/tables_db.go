package cms

// ─────────────────────────────────────────────────────────────────────────
// 本文の表を「キャプションの名前の表」として写す（2026-09-25・docs/【考察】DBの日本語化.md）
//
// 利用者:「見たままを実現するには、表のキャプションそのままのテーブルや表の列の名前
// そのままの検索が欲しい」「表の種類が増えても一般的なルールで扱いたい」。
//
//	本文:  <table><caption>受注明細</caption><tr><th>品番</th><th>数量</th></tr>…
//	DB:    SELECT 品番, 数量 FROM 受注明細 WHERE …          （data/tables.db）
//
// 規則（決定は同書 §6）:
//
//   - **キャプションのある表だけ**を入れる（名前の無い表は入れない）。
//   - 表の名前はキャプション、列の名前は見出し。どちらも**同じ規則で正規化**する
//     （NFKC＝英数字は半角・仮名は全角／前後の空白を落とす／続いた空白は1つ——TableName）。
//   - 同じ名前の表が複数のページ（や1ページに複数）にあれば**1つの表に入れ、列を統合**する。
//   - 行ごとに `page_id`・`table_id`（そのページで同じ名前の何番目の表か）・`row_id`（何行目か）。
//   - 値は**列の名前から語彙で型を決めて正規化**した値。畳めなければ書いたまま。
//
// ⚠ **表の名前・列の名前は、利用者が本文に書いた文字です**——SQL に埋め込むときは必ず
// quoteIdent を通します（`?` では渡せない）。囲み忘れると、キャプションの文字が SQL として
// 走ります。**名前を SQL に入れる経路は、このファイルだけに閉じます。**
//
// ⚠ **移行のあいだは cms.db の vocab_index にも書き続けます**——いまの集計（必要部材表・
// 受注残など）がそちらを読んでいるためです。読み手を全部こちらへ移したら vocab_index を
// 消します（同書 §7）。
//
// ⚠ **表や列は消しません**（見出しを直すと古い列は空のまま残る）。DB再構築で作り直すと
// 消えます——運用者に見せる「列の揃っていない表」の一覧（同書 §7 の2段目）で気づけます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"w-cms/internal/database"
)

// 行の出どころの列（2026-09-25 利用者:「page_id、table_id などアルファベットで良いです」）。
const (
	colPageID  = "page_id"
	colTableID = "table_id"
	colRowID   = "row_id"
)

// systemColumnSuffix は、見出しが出どころの列と同じ名前だったときに付ける印です
// （`page_id` という見出しは `page_id_列` として入る）。
const systemColumnSuffix = "_列"

// 数の上限（同書 §3.3 の4）——キャプションを打ち間違えるたびに表が増えるため。
const (
	maxCaptionTables = 1000
	// SQLite の列の上限は既定で2000。出どころの列ぶんを引き、余裕を見る。
	maxTableColumns = 1900
)

// TableName は表の名前・列の名前を正規化します（同書 §3.2）。
//
// NFKC（英数字は半角・仮名は全角）と設定の置き換え表（NormalizeText）を掛け、
// 前後の空白を落とし、続いた空白を1つにまとめます。⚠ **中の空白は消しません**
// （`品 番` と `品番` は別の列）。
func TableName(s string) string {
	return strings.Join(strings.Fields(NormalizeText(s)), " ")
}

// quoteIdent は名前を SQL の識別子として囲みます（中の `"` は `""` に）。
//
// ⚠ **名前を SQL に埋め込むときは、必ずここを通すこと**（上の冒頭）。
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// asciiFold は SQLite が名前を比べるのと同じく、**ASCII の英字だけ**を小文字にします。
//
// SQLite は名前の ASCII の大文字小文字を区別しません（`Qty` と `QTY` は同じ列）。
// ASCII 以外の文字は区別します。比べ方をそろえないと、同じ列を2回足そうとして落ちます。
func asciiFold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// isSystemColumn は、名前が出どころの列（page_id・table_id・row_id）と重なるかを返します。
func isSystemColumn(name string) bool {
	switch asciiFold(name) {
	case colPageID, colTableID, colRowID:
		return true
	}
	return false
}

// ── 引用符の要る名前 ─────────────────────────────────────────────────────

// identNeedsQuote は、名前を SQL に**引用符なしで**書けないかを返します
// （空白・記号を含む／数字で始まる／予約語）。
//
// 引用符なしで書ける文字は SQLite の字句規則のとおり——英数字・`_`・`$`・**ASCII 以外の
// すべての文字**（だから日本語はそのまま書ける）。先頭は数字と `$` を除く。
func identNeedsQuote(name string) bool {
	if name == "" {
		return true
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 0x80, c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9', c == '$':
			if i == 0 {
				return true
			}
		default:
			return true
		}
	}
	return sqlKeywordNeedsQuote(name)
}

// sqlKeywords は SQLite の予約語（https://www.sqlite.org/lang_keywords.html・147語）です。
//
// ⚠ **すべてが引用符を要るわけではありません**——SQLite は多くの予約語を名前としても
// 受けます（`KEY`・`ABORT` など）。どれが要るかは sqlKeywordNeedsQuote が**実際に試して**
// 決めます（表を覚えると、SQLite の版で変わったときに嘘になる）。
var sqlKeywords = []string{
	"ABORT", "ACTION", "ADD", "AFTER", "ALL", "ALTER", "ALWAYS", "ANALYZE", "AND", "AS",
	"ASC", "ATTACH", "AUTOINCREMENT", "BEFORE", "BEGIN", "BETWEEN", "BY", "CASCADE", "CASE",
	"CAST", "CHECK", "COLLATE", "COLUMN", "COMMIT", "CONFLICT", "CONSTRAINT", "CREATE",
	"CROSS", "CURRENT", "CURRENT_DATE", "CURRENT_TIME", "CURRENT_TIMESTAMP", "DATABASE",
	"DEFAULT", "DEFERRABLE", "DEFERRED", "DELETE", "DESC", "DETACH", "DISTINCT", "DO", "DROP",
	"EACH", "ELSE", "END", "ESCAPE", "EXCEPT", "EXCLUDE", "EXCLUSIVE", "EXISTS", "EXPLAIN",
	"FAIL", "FILTER", "FIRST", "FOLLOWING", "FOR", "FOREIGN", "FROM", "FULL", "GENERATED",
	"GLOB", "GROUP", "GROUPS", "HAVING", "IF", "IGNORE", "IMMEDIATE", "IN", "INDEX", "INDEXED",
	"INITIALLY", "INNER", "INSERT", "INSTEAD", "INTERSECT", "INTO", "IS", "ISNULL", "JOIN",
	"KEY", "LAST", "LEFT", "LIKE", "LIMIT", "MATCH", "MATERIALIZED", "NATURAL", "NO", "NOT",
	"NOTHING", "NOTNULL", "NULL", "NULLS", "OF", "OFFSET", "ON", "OR", "ORDER", "OTHERS",
	"OUTER", "OVER", "PARTITION", "PLAN", "PRAGMA", "PRECEDING", "PRIMARY", "QUERY", "RAISE",
	"RANGE", "RECURSIVE", "REFERENCES", "REGEXP", "REINDEX", "RELEASE", "RENAME", "REPLACE",
	"RESTRICT", "RETURNING", "RIGHT", "ROLLBACK", "ROW", "ROWS", "SAVEPOINT", "SELECT", "SET",
	"TABLE", "TEMP", "TEMPORARY", "THEN", "TIES", "TO", "TRANSACTION", "TRIGGER", "UNBOUNDED",
	"UNION", "UNIQUE", "UPDATE", "USING", "VACUUM", "VALUES", "VIEW", "VIRTUAL", "WHEN",
	"WHERE", "WINDOW", "WITH", "WITHOUT",
}

var (
	keywordOnce      sync.Once
	keywordNeedQuote map[string]bool
)

// sqlKeywordNeedsQuote は、予約語のうち**引用符が要るもの**かを返します。
//
// 1度だけ、使い捨てのメモリ上の SQLite で、**列の名前として・表の名前として**引用符なしで
// 書いて試します。⚠ **試すのは固定の予約語だけ**です——利用者の文字を引用符なしで SQL に
// 入れることはしません。
func sqlKeywordNeedsQuote(name string) bool {
	keywordOnce.Do(func() {
		keywordNeedQuote = map[string]bool{}
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			for _, k := range sqlKeywords {
				keywordNeedQuote[k] = true // 試せないなら、予約語は全部要ると言う（多めに知らせる）
			}
			return
		}
		defer db.Close()
		for _, k := range sqlKeywords {
			var v int
			asColumn := db.QueryRow(`SELECT ` + k + ` FROM (SELECT 1 AS ` + quoteIdent(k) + `)`).Scan(&v)
			asTable := db.QueryRow(`WITH ` + quoteIdent(k) + ` AS (SELECT 1 AS x) SELECT x FROM ` + k).Scan(&v)
			if asColumn != nil || asTable != nil {
				keywordNeedQuote[k] = true
			}
		}
	})
	return keywordNeedQuote[strings.ToUpper(name)]
}

// ── 本文から表を読む ─────────────────────────────────────────────────────

// captionTable は本文の中の、キャプションのある表1つです。
type captionTable struct {
	Name    string     // 正規化したキャプション＝表の名前
	No      int        // そのページで同じ名前の何番目の表か（1から）
	Headers []string   // 正規化した見出し（空の見出しは ""）
	Rows    []tableRow // データ行（空の行は入れない）
}

// tableRow はデータ行1つです。
type tableRow struct {
	No    int      // 見出しを除いて何行目か（1から・空の行も数える＝画面の位置と揃える）
	Cells []string // セルの文字（前後の空白を落とす・<br> は改行）
}

// captionTablesOf は本文から、キャプションのある表を文書順に読みます。
//
// 見出しは**最初の行**です（本文の表の約束・索引と同じ）。サーバーが足したもの
// （`.vocab-chrome`）の中は読みません。
func captionTablesOf(root *html.Node) []captionTable {
	var out []captionTable
	seen := map[string]int{}
	WalkElements(root, func(n *html.Node) {
		if n.Data != "table" || inVocabChrome(n) {
			return
		}
		name := TableName(tableCaption(n))
		if name == "" {
			return
		}
		rows := tableRows(n)
		if len(rows) == 0 {
			return
		}
		key := asciiFold(name)
		seen[key]++
		ct := captionTable{Name: name, No: seen[key]}
		for _, c := range rowCells(rows[0]) {
			ct.Headers = append(ct.Headers, TableName(cellText(c)))
		}
		for i, tr := range rows[1:] {
			var cells []string
			blank := true
			for _, c := range rowCells(tr) {
				v := strings.TrimSpace(cellText(c))
				if v != "" {
					blank = false
				}
				cells = append(cells, v)
			}
			if !blank {
				ct.Rows = append(ct.Rows, tableRow{No: i + 1, Cells: cells})
			}
		}
		out = append(out, ct)
	})
	return out
}

// cellText はセルの文字を返します。`<br>` は改行にします（複数行の備考を1行に潰さない）。
func cellText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			sb.WriteString(n.Data)
			return
		case n.Type == html.ElementNode && n.Data == "br":
			sb.WriteString("\n")
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// tableCellValue は、列の名前から型を決めて正規化した値を返します（同書 §3.7）。
//
// ⚠ **畳めない値は、書いたまま入れます**（date の列の `最短納期` など）——落とすと
// 「書いてあるのに出てこない」。数は**数として**入れます（`"8000" < "900"` にしない）。
func tableCellValue(column, raw string) any {
	typ := InferColumnType(column)
	if n, ok := NormalizeValue(typ, raw); ok && n != "" {
		return tagNormBind(typ, n)
	}
	return raw
}

// ── 除外 ────────────────────────────────────────────────────────────────

// tablesExclusions は「このページは表を DB に入れない」を拡張が言うための口です。
var tablesExclusions []func(root *html.Node) bool

// RegisterTablesExclusion は、表を DB に入れないページの判定を登録します。
//
// ⚠ **業務の言葉は拡張から渡します**（開発方針 §0）——例えば板金の拡張は「`移行中` の
// タグがあるページは入れない」を登録します（移植の手順・DBの日本語化 §3.6）。
// コアが自分で知っているのはテンプレート領域だけです。
func RegisterTablesExclusion(f func(root *html.Node) bool) {
	tablesExclusions = append(tablesExclusions, f)
}

func excludedFromTables(id string, root *html.Node) bool {
	if IsTemplateArea(id) {
		return true
	}
	for _, f := range tablesExclusions {
		if f(root) {
			return true
		}
	}
	return false
}

// ── 書き込み ────────────────────────────────────────────────────────────

// syncCaptionTables は、1ページぶんの表を data/tables.db へ洗い替えます
// （そのページの行を全部の表から消してから、いまの本文の表を入れる）。
//
// root が nil（本文が空＝ページを消した）なら、消すだけです。
// TablesDB が開いていなければ何もしません。
func syncCaptionTables(id string, pageID int, root *html.Node) error {
	db := database.TablesDB
	if db == nil {
		return nil
	}
	var tables []captionTable
	if root != nil && !excludedFromTables(id, root) {
		tables = captionTablesOf(root)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := existingTables(tx)
	if err != nil {
		return err
	}
	for _, t := range existing {
		if !t.Ours {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM `+quoteIdent(t.Name)+` WHERE `+colPageID+` = ?`, pageID); err != nil {
			return err
		}
	}
	for _, ct := range tables {
		if err := insertCaptionTable(tx, existing, pageID, ct); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// dbTable は data/tables.db にある表1つです。
type dbTable struct {
	Name string
	// Ours は**この仕組みが作った表か**（出どころの列 page_id を持つか）。
	// ⚠ 運用者が SQL の道具で自分の表を作ることがあります——そういう表には触りません
	// （触ると、page_id が無くて**以後の保存の同期が全部落ちます**・2026-09-25 に試験で踏んだ）。
	Ours bool
}

// existingTables は data/tables.db にある表を、比べる鍵（asciiFold）→ 表で返します。
func existingTables(tx *sql.Tx) (map[string]dbTable, error) {
	rows, err := tx.Query(`SELECT m.name, EXISTS (SELECT 1 FROM pragma_table_info(m.name) WHERE name = '` +
		colPageID + `') FROM sqlite_master m WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]dbTable{}
	for rows.Next() {
		var t dbTable
		if err := rows.Scan(&t.Name, &t.Ours); err != nil {
			return nil, err
		}
		out[asciiFold(t.Name)] = t
	}
	return out, rows.Err()
}

// tableColumns は表の列を、比べる鍵（asciiFold）→ 実際の名前で返します。
func tableColumns(tx *sql.Tx, table string) (map[string]string, error) {
	// ⚠ 表の名前は `?` で渡せる形（表を返す PRAGMA 関数）を使う——引用符の出番を減らす。
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[asciiFold(name)] = name
	}
	return out, rows.Err()
}

// insertCaptionTable は表1つぶんを入れます（表・列が無ければ作る）。
func insertCaptionTable(tx *sql.Tx, existing map[string]dbTable, pageID int, ct captionTable) error {
	key := asciiFold(ct.Name)
	found, ok := existing[key]
	if ok && !found.Ours {
		log.Printf("表 %q は運用者が作った表と同じ名前なので DB に入れません（page=%d）", ct.Name, pageID)
		return nil
	}
	table := found.Name
	if !ok {
		if strings.HasPrefix(key, "sqlite_") {
			log.Printf("表 %q は SQLite の予約した名前なので DB に入れません（page=%d）", ct.Name, pageID)
			return nil
		}
		if len(existing) >= maxCaptionTables {
			log.Printf("表の数が上限（%d）に達したので %q を DB に入れません（page=%d）",
				maxCaptionTables, ct.Name, pageID)
			return nil
		}
		// ⚠ 列に型を宣言しません——型は値が持つ（数は数・文字は文字として比べられる）。
		if _, err := tx.Exec(`CREATE TABLE ` + quoteIdent(ct.Name) + ` (` +
			colPageID + ` INTEGER, ` + colTableID + ` INTEGER, ` + colRowID + ` INTEGER)`); err != nil {
			return fmt.Errorf("表 %q を作れません: %w", ct.Name, err)
		}
		existing[key] = dbTable{Name: ct.Name, Ours: true}
		table = ct.Name
	}

	cols, err := tableColumns(tx, table)
	if err != nil {
		return err
	}
	// 見出し i がどの列に入るか（"" は入れない）。
	colFor := make([]string, len(ct.Headers))
	used := map[string]bool{}
	for i, h := range ct.Headers {
		if h == "" {
			continue
		}
		if isSystemColumn(h) {
			h += systemColumnSuffix
		}
		k := asciiFold(h)
		if used[k] {
			continue // 1つの表で見出しが重なったら先勝ち（索引と同じ規則）
		}
		used[k] = true
		if actual, ok := cols[k]; ok {
			colFor[i] = actual
			continue
		}
		if len(cols) >= maxTableColumns {
			log.Printf("表 %q の列が上限（%d）に達したので %q を入れません（page=%d）",
				table, maxTableColumns, h, pageID)
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE ` + quoteIdent(table) + ` ADD COLUMN ` + quoteIdent(h)); err != nil {
			return fmt.Errorf("表 %q に列 %q を足せません: %w", table, h, err)
		}
		cols[k] = h
		colFor[i] = h
	}

	for _, r := range ct.Rows {
		names := []string{colPageID, colTableID, colRowID}
		args := []any{pageID, ct.No, r.No}
		for i, v := range r.Cells {
			if i >= len(colFor) || colFor[i] == "" || v == "" {
				continue
			}
			names = append(names, quoteIdent(colFor[i]))
			args = append(args, tableCellValue(colFor[i], v))
		}
		q := `INSERT INTO ` + quoteIdent(table) + ` (` + strings.Join(names, ", ") +
			`) VALUES (` + strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ") + `)`
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("表 %q へ行を入れられません: %w", table, err)
		}
	}
	return nil
}

// ResetTablesDB は data/tables.db の表を全部消します（DB再構築の最初に呼ぶ）。
//
// ⚠ **ファイルは消しません**——運用者が SQL の道具で開いていると、Windows では消せない
// （cms.db の再構築と同じ理由）。
func ResetTablesDB() error {
	db := database.TablesDB
	if db == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := existingTables(tx)
	if err != nil {
		return err
	}
	for _, t := range existing {
		if !t.Ours {
			continue // 運用者が作った表は消さない
		}
		if _, err := tx.Exec(`DROP TABLE ` + quoteIdent(t.Name)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`PRAGMA user_version = ` + fmt.Sprint(tablesDBVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// tablesDBVersion は data/tables.db の作り方の版です。起動時にこれより古ければ作り直します
// （RebuildIfNeeded）。作り方を変えたら上げること。
const tablesDBVersion = 1

// tablesDBOutdated は data/tables.db が今の作り方で作られていないかを返します。
func tablesDBOutdated() bool {
	db := database.TablesDB
	if db == nil {
		return false
	}
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return true
	}
	return v < tablesDBVersion
}

// ── 保存時の告知 ─────────────────────────────────────────────────────────

// TableNameNotes は、本文の表の名前・見出しのうち、**SQL で引くときに気をつけるもの**を
// 告知の文にして返します（保存の応答で画面に出す・拒否はしない）。
//
// 利用者:「表の見出しに予約語が来たら警告してください」。引用符が要る名前（予約語・
// 空白や記号を含む・数字で始まる）と、出どころの列（page_id など）と重なる見出しを言います。
func TableNameNotes(bodyHTML string) []string {
	root, err := html.Parse(strings.NewReader(bodyHTML))
	if err != nil {
		return nil
	}
	var notes []string
	said := map[string]bool{}
	say := func(s string) {
		if !said[s] {
			said[s] = true
			notes = append(notes, s)
		}
	}
	for _, ct := range captionTablesOf(root) {
		if identNeedsQuote(ct.Name) {
			say("表「" + ct.Name + "」は SQL で引くとき " + quoteIdent(ct.Name) + " と引用符で囲む必要があります")
		}
		for _, h := range ct.Headers {
			switch {
			case h == "":
			case isSystemColumn(h):
				say("表「" + ct.Name + "」の見出し「" + h + "」は DB の予約した列と同じ名前なので、" +
					"DB では「" + h + systemColumnSuffix + "」になります")
			case identNeedsQuote(h):
				say("表「" + ct.Name + "」の見出し「" + h + "」は SQL で引くとき " + quoteIdent(h) +
					" と引用符で囲む必要があります")
			}
		}
	}
	return notes
}
