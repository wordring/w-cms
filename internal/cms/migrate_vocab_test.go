package cms

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ── 移行第2段: 同期の両対応（新形式＋旧形式の短期保険） ─────────────────

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

// TestLegacyElementsNoLongerSynced は旧形式（<m-tag>・<m-material>）の読み取り
// （短期の保険）が除去済みであることを固定します。旧要素はサニタイズ・表示は
// できても（語彙宣言は第4段まで残る）、索引には乗りません——実データは一括変換
// （MigrateVocab）を通す前提です。
func TestLegacyElementsNoLongerSynced(t *testing.T) {
	setupSaveTest(t)

	const id = "000042"
	body := `<m-tag name="部品番号" value="GEAR-9"></m-tag>` +
		`<m-material item-name="丸鋼材" cost="8000" supplier-name="大同特殊鋼" quantity="2"></m-material>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if tags := queryPageTags(t, 42); len(tags) != 0 {
		t.Errorf("旧 <m-tag> が同期されています（除去済みのはず）: %v", tags)
	}
	if mats := queryPartMaterials(t, 42); len(mats) != 0 {
		t.Errorf("旧 <m-material> が同期されています（除去済みのはず）: %v", mats)
	}
}

// ── 一度きり変換ツール ────────────────────────────────────────────────

// TestConvertVocabHTML は変換の形を検証します——連続する m-tag は1つの dl へ、
// 連続する m-material は1つの表へ、data-id は先頭ブロックから引き継ぐ。
func TestConvertVocabHTML(t *testing.T) {
	in := `<h1>ページ</h1>` +
		`<m-tag name="発注元" value="トーア" data-id="aa11"></m-tag>` +
		`<m-tag name="担当者" value="紀平"></m-tag>` +
		`<p>本文</p>` +
		`<m-material item-name="丸鋼材" cost="8000" supplier-name="大同" quantity="2" data-id="bb22"></m-material>` +
		`<m-material item-name="軸受" cost="500" supplier-name="NSK"></m-material>`

	out, changed := ConvertVocabHTML("000099", in)
	if !changed {
		t.Fatal("変換が起きていません")
	}

	for _, want := range []string{
		`<dl data-type="tags" data-id="aa11"><dt>発注元</dt><dd>トーア</dd><dt>担当者</dt><dd>紀平</dd></dl>`,
		`<table data-type="part-materials" data-id="bb22">`,
		`<th data-field="item-name">部材名</th>`,
		`<td>丸鋼材</td><td>8000</td><td>大同</td><td>2</td>`,
		`<td>軸受</td><td>500</td><td>NSK</td><td></td>`, // quantity 無し → 空セル
	} {
		if !strings.Contains(out, want) {
			t.Errorf("変換結果に %q がありません:\n%s", want, out)
		}
	}
	if strings.Contains(out, "m-tag") || strings.Contains(out, "m-material") {
		t.Errorf("旧要素が残っています:\n%s", out)
	}

	// 冪等性: 変換後をもう一度通しても変化しない
	if _, changed2 := ConvertVocabHTML("000099", out); changed2 {
		t.Error("変換が冪等ではありません（2回目でも変化した）")
	}
}

// TestConvertVocabHTMLKeepsExcluded は変換しない約束のもの（配線タグ・親ページID・
// 名前空・旧要素なしの本文）がそのまま残ることを検証します。
func TestConvertVocabHTMLKeepsExcluded(t *testing.T) {
	in := `<m-tag name="受信元" value="000123"></m-tag>` +
		`<m-tag name="前版" value="000122"></m-tag>` +
		`<m-tag name="親ページID" value="000000"></m-tag>` +
		`<m-tag name="" value="x"></m-tag>`
	out, changed := ConvertVocabHTML("000099", in)
	if changed {
		t.Errorf("除外対象が変換されています:\n%s", out)
	}

	if _, changed := ConvertVocabHTML("000099", `<h1>普通のページ</h1><p>本文</p>`); changed {
		t.Error("旧要素の無い本文で changed=true になっています")
	}
}

// TestMigrateVocabConversion は一度きり変換ツールの実行経路（MigrateVocab）を
// 全ページ走査ごと検証します——旧要素のページは変換されるまで索引に乗らず
// （読み取りの保険は除去済み）、変換後は旧形式と同じ抽出結果が得られる
// （期待値は旧 Sync の仕様: quantity 省略→1 など）。バックアップの作成も見る。
func TestMigrateVocabConversion(t *testing.T) {
	setupSaveTest(t)

	const id = "000043"
	body := `<h1>移行対象</h1>` +
		`<m-tag name="部品番号" value="SHAFT-01"></m-tag>` +
		`<m-tag name="担当者" value="紀平"></m-tag>` +
		`<m-material item-name="丸鋼材" cost="8000" supplier-name="大同特殊鋼" quantity="2"></m-material>` +
		`<m-material item-name="ベアリング" cost="500" supplier-name="NSK"></m-material>`

	// 保存経路でページを作る（正本ファイル＋同期）。旧要素は索引に乗らない。
	postSave(t, id, body)
	if tags := queryPageTags(t, 43); len(tags) != 0 {
		t.Fatalf("変換前の旧要素が索引に乗っています: %v", tags)
	}

	converted, backupDir, err := MigrateVocab()
	if err != nil {
		t.Fatalf("MigrateVocabエラー: %v", err)
	}
	if converted != 1 {
		t.Errorf("変換ページ数が期待と異なります: got %d want 1", converted)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "00", id, id+".html")); err != nil {
		t.Errorf("バックアップに元ファイルがありません: %v", err)
	}

	// 正本から旧要素が消えている
	content, _ := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if strings.Contains(string(content), "m-tag") || strings.Contains(string(content), "m-material") {
		t.Errorf("変換後の正本に旧要素が残っています:\n%s", content)
	}

	// 変換後の抽出結果（旧 Sync が返していた値と同じ）
	wantTags := []string{"担当者=紀平", "部品番号=SHAFT-01"}
	if got := queryPageTags(t, 43); strings.Join(got, "\n") != strings.Join(wantTags, "\n") {
		t.Errorf("page_tags が期待と異なります:\ngot  %v\nwant %v", got, wantTags)
	}
	wantMats := []string{
		"SHAFT-01|ベアリング|500|NSK|1", // quantity 省略 → 旧既定の 1
		"SHAFT-01|丸鋼材|8000|大同特殊鋼|2",
	}
	if got := queryPartMaterials(t, 43); strings.Join(got, "\n") != strings.Join(wantMats, "\n") {
		t.Errorf("part_materials が期待と異なります:\ngot  %v\nwant %v", got, wantMats)
	}

	// 再実行しても変化しない（冪等）
	converted2, _, err := MigrateVocab()
	if err != nil {
		t.Fatalf("2回目のMigrateVocabエラー: %v", err)
	}
	if converted2 != 0 {
		t.Errorf("再実行で %d ページが変換されました（冪等ではない）", converted2)
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
