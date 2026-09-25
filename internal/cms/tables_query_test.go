package cms

import (
	"fmt"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 表を探す——検索画面と AI の口（2026-09-25・DBの日本語化 §7 の3段目）。

// seedQueryPages は、誰でも読める受注ページと、bob だけが読めるページを作ります。
func seedQueryPages(t *testing.T) {
	t.Helper()
	newTestFileDB(t)
	newTestTablesDB(t)
	newPage(t, "000070", `<h1>受注 みなと商店</h1><table><caption>受注明細</caption><tbody>`+
		`<tr><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>A100-B01-001</td><td>留めプレート</td><td>3</td></tr>`+
		`<tr><td>A100-B01-002</td><td>カバー 100%</td><td>10</td></tr>`+
		`</tbody></table>`, page.PageMeta{Owner: "alice", Mode: "302"})
	newPage(t, "000071", `<h1>受注 秘</h1><table><caption>受注明細</caption><tbody>`+
		`<tr><th>品番</th><th>単価</th><th>数量</th></tr>`+
		`<tr><td>A100-B01-009</td><td>9999</td><td>5</td></tr></tbody></table>`+
		`<table><caption>秘密の表</caption><tbody><tr><th>x</th></tr><tr><td>1</td></tr></tbody></table>`,
		page.PageMeta{Owner: "bob", Mode: "300"})
}

var (
	alice = &auth.User{Username: "alice"}
	admin = &auth.User{Username: "root", IsAdmin: true}
)

func mustQuery(t *testing.T, u *auth.User, q TableQuery) TableResult {
	t.Helper()
	res, err := QueryTable(u, q)
	if err != nil {
		t.Fatalf("QueryTable(%+v): %v", q, err)
	}
	return res
}

// firstColumn は結果の最初の列の値を文字で並べます。
func firstColumn(res TableResult) []string {
	var out []string
	for _, r := range res.Rows {
		if len(r.Values) > 0 {
			out = append(out, fmt.Sprint(r.Values[0]))
		}
	}
	return out
}

// TestQueryTableShowsOnlyReadableRows は、⚠ **読めるページの行だけ**が返り、読めるページに
// 無い表・列は名前も見えないことを固定します（見せ分け・C案）。
func TestQueryTableShowsOnlyReadableRows(t *testing.T) {
	seedQueryPages(t)

	res := mustQuery(t, alice, TableQuery{Table: "受注明細", Columns: []string{"品番"}})
	if got := strings.Join(firstColumn(res), ","); got != "A100-B01-001,A100-B01-002" {
		t.Errorf("⚠ alice に読めない行が見えています: %s", got)
	}
	// 静けさと成功を区別する——読める人には bob の行も出る。
	if n := len(mustQuery(t, admin, TableQuery{Table: "受注明細"}).Rows); n != 3 {
		t.Errorf("管理者には3行のはず: %d", n)
	}

	tables, err := TablesFor(alice)
	if err != nil {
		t.Fatalf("TablesFor: %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "受注明細" || tables[0].Rows != 2 {
		t.Fatalf("⚠ alice に見える表が違います（秘密の表は見えない）: %+v", tables)
	}
	// ⚠ **読めるページに値の無い列も見せない**（`単価` は bob のページにしか無い）。
	if cols := strings.Join(tables[0].Columns, ","); cols != "品番,品名,数量" {
		t.Errorf("⚠ alice に見える列が違います: %s", cols)
	}
	if _, err := QueryTable(alice, TableQuery{Table: "秘密の表"}); err == nil {
		t.Error("⚠ 読めない表を探せてしまいます")
	}
	if _, err := QueryTable(alice, TableQuery{Table: "受注明細", Columns: []string{"単価"}}); err == nil {
		t.Error("⚠ 読めるページに無い列を出せてしまいます")
	}
}

// TestQueryTableNormalizesConditions は、条件の値に **DB に入れたときと同じ正規化**が掛かる
// こと（答え4）と、4つの条件の種類を固定します。
func TestQueryTableNormalizesConditions(t *testing.T) {
	seedQueryPages(t)
	cases := []struct {
		name string
		cond TableCondition
		want string
	}{
		// code は全角・小文字でも当たる（NFKC＋大文字）。
		{"等しい（全角・小文字）", TableCondition{"品番", "eq", "ａ１００－ｂ０１－００１"}, "A100-B01-001"},
		// number は数として比べる（"10" < "3" の文字比べにしない）。
		{"以上", TableCondition{"数量", "gte", "５"}, "A100-B01-002"},
		{"以下", TableCondition{"数量", "lte", "3"}, "A100-B01-001"},
		// text は NFKC（半角カナでも当たる）。
		{"含む（半角カナ）", TableCondition{"品名", "contains", "ﾌﾟﾚｰ"}, "A100-B01-001"},
		// ⚠ `%` はただの文字（LIKE の記号として効かない）。
		{"含む（%）", TableCondition{"品名", "contains", "%"}, "A100-B01-002"},
		// 列の名前も正規化して照らすが、⚠ **中の空白は消さない**（「数 量」は「数量」と別の名前）。
		{"列名の中の空白", TableCondition{"数　量", "eq", "3"}, ""},
	}
	for _, c := range cases {
		res, err := QueryTable(alice, TableQuery{Table: "受注明細", Columns: []string{"品番"},
			Where: []TableCondition{c.cond}})
		if c.want == "" {
			// 「数 量」は「数量」とは別の名前（中の空白は消さない）——ありません、と言う。
			if err == nil {
				t.Errorf("%s: 中の空白を消して別の列に当てています", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := strings.Join(firstColumn(res), ","); got != c.want {
			t.Errorf("%s: %s（期待 %s）", c.name, got, c.want)
		}
	}
}

// TestQueryTableRefusesUnknownNames は、⚠ **名前と条件の種類は DB とコードに実際にあるもの
// だけ**が SQL に入ることを固定します（利用者の文字は SQL の文に入らない）。
func TestQueryTableRefusesUnknownNames(t *testing.T) {
	seedQueryPages(t)
	for _, q := range []TableQuery{
		{Table: `受注明細"; DROP TABLE 受注明細; --`},
		{Table: "受注明細", Columns: []string{`品番", 1; --`}},
		{Table: "受注明細", Where: []TableCondition{{"品番", "= 1 OR 1=1 --", "x"}}},
		{Table: "受注明細", Where: []TableCondition{{`品番" OR 1=1 --`, "eq", "x"}}},
	} {
		if _, err := QueryTable(alice, q); err == nil {
			t.Errorf("⚠ 知らない名前・条件を受けています: %+v", q)
		}
	}
	if n := len(mustQuery(t, admin, TableQuery{Table: "受注明細"}).Rows); n != 3 {
		t.Errorf("表が壊れています: %d 行", n)
	}
}

// TestQueryTableLimitAndSQL は、上限での打ち切りと、運用者向けの SQL の文を固定します。
func TestQueryTableLimitAndSQL(t *testing.T) {
	seedQueryPages(t)
	res := mustQuery(t, alice, TableQuery{Table: "受注明細", Columns: []string{"品番", "数量"},
		Where: []TableCondition{{"数量", "gte", "1"}, {"品名", "contains", "カバー"}}, Limit: 5})
	want := `SELECT page_id, table_id, row_id, 品番, 数量 FROM 受注明細 WHERE 数量 >= 1 AND 品名 LIKE '%カバー%' ESCAPE '\' ORDER BY page_id, table_id, row_id;`
	if res.SQL != want {
		t.Errorf("SQL の文が違います:\n%s\n（期待）\n%s", res.SQL, want)
	}
	res = mustQuery(t, alice, TableQuery{Table: "受注明細", Limit: 1})
	if len(res.Rows) != 1 || !res.Truncated {
		t.Errorf("上限で打ち切っていません: %d 行・truncated=%v", len(res.Rows), res.Truncated)
	}
	if res.Rows[0].PageID != "000070" || res.Rows[0].Title != "受注 みなと商店" || res.Rows[0].RowID != 1 {
		t.Errorf("行の出どころが違います: %+v", res.Rows[0])
	}
}
