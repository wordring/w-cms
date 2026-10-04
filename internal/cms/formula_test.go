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
		{"無い列", []string{"板", "100", "2", "=個数*送料"}, 3, "", "「送料」の列がありません"},
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
		got := EvalRowFormulas(headers, c.cells)
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
	if r := EvalRowFormulas(headers, []string{"板", "100"}); r[0].Formula || r[1].Formula {
		t.Errorf("式でないセルが式と読まれています: %+v", r)
	}
}

// TestFormulaCellValues は DB へ入れる値——計算できたら値、できなければ書いたまま——を固定します。
func TestFormulaCellValues(t *testing.T) {
	got := FormulaCellValues([]string{"単価", "個数", "価格", "税込"}, []string{"12.5", "3", "=個数*単価", "=価格*不明"})
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
