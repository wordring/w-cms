package cms

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 表のセルの式（2026-10-04・formula.go）——利用者:「=個数*単価みたいにセルに書きます」「小数も残す」。

// TestEvalRowFormulas は式の読み方と計算を固定します——全角・桁区切り・円、小数を残す、空なら空、読めないなら理由。
func TestEvalRowFormulas(t *testing.T) {
	headers := []string{"品名", "単価", "個数", "価格", "単価（ロット1）", "メーカー価格", "税込"}
	cases := []struct {
		name  string
		cells []string
		col   int
		value string
		err   string
	}{
		{"基本", []string{"板", "12.5", "3", "=個数*単価"}, 3, "37.5", ""},
		{"全角の式と値", []string{"板", "１，５００円", "２", "＝個数×単価"}, 3, "3000", ""},
		{"括弧と数", []string{"板", "100", "3", "=(単価+10)*個数"}, 3, "330", ""},
		{"浮動小数の端数を見せない", []string{"板", "100", "1", "=単価*1.1"}, 3, "110", ""},
		{"割り算の小数は残す", []string{"板", "10", "4", "=単価/個数"}, 3, "2.5", ""},
		{"符号", []string{"板", "12.5", "", "=-単価"}, 3, "-12.5", ""},
		{"空のセルを参照したら空（エラーにしない）", []string{"板", "100", "", "=個数*単価"}, 3, "", ""},
		{"数でない", []string{"板", "100", "多数", "=個数*単価"}, 3, "", "「個数」が数ではありません"},
		{"無い名前", []string{"板", "100", "2", "=個数*送料"}, 3, "", "「送料」の列も行もありません"},
		{"読めない式", []string{"板", "100", "2", "=個数*"}, 3, "", "式が読めません"},
		{"括弧のある名前は囲む", []string{"板", "", "2", "=「単価（ロット1）」*個数", "300"}, 3, "600", ""},
		{"囲まない括弧の名前は理由を言う", []string{"板", "", "2", "=単価（ロット1）*個数", "300"}, 3, "", "演算の記号が抜けています"},
		{"長音は名前の一部", []string{"板", "", "2", "=メーカー価格*個数", "", "50"}, 3, "100", ""},
		{"式が式を指す", []string{"板", "100", "3", "=単価*個数", "", "", "=価格*1.1"}, 6, "330", ""},
		{"指した式のエラー", []string{"板", "100", "3", "=単価*不明", "", "", "=価格*1.1"}, 6, "", "「価格」の式がエラーです"},
		{"循環", []string{"板", "100", "3", "=価格*2"}, 3, "", "式が循環しています"},
		{"0 で割る", []string{"板", "100", "0", "=単価/個数"}, 3, "", "0 で割っています"},
		{"空の式", []string{"板", "100", "3", "="}, 3, "", "式が空です"},
	}
	for _, c := range cases {
		got := evalRowFormulas(headers, c.cells)
		r := got[c.col]
		if !r.Formula {
			t.Errorf("%s: 式と読まれていません", c.name)
			continue
		}
		if c.err != "" {
			if !strings.Contains(r.Err, c.err) {
				t.Errorf("%s: エラーが %q（%q を含むはず）・値 %q", c.name, r.Err, c.err, r.Value)
			}
			continue
		}
		if r.Err != "" || r.Value != c.value {
			t.Errorf("%s: 値 %q エラー %q（期待 %q）", c.name, r.Value, r.Err, c.value)
		}
	}
	// 式でないセルは触らない。
	if r := evalRowFormulas(headers, []string{"板", "100"}); r[0].Formula || r[1].Formula {
		t.Errorf("式でないセルが式と読まれています: %+v", r)
	}
}

// TestFormulaCellValues は DB へ入れる値——計算できたら値、できなければ書いたまま——を固定します。
func TestFormulaCellValues(t *testing.T) {
	got := formulaCellValues([]string{"単価", "個数", "価格", "税込"}, []string{"12.5", "3", "=個数*単価", "=価格*不明"})
	if strings.Join(got, "|") != "12.5|3|37.5|=価格*不明" {
		t.Errorf("DB へ入れる値が違います: %q", got)
	}
}

