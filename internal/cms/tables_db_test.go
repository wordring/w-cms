package cms

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/database"
)

// 表の写し（data/tables.db・キャプションの名前の表）——2026-09-25・docs/【考察】DBの日本語化.md。

// newTestTablesDB は試験用の data/tables.db を開き、終わったら閉じて nil に戻します。
// ⚠ **nil に戻すこと**——開いたままだと、後の試験の SyncIndex が消えたファイルへ書きます。
func newTestTablesDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tables.db"))
	if err != nil {
		t.Fatalf("tables.db を開けません: %v", err)
	}
	database.TablesDB = db
	t.Cleanup(func() {
		database.TablesDB = nil
		db.Close()
	})
	return db
}

// queryStrings は1列の結果を文字で集めます（NULL は "<nil>"）。
func queryStrings(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !v.Valid {
			out = append(out, "<nil>")
		} else {
			out = append(out, v.String)
		}
	}
	return out
}

// TestTableNameNormalizes は、名前の正規化（英数字は半角・仮名は全角・空白）を固定します。
func TestTableNameNormalizes(t *testing.T) {
	for in, want := range map[string]string{
		"ＡＢＣ　品番 ":   "ABC 品番",
		"ﾌﾟﾚｰﾄ":      "プレート",
		"  a   b  ": "a b",
		"品 番":        "品 番", // 中の空白は消さない
		"受注明細":       "受注明細",
	} {
		if got := TableName(in); got != want {
			t.Errorf("TableName(%q) = %q（期待 %q）", in, got, want)
		}
	}
}

