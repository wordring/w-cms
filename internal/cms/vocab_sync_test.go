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

// TestPageTagsFromDL は新形式 <dl data-type="tags"> が page_tags へ同期されることを
// 検証します（多値・trim・親ページID除外・従来テストと同じ観点）。
func TestPageTagsFromDL(t *testing.T) {
	setupSaveTest(t)

	const id = "000040"
	body := `<h1>新形式タグ</h1>` +
		`<dl data-type="tags">` +
		`<dt>担当者</dt><dd> 紀平 </dd><dd>田中</dd>` + // 多値＝複数 dd・値は trim
		`<dt>親ページID</dt><dd>000000</dd>` + // 遺物の名前は新形式でも除外
		`<dt>希望納期</dt><dd>2026-07-10</dd>` +
		`</dl>` +
		`<dl><dt>用語</dt><dd>説明</dd></dl>` // data-type 無しの素の dl は対象外

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows, err := database.DB.Query(`SELECT name, value FROM page_tags WHERE page_id = ? ORDER BY name, value`, 40)
	if err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
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
		t.Errorf("page_tags の内容が期待と異なります:\ngot  %v\nwant %v", got, want)
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
		{"data-field優先", `<dl data-type="tags"><dt>品番</dt><dd data-field="部品番号">SHAFT-01</dd></dl>`},
	}
	for _, c := range cases {
		root := mustParse(t, c.body)
		if got := TagValue(root, "部品番号"); got != "SHAFT-01" {
			t.Errorf("%s: TagValue = %q, want SHAFT-01", c.name, got)
		}
	}

	legacy := mustParse(t, `<m-tag name="部品番号" value="SHAFT-01"></m-tag>`)
	if got := TagValue(legacy, "部品番号"); got != "" {
		t.Errorf("旧 <m-tag> が読めてしまいます（除去済みのはず）: %q", got)
	}
}

// TestPartMaterialsFromTable は新形式 <table data-type="part-materials"> が
// part_materials へ同期されることを検証します（data-field と見出しラベルの両解決・
// 数値の正規化・quantity 空セルの既定値 1）。
func TestPartMaterialsFromTable(t *testing.T) {
	setupSaveTest(t)

	const id = "000041"
	body := `<dl data-type="tags"><dt>部品番号</dt><dd>SHAFT-01</dd></dl>` +
		`<table data-type="part-materials"><tbody>` +
		// 1〜2列目は data-field、3〜4列目は見出しラベルで解決（レジストリの Label 経由）
		`<tr><th data-field="item-name">部材名</th><th data-field="cost">単価（税抜）</th><th>仕入先</th><th>数量</th></tr>` +
		`<tr><td>丸鋼材</td><td>¥8,000</td><td>大同特殊鋼</td><td>2</td></tr>` +
		`<tr><td>ベアリング</td><td>500</td><td>NSK</td><td></td></tr>` + // quantity 空 → 1
		`</tbody></table>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryPartMaterials(t, 41)
	want := []string{
		"SHAFT-01|ベアリング|500|NSK|1",
		"SHAFT-01|丸鋼材|8000|大同特殊鋼|2",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("part_materials の内容が期待と異なります:\ngot  %v\nwant %v", rows, want)
	}
}

// ── ヘルパ ───────────────────────────────────────────────────────────

func queryPageTags(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(`SELECT name, value FROM page_tags WHERE page_id = ? ORDER BY name, value`, pageID)
	if err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n, v string
		rows.Scan(&n, &v)
		out = append(out, n+"="+v)
	}
	return out
}

func queryPartMaterials(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT part_id, material_name, cost, supplier_name, quantity
		 FROM part_materials WHERE page_id = ? ORDER BY material_name`, pageID)
	if err != nil {
		t.Fatalf("part_materialsのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var partID, name, supplier string
		var cost, qty int
		rows.Scan(&partID, &name, &cost, &supplier, &qty)
		out = append(out, strings.Join([]string{partID, name, itoa(cost), supplier, itoa(qty)}, "|"))
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