// TestRenderFormulaCellsAddsResult は、閲覧の表示に結果の属性が付くこと、⚠ **本文の式は残ること**、
// 見出しの行と鏡（クローム）の表には付けないこと、保存（サニタイズ）で属性が落ちることを固定します。
func TestRenderFormulaCellsAddsResult(t *testing.T) {
	body := `<h1>見積</h1><table><caption>部品</caption><tbody>` +
		`<tr><th>品名</th><th>単価</th><th>個数</th><th>価格</th></tr>` +
		`<tr><td>板</td><td>12.5</td><td>3</td><td>=個数*単価</td></tr>` +
		`<tr><td>棒</td><td>100</td><td>たくさん</td><td>=個数*単価</td></tr>` +
		`</tbody></table>` +
		`<div class="vocab-chrome"><table><tbody><tr><th>単価</th><th>価格</th></tr><tr><td>1</td><td>=単価*2</td></tr></tbody></table></div>`
	got := RenderComputedViews(httptest.NewRequest("GET", "/000010", nil), 10, body)
	for _, want := range []string{`data-w-value="37.5"`, `data-w-error="「個数」が数ではありません"`, `>=個数*単価</td>`} {
		if !strings.Contains(got, want) {
			t.Errorf("%q がありません:\n%s", want, got)
		}
	}
	if strings.Count(got, "data-w-") != 2 {
		t.Errorf("結果の属性は本文の式のセル2つだけ（鏡の表には付けない）: %d\n%s", strings.Count(got, "data-w-"), got)
	}
	if s := Sanitize(got); strings.Contains(s, "data-w-") || !strings.Contains(s, "=個数*単価") {
		t.Errorf("保存すると属性が落ちて式だけが残るはず:\n%s", s)
	}
	// 式の無い本文は手を付けない（パースもしない早道）。
	plain := `<p>a = b</p><table><tbody><tr><th>x</th></tr><tr><td>1</td></tr></tbody></table>`
	if out := renderFormulaCells(plain); out != plain {
		t.Errorf("式の無い本文が変わりました:\n%s", out)
	}
}