// TestCaptionTablesGoToTablesDB は、**キャプションのある表だけ**が、その名前の表として入り、
// **列が統合され**、値が型で正規化されることを固定します。
func TestCaptionTablesGoToTablesDB(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)

	pageA := `<h1>受注A</h1>` +
		`<table><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>数量</th></tr>` +
		`<tr><td>a100 b01 001</td><td>留めﾌﾟﾚｰﾄ</td><td>３</td></tr>` +
		`<tr><td></td><td></td><td></td></tr>` + // 空の行は入れない（でも行番号は数える）
		`<tr><td>A100-B01-002</td><td>カバー</td><td>1,200</td></tr>` +
		`</tbody></table>` +
		`<table><tbody><tr><th>名前の無い表</th></tr><tr><td>入らない</td></tr></tbody></table>` +
		// 同じページの2つ目の「受注明細」——列が1つ多い（統合される）
		`<table><caption> 受注明細 </caption><tbody>` +
		`<tr><th>品番</th><th>備考</th></tr><tr><td>A100-B01-003</td><td>一行目<br>二行目</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000010", pageA); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	pageB := `<h1>受注B</h1><table><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>Qty</th></tr><tr><td>K120-01-211</td><td>5</td></tr></tbody></table>` +
		`<table><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>QTY</th></tr><tr><td>K120-01-212</td><td>6</td></tr></tbody></table>`
	if err := SyncIndex("000011", pageB); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}

	tables := queryStrings(t, tdb, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if strings.Join(tables, ",") != "受注明細" {
		t.Fatalf("表は「受注明細」1つのはず（名前の無い表は入らない）: %v", tables)
	}
	// 列の統合——見出しの和集合。Qty と QTY は SQLite では同じ列（ASCII の大小を区別しない）。
	cols := queryStrings(t, tdb, `SELECT name FROM pragma_table_info('受注明細')`)
	if got := strings.Join(cols, ","); got != "page_id,table_id,row_id,品番,品名,数量,備考,Qty" {
		t.Errorf("列が違います: %s", got)
	}

	// 日本語の名前は**引用符なしで**引ける（これがやりたかったこと）。
	got := queryStrings(t, tdb,
		`SELECT 品番 || '|' || coalesce(品名,'') || '|' || coalesce(数量,'') || '|' || table_id || '|' || row_id
		   FROM 受注明細 WHERE page_id = 10 ORDER BY table_id, row_id`)
	want := []string{
		"A100B01001|留めプレート|3|1|1", // code は畳む・text は NFKC・number は数
		"A100-B01-002|カバー|1200|1|3",  // 空の行（2行目）を飛ばしても行番号は画面の位置のまま
		"A100-B01-003|||2|1",          // 同じページの2つ目の表は table_id=2
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("行が違います:\n%s\n（期待）\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// 数は**数として**入る（"8000" < "900" の事故を起こさない）。
	if typ := queryStrings(t, tdb, `SELECT typeof(数量) FROM 受注明細 WHERE row_id = 3 AND page_id = 10`); len(typ) != 1 || typ[0] != "integer" {
		t.Errorf("数量が数として入っていません: %v", typ)
	}
	// <br> は改行として残る（複数行の備考を1行に潰さない）。
	if note := queryStrings(t, tdb, `SELECT 備考 FROM 受注明細 WHERE page_id = 10 AND table_id = 2`); len(note) != 1 || note[0] != "一行目\n二行目" {
		t.Errorf("備考の改行が落ちています: %q", note)
	}
	if qty := queryStrings(t, tdb, `SELECT qty FROM 受注明細 WHERE page_id = 11 ORDER BY table_id`); strings.Join(qty, ",") != "5,6" {
		t.Errorf("Qty と QTY が同じ列に入っていません: %v", qty)
	}
}

// TestTablesDBReplacesPageRows は、保存のたびに**そのページの行だけ**入れ替わり、
// ページを消すと行も消えることを固定します。
func TestTablesDBReplacesPageRows(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)
	body := func(items ...string) string {
		b := `<h1>p</h1><table><caption>発注明細</caption><tbody><tr><th>品名</th></tr>`
		for _, it := range items {
			b += `<tr><td>` + it + `</td></tr>`
		}
		return b + `</tbody></table>`
	}
	for id, items := range map[string][]string{"000020": {"甲", "乙"}, "000021": {"丙"}} {
		if err := SyncIndex(id, body(items...)); err != nil {
			t.Fatalf("SyncIndex: %v", err)
		}
	}
	if err := SyncIndex("000020", body("丁")); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	if got := queryStrings(t, tdb, `SELECT 品名 FROM 発注明細 ORDER BY page_id, row_id`); strings.Join(got, ",") != "丁,丙" {
		t.Errorf("入れ替わっていません: %v", got)
	}
	if err := PurgePageIndex("000020"); err != nil {
		t.Fatalf("PurgePageIndex: %v", err)
	}
	if got := queryStrings(t, tdb, `SELECT 品名 FROM 発注明細`); strings.Join(got, ",") != "丙" {
		t.Errorf("消したページの行が残っています: %v", got)
	}
}

// TestTablesDBQuotesNames は、⚠ **利用者の書いた名前が SQL として走らない**ことを固定します。
//
// キャプションと見出しは本文に誰でも書けます。引用符で囲まずに SQL へ入れると、
// キャプションの文字が SQL として走ります。
func TestTablesDBQuotesNames(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)
	if _, err := tdb.Exec(`CREATE TABLE 守る表 (x)`); err != nil {
		t.Fatalf("前準備: %v", err)
	}
	evil := `x"); DROP TABLE 守る表; --`
	body := `<h1>p</h1><table><caption>` + html.EscapeString(evil) + `</caption><tbody>` +
		`<tr><th>a"b</th><th>page_id</th><th>sqlite_x</th></tr>` +
		`<tr><td>1</td><td>2</td><td>3</td></tr></tbody></table>` +
		`<table><caption>sqlite_master</caption><tbody><tr><th>x</th></tr><tr><td>1</td></tr></tbody></table>`
	if err := SyncIndex("000030", body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	tables := queryStrings(t, tdb, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if !contains(tables, "守る表") {
		t.Fatalf("⚠ キャプションの文字が SQL として走りました（表が消えた）: %v", tables)
	}
	if !contains(tables, evil) {
		t.Errorf("名前どおりの表ができていません: %v", tables)
	}
	cols := queryStrings(t, tdb, `SELECT name FROM pragma_table_info(?)`, evil)
	// 見出しの `page_id` は出どころの列とぶつかるので `page_id_列` に。
	if got := strings.Join(cols, ","); got != `page_id,table_id,row_id,a"b,page_id_列,sqlite_x` {
		t.Errorf("列が違います: %s", got)
	}
	var v string
	if err := tdb.QueryRow(`SELECT "a""b" || '/' || page_id_列 || '/' || page_id FROM `+quoteIdent(evil)).Scan(&v); err != nil || v != "1/2/30" {
		t.Errorf("値が違います: %q %v", v, err)
	}
}

// TestTablesDBExclusions は、テンプレート領域と、拡張が「入れない」と言ったページの表が
// 入らないことを固定します（移植の手順・DBの日本語化 §3.6）。
func TestTablesDBExclusions(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)
	saved := tablesExclusions
	t.Cleanup(func() { tablesExclusions = saved })
	RegisterTablesExclusion(func(root *html.Node) bool { return HasTag(root, "試験の印") })

	table := `<table><caption>材料</caption><tbody><tr><th>材質</th></tr><tr><td>鉄</td></tr></tbody></table>`
	// 値の無いタグでも「在るだけで止める」。
	marked := `<h1>p</h1><dl data-type="tags"><dt>試験の印</dt><dd><br></dd></dl>` + table
	if err := SyncIndex("000040", marked); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	if n := queryStrings(t, tdb, `SELECT name FROM sqlite_master WHERE type = 'table'`); len(n) != 0 {
		t.Errorf("⚠ 入れないはずのページの表が入っています: %v", n)
	}
	// 静けさと成功を区別する——印が無ければ入る。
	if err := SyncIndex("000041", `<h1>q</h1>`+table); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	if got := queryStrings(t, tdb, `SELECT 材質 FROM 材料`); strings.Join(got, ",") != "鉄" {
		t.Errorf("印の無いページの表が入っていません: %v", got)
	}
}

