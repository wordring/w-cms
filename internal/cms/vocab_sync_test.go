package cms

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/database"
)

// ── マーカー付き標準HTMLからの同期（dl・表） ──────────────────────────

// TestPageTagsFromDL は <dl data-type="tags"> が②汎用索引へ同期されることを
// 検証します（多値・trim・素の dl の除外）。専用テーブル page_tags は 2026-08-30 に
// 吸収済みで、タグの行き先はこの索引だけです。
func TestPageTagsFromDL(t *testing.T) {
	setupSaveTest(t)

	const id = "000040"
	body := `<h1>新形式タグ</h1>` +
		`<dl data-type="tags">` +
		`<dt>担当者</dt><dd> 紀平 </dd><dd>田中</dd>` + // 多値＝複数 dd・値は trim
		`<dt>希望納期</dt><dd>2026-07-10</dd>` +
		`</dl>` +
		`<dl><dt>用語</dt><dd>説明</dd></dl>` // data-type 無しの素の dl は対象外

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows, err := database.DB.Query(`SELECT field, value FROM vocab_index WHERE page_id = ? AND data_type = 'tags' ORDER BY field, value`, 40)
	if err != nil {
		t.Fatalf("vocab_indexのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n, v string
		rows.Scan(&n, &v)
		got = append(got, n+"="+v)
	}
	want := []string{"希望納期=2026-07-10", "担当者=田中", "担当者=紀平"}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("可変タグの索引の内容が期待と異なります:\ngot  %v\nwant %v", got, want)
	}
}

// TestTagValueFromDL は TagValue が <dl data-type="tags"> から値を引けることを
// 検証します（部材手配計算の part_id 注入の要）。旧 <m-tag> の読み取り（短期の保険）は
// 実データの一括変換完了後に除去したため、読めないことも固定します。
func TestTagValueFromDL(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"dt の表示文字", `<dl data-type="tags"><dt>部品番号</dt><dd>SHAFT-01</dd></dl>`},
	}
	for _, c := range cases {
		root := mustParse(t, c.body)
		if got := TagValue(root, "部品番号"); got != "SHAFT-01" {
			t.Errorf("%s: TagValue = %q, want SHAFT-01", c.name, got)
		}
	}

	// 機械キーの属性は撤去済み。残っていても鍵にはならず、dt の表示文字だけを見る。
	stale := mustParse(t, `<dl data-type="tags"><dt>品番</dt><dd data-field="部品番号">SHAFT-01</dd></dl>`)
	if got := TagValue(stale, "部品番号"); got != "" {
		t.Errorf("撤去した data-field が鍵として読まれています: %q", got)
	}

	legacy := mustParse(t, `<m-tag name="部品番号" value="SHAFT-01"></m-tag>`)
	if got := TagValue(legacy, "部品番号"); got != "" {
		t.Errorf("旧 <m-tag> が読めてしまいます（除去済みのはず）: %q", got)
	}
}

// TestPartMaterialsFromTable は新形式 <table data-type="part-materials"> が
// 汎用索引へ載ることを検証します（見出しラベルからの解決・数値の正規化・
// quantity 空セルの既定値 1）。専用テーブル part_materials は D-1 で廃止し、
// 部品番号はページのタグから逆引きします（pagesByTag）。
func TestPartMaterialsFromTable(t *testing.T) {
	setupSaveTest(t)

	const id = "000041"
	body := `<dl data-type="tags"><dt>部品番号</dt><dd>SHAFT-01</dd></dl>` +
		`<table data-type="part-materials"><tbody>` +
		// 列は見出しの表示文字からレジストリの Label 経由で機械キーへ解決される
		`<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>` +
		`<tr><td>丸鋼材</td><td>¥8,000</td><td>大同特殊鋼</td><td>2</td></tr>` +
		`<tr><td>ベアリング</td><td>500</td><td>NSK</td><td></td></tr>` + // quantity 空 → 1
		`</tbody></table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 部品番号はタグからの逆引きで解決される（部材行そのものには無い）
	pages, err := PagesByTag(database.DB, "部品番号", "SHAFT-01")
	if err != nil {
		t.Fatalf("pagesByTagエラー: %v", err)
	}
	if len(pages) != 1 || pages[0] != 41 {
		t.Fatalf("部品番号タグの逆引きが期待と異なります: %v", pages)
	}

	rows := queryPartMaterials(t, 41)
	want := []string{
		"丸鋼材|8000|大同特殊鋼|2",
		"ベアリング|500|NSK|1",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("部材表の索引内容が期待と異なります:\ngot  %v\nwant %v", rows, want)
	}
}

// ── ヘルパ ───────────────────────────────────────────────────────────

// queryPartMaterials は部材表の索引行を「部材名|単価|仕入先|数量」の文書順で返します。
func queryPartMaterials(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := VocabTableRowsOf(database.DB, pageID, "part-materials")
	if err != nil {
		t.Fatalf("部材表の索引の読み出しエラー: %v", err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, strings.Join([]string{
			r.Values["item-name"],
			itoa(r.Num("cost")),
			r.Values["supplier-name"],
			itoa(vocabQuantity(r)),
		}, "|"))
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// mustParse は本文HTMLをノード木としてパースします（TagValue 等の単体検証用）。
func mustParse(t *testing.T, body string) *html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("パースエラー: %v", err)
	}
	return root
}