// TestFormulaValuesGoToTablesDBAndIndex は、⚠ **DB には計算した値が入る**こと（表の写しと索引の両方）を固定します
// ——画面に見える値と DB で探せる値を揃える。
func TestFormulaValuesGoToTablesDBAndIndex(t *testing.T) {
	db := newTestFileDB(t)
	tdb := newTestTablesDB(t)
	body := `<h1>部品</h1><table><caption>部品表</caption><tbody>` +
		`<tr><th>品名</th><th>単価</th><th>個数</th><th>価格</th></tr>` +
		`<tr><td>板</td><td>12.5</td><td>3</td><td>=個数*単価</td></tr>` +
		`<tr><td>棒</td><td>100</td><td>たくさん</td><td>=個数*単価</td></tr>` +
		`</tbody></table>` +
		`<table data-type="part-materials"><tbody>` +
		`<tr><th>部材名</th><th>単価</th><th>数量</th><th>金額</th></tr>` +
		`<tr><td>ボルト</td><td>20</td><td>4</td><td>＝単価×数量</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000010", body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	got := queryStrings(t, tdb, `SELECT coalesce(価格, '') FROM 部品表 WHERE page_id = 10 ORDER BY row_id`)
	if strings.Join(got, "|") != "37.5|=個数*単価" {
		t.Errorf("表の写しの価格が違います（計算した値・読めなければ書いたまま）: %q", got)
	}
	idx := queryStrings(t, db, `SELECT value FROM vocab_index WHERE page_id = 10 AND field = '金額'`)
	if strings.Join(idx, "|") != "80" {
		t.Errorf("索引の金額が計算した値ではありません: %q", idx)
	}
}

// TestEvalTableFormulasVertical は縦の計算と合計を固定します（2026-10-15）——利用者:「ExcelのオートSUMのように総計を出したい」
// 「SUMは必要ないです。列の見出し以外全部の合計もあったほうがよいと思います」。
func TestEvalTableFormulasVertical(t *testing.T) {
	headers := []string{"工程", "価格", "備考"}
	rows := [][]string{
		{"材料", "620", ""},
		{"ブランク", "1,500円", ""},
		{"ブレーキ", "", "まだ"},
		{"塗装", "=材料*2", ""},
		{"小計", "=合計(上)", ""},
		{"総計", "=合計()", ""},
		{"範囲", "=合計(材料:ブランク)", ""},
		{"逆の範囲", "=合計(ブランク：材料)", ""},
		{"列の名前", "=合計(価格)", ""},
	}
	got := EvalTableFormulas(headers, rows)
	want := map[string]string{
		"塗装":   "1240",
		"小計":   "3360", // 620+1500+(空は飛ばす)+1240
		"総計":   "", // 下で別に見る（ほかの合計を含むので循環しない形か）
		"範囲":   "2120",
		"逆の範囲": "2120",
	}
	for r, row := range rows {
		w, ok := want[row[0]]
		if !ok || row[0] == "総計" {
			continue
		}
		if g := got[r][1]; g.Err != "" || g.Value != w {
			t.Errorf("%s: 値 %q エラー %q（期待 %q）", row[0], g.Value, g.Err, w)
		}
	}
	// 列の全部の合計（自分を除く）——ほかの合計の行も足すので、合計を2つ同じ列に置くと二重に数える（表計算ソフトと同じ）。
	// ここでは「循環しない」ことと「自分を足さない」ことを見る。総計＝620+1500+1240+3360+2120+2120+列の名前、列の名前も総計を含む→循環。
	if g := got[5][1]; g.Err != "式が循環しています" {
		t.Errorf("列の全部の合計を2つ置くと互いを指して循環するはず: %+v", g)
	}

	// 行の合計（先頭の列を除く）と、列どうしの範囲。
	h2 := []string{"品名", "4月", "5月", "6月", "計"}
	r2 := [][]string{
		{"板", "10", "20", "", "=合計(板)"},
		{"棒", "1", "2", "3", "=合計(4月:6月)"},
		{"計", "=合計()", "=合計(上)", "=合計()", ""},
	}
	g2 := EvalTableFormulas(h2, r2)
	for _, c := range []struct {
		r, c int
		v    string
	}{{0, 4, "30"}, {1, 4, "6"}, {2, 1, "11"}, {2, 2, "22"}, {2, 3, "3"}} {
		if g := g2[c.r][c.c]; g.Err != "" || g.Value != c.v {
			t.Errorf("[%d,%d]: 値 %q エラー %q（期待 %q）", c.r, c.c, g.Value, g.Err, c.v)
		}
	}

	// 名前の書き分け・エラーの理由。
	h3 := []string{"工程", "単価", "数"}
	r3 := [][]string{
		{"単価", "100", "2"},
		{"ロット", "", "20"},
		{"a", "=単価*2", ""},
		{"b", "7", "=列「単価」*2"},
		{"c", "", "=行「単価」*ロット"},
		{"d", "=合計(上", ""},
		{"e", "=合計(列「単価」:数)", ""},
		{"f", "=合計(列「単価」:ロット)", ""},
		{"g", "=合計(工程)", ""},
		{"h", "=合計(ロット)", ""},
		{"i", "=合計(上上)", ""},
	}
	g3 := EvalTableFormulas(h3, r3)
	for _, c := range []struct {
		r, c     int
		val, err string
	}{
		{2, 1, "", "は列にも行にもあります"},
		{3, 2, "14", ""},
		{4, 2, "40", ""},
		{5, 1, "", "式が読めません"},
		{6, 1, "", ""}, // 列どうしの範囲（自分の行の 単価:数）——単価の欄は自分なので飛ばし、数は空→空
		{7, 1, "", "行どうしか列どうし"},
		{8, 1, "", "合計: 「単価」が数ではありません"},
		{9, 1, "20", ""}, // 行「ロット」の全部（先頭の列を除く）
		{10, 1, "", "「上上」の列も行もありません"},
	} {
		g := g3[c.r][c.c]
		if c.err != "" {
			if !strings.Contains(g.Err, c.err) {
				t.Errorf("%s: エラー %q（%q を含むはず）値 %q", r3[c.r][0], g.Err, c.err, g.Value)
			}
			continue
		}
		if g.Err != "" || g.Value != c.val {
			t.Errorf("%s: 値 %q エラー %q（期待 %q）", r3[c.r][0], g.Value, g.Err, c.val)
		}
	}
}

// TestFormulaTableValuesForDB は、縦の式も DB へは計算した値で入ること（表の写し・索引の両方の入口）を固定します。
func TestFormulaTableValuesForDB(t *testing.T) {
	got := FormulaTableValues([]string{"工程", "価格"}, [][]string{{"材料", "100"}, {"加工", "250"}, {"計", "=合計(上)"}})
	if got[2][1] != "350" {
		t.Errorf("縦の合計が値になっていません: %q", got)
	}
}

// TestFormulaRound は ROUND（2026-10-19）を固定します——整数に四捨五入（0.5 は 0 から遠い方へ）・大小文字を問わない・
// 空なら空・中のエラーはそのまま・閉じていなければ理由。見積表の単価 =ROUND(原価*(1+利益率/100)) の形。
// 利用者:「式の結果に円未満が出ますが、ROUNDという関数を用意しては？」
func TestFormulaRound(t *testing.T) {
	headers := []string{"原価", "利益率", "単価"}
	for _, c := range []struct {
		name  string
		cells []string
		value string
		err   string
	}{
		{"見積表の単価", []string{"62727", "10", "=ROUND(原価*(1+利益率/100))"}, "69000", ""},
		{"切り捨てる側", []string{"100.4", "", "=ROUND(原価)"}, "100", ""},
		{"0.5 は上へ", []string{"100.5", "", "=round(原価)"}, "101", ""},
		{"負の 0.5 は 0 から遠い方へ", []string{"-100.5", "", "=ROUND(原価)"}, "-101", ""},
		{"式の中で使える", []string{"10.4", "2", "=ROUND(原価)*利益率"}, "20", ""},
		{"空なら空", []string{"", "10", "=ROUND(原価*(1+利益率/100))"}, "", ""},
		{"中のエラーはそのまま", []string{"多数", "10", "=ROUND(原価)"}, "", "「原価」が数ではありません"},
		{"閉じていない", []string{"1", "10", "=ROUND(原価"}, "", "ROUND の中は式1つです"},
	} {
		r := evalRowFormulas(headers, c.cells)[2]
		if c.err != "" {
			if !strings.Contains(r.Err, c.err) {
				t.Errorf("%s: エラーが %q（%q を含むはず）", c.name, r.Err, c.err)
			}
			continue
		}
		if r.Err != "" || r.Value != c.value {
			t.Errorf("%s: 値 %q エラー %q（期待 %q）", c.name, r.Value, r.Err, c.value)
		}
	}
}
