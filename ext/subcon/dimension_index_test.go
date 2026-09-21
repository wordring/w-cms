package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// 分解した寸法が**別の表**に入ることを固定します（2026-09-21）。
//
// ⚠ **いちばん危ないのは二重登録**です。節と表の両方が引き金になるので、
// 判定を誤ると**同じ行が2回入り**、検索の件数が黙って倍になります
// （同日、コアの `vocab_index` がまさにそれを踏みました）。

// dimRowsSummary は「役割=raw」の並びにして比べやすくします。
func dimRowsSummary(t *testing.T, pageID int) string {
	t.Helper()
	parts, err := dimensionRowsOf(database.DB, pageID)
	if err != nil {
		t.Fatalf("dimensionRowsOfエラー: %v", err)
	}
	var b []string
	for _, p := range parts {
		b = append(b, p.Role+"="+p.Raw)
	}
	return strings.Join(b, " ")
}

// TestDimensionIndexStoresParts は、材料表の寸法が分解されて入ることを固定します。
func TestDimensionIndexStoresParts(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 10, 0, "加工製品", "root", "302", true)
	syncBody(t, 10, `<h1>加工製品</h1>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>角パイプ</td><td>□75*75*t3.2*1090</td><td>2</td></tr>`+
		`</tbody></table>`)

	got := dimRowsSummary(t, 10)
	want := "角=□75 寸法値=75 厚み=t3.2 寸法値=1090"
	if got != want {
		t.Fatalf("分解が入っていません\n  got  %s\n  want %s", got, want)
	}
}

// TestDimensionIndexDoesNotDoubleCount は、⚠ **節と caption が重なっても二重に
// 入らない**ことを固定します。
//
// ⚠ これはコアが同日に踏んだ不具合と**同じ形**です。判定は `cms.VocabTypeOf` で
// コアと揃えてあります——**形式の解き方を2か所に持たない**。
func TestDimensionIndexDoesNotDoubleCount(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 11, 0, "加工製品", "root", "302", true)
	// 節の見出しと caption の**両方**が `材料` を名乗る形。
	syncBody(t, 11, `<h1>加工製品</h1>`+
		`<section><h2>材料</h2>`+
		`<table><caption>材料</caption><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>2</td></tr>`+
		`</tbody></table></section>`)

	got := dimRowsSummary(t, 11)
	want := "厚み=t4.5 寸法値=75 寸法値=1090"
	if got != want {
		t.Fatalf("⚠ 二重に入っているか、入っていません\n  got  %s\n  want %s", got, want)
	}
}

// TestDimensionIndexReadsPlainTableUnderHeading は、**caption の無い素の表**
// （節の見出しだけが名乗る形）も読むことを固定します。
//
// ⚠ ワンノートから移した本文はこの形になりえます——片方しか読まないと、
// **移行したページだけ検索に出ない**という気づきにくい欠け方をします。
func TestDimensionIndexReadsPlainTableUnderHeading(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 12, 0, "加工製品", "root", "302", true)
	syncBody(t, 12, `<h1>加工製品</h1>`+
		`<section><h2>材料</h2>`+
		`<table><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>チャンネル</td><td>C125*65*t6*507</td><td>2</td></tr>`+
		`</tbody></table></section>`)

	if got := dimRowsSummary(t, 12); got != "寸法値=C125 寸法値=65 厚み=t6 寸法値=507" {
		t.Fatalf("素の表が読めていません: %s", got)
	}
}

// TestDimensionIndexIsWashed は、**本文を直すと入れ替わる**ことを固定します
// （導出なので洗い替え）。
func TestDimensionIndexIsWashed(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 13, 0, "加工製品", "root", "302", true)
	body := func(size string) string {
		return `<h1>加工製品</h1>` +
			`<table data-type="` + partMaterialsType + `"><tbody>` +
			`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
			`<tr><td>鉄</td><td>FB</td><td>` + size + `</td><td>2</td></tr>` +
			`</tbody></table>`
	}
	syncBody(t, 13, body("t4.5*75*1090"))
	syncBody(t, 13, body("t6*100"))

	if got := dimRowsSummary(t, 13); got != "厚み=t6 寸法値=100" {
		t.Fatalf("⚠ 古い分解が残っています（洗い替えになっていません）: %s", got)
	}
}

// TestDimensionIndexSkipsTablesWithoutSize は、⚠ **`寸法` の列が無い表には
// 何もしない**ことを固定します（分解する元がありません）。
func TestDimensionIndexSkipsTablesWithoutSize(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 14, 0, "発注書", "root", "302", true)
	syncBody(t, 14, `<h1>発注書</h1>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>品名</th><th>数量</th><th>単価</th></tr>`+
		`<tr><td>カラー</td><td>2</td><td>530</td></tr>`+
		`</tbody></table>`)

	if got := dimRowsSummary(t, 14); got != "" {
		t.Errorf("寸法の列が無いのに何か入れています: %s", got)
	}
}