// TestTableNameNotes は、SQL で引くとき気をつける名前の告知を固定します
// （利用者:「表の見出しに予約語が来たら警告してください」）。
func TestTableNameNotes(t *testing.T) {
	body := `<table><caption>受注 明細</caption><tbody>` +
		`<tr><th>品名</th><th>Order</th><th>単価(円)</th><th>1列目</th><th>row_id</th><th>数量</th></tr>` +
		`</tbody></table>`
	notes := strings.Join(TableNameNotes(body), "\n")
	for _, s := range []string{`表「受注 明細」は SQL で引くとき "受注 明細"`, `「Order」`, `「単価(円)」`,
		`「1列目」`, `「row_id」は DB の予約した列`} {
		if !strings.Contains(notes, s) {
			t.Errorf("%s が告知されていません:\n%s", s, notes)
		}
	}
	for _, s := range []string{"「品名」", "「数量」"} {
		if strings.Contains(notes, s) {
			t.Errorf("⚠ 引用符の要らない %s まで告知しています:\n%s", s, notes)
		}
	}
}

// TestIdentNeedsQuote は、引用符の要る名前の見分けを固定します（予約語は実際に試して決める）。
func TestIdentNeedsQuote(t *testing.T) {
	for name, want := range map[string]bool{
		"品番": false, "受注明細": false, "顧客の発注書（読んだまま）": false, "Qty": false, "_x": false,
		"Order": true, "select": true, "受注 明細": true, "単価(円)": true, "1列": true, "": true,
	} {
		if got := identNeedsQuote(name); got != want {
			t.Errorf("identNeedsQuote(%q) = %v（期待 %v）", name, got, want)
		}
	}
}

// TestResetTablesDB は、DB再構築で写しが空になり、今の作り方の印が付くことを固定します。
func TestResetTablesDB(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)
	if !tablesDBOutdated() {
		t.Fatal("作ったばかりの写しは「古い」はず（初回に作り直すため）")
	}
	if err := SyncIndex("000050", `<h1>p</h1><table><caption>材料</caption><tbody><tr><th>材質</th></tr><tr><td>鉄</td></tr></tbody></table>`); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	if err := ResetTablesDB(); err != nil {
		t.Fatalf("ResetTablesDB: %v", err)
	}
	if n := queryStrings(t, tdb, `SELECT name FROM sqlite_master WHERE type = 'table'`); len(n) != 0 {
		t.Errorf("表が残っています: %v", n)
	}
	if tablesDBOutdated() {
		t.Error("作り直したあとも「古い」と言っています")
	}
}
