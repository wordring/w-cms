package toho

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// 1台あたりの個数が小数の行に印と警告が出る。整数・廃版の行には出ない（2026-10-22・利用者:「一台あたりの数が少数の場合、警告してください」）。
func TestMarkFractionalPerUnit(t *testing.T) {
	body := `<table><caption>材料表</caption><tbody>` +
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th><th>区分</th></tr>` +
		`<tr><td>SS400</td><td>板</td><td>t3</td><td>2</td><td></td></tr>` +
		`<tr><td>SUS304</td><td>板</td><td>t2</td><td>0.5</td><td></td></tr>` +
		`<tr><td>A5052</td><td>板</td><td>t1</td><td>1.5</td><td>廃版</td></tr>` +
		`</tbody></table>`
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	tbl := nodes[0]
	markFractionalPerUnit(partMaterialsType, tbl)
	var buf bytes.Buffer
	html.Render(&buf, tbl)
	got := buf.String()
	if strings.Count(got, "cell-fraction") != 1 {
		t.Errorf("⚠ 印の数が違います（0.5 の1つだけ）: %s", got)
	}
	if !strings.Contains(got, "2行目 0.5") || strings.Contains(got, "3行目") {
		t.Errorf("⚠ 警告の文が違います: %s", got)
	}
	if !strings.Contains(got, `<tfoot class="vocab-chrome">`) {
		t.Errorf("⚠ 警告が保存されない足元に入っていません: %s", got)
	}
}

// 小数が無ければ、何も足さない。
func TestMarkFractionalPerUnitQuietOnIntegers(t *testing.T) {
	nodes, err := htmldoc.ParseFragment(`<table><caption>材料表</caption><tbody>` +
		`<tr><th>材質</th><th>個数</th></tr><tr><td>SS400</td><td>3</td></tr></tbody></table>`)
	if err != nil {
		t.Fatal(err)
	}
	markFractionalPerUnit(partMaterialsType, nodes[0])
	var buf bytes.Buffer
	html.Render(&buf, nodes[0])
	if strings.Contains(buf.String(), "tfoot") || strings.Contains(buf.String(), "cell-fraction") {
		t.Errorf("⚠ 整数だけの表に警告が出ました: %s", buf.String())
	}
}
