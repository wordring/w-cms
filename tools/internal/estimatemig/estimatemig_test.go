package estimatemig

import (
	"strings"
	"testing"
)

// 見積計算表を 2026-10-15 の形へ移す変換を固定します（職場の235ページを移した道具と同じ変換）。
func TestConvertPage(t *testing.T) {
	old := func(rows string) string {
		return `<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>` + rows + `</tbody></table>`
	}
	tr := func(c ...string) string { return "<tr><td>" + strings.Join(c, "</td><td>") + "</td></tr>" }
	body := `<h1>品物</h1><section><h2>見積計算表</h2><p>※計算用の項目です</p>` +
		old(tr("ロット", "20", "個", "")+tr("材料", "300", "円", "")+tr("板金", "65", "円", "")+tr("単価", "", "円", "塗装無し、試作価格")+tr("総計", "", "円", "")) +
		`<p>あと</p></section>` +
		`<section><h2>見積計算表</h2><table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>ロット6</th><th>ロット10黒塗装</th><th>備考</th></tr>` +
		tr("材料", "100", "90", "") + tr("単価", "", "120", "") + `</tbody></table></section>` +
		`<section><h2>工程</h2><p>x</p></section>` +
		`<section><h2>見積計算表</h2><table><caption>見積計算表</caption><tbody><tr><th>品名</th><th>ロット5</th></tr>` + tr("ボルト", "3") + `</tbody></table></section>`
	r, err := ConvertPage(body)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed || r.Tables != 3 || r.Blocks != 3 || len(r.Skipped) != 1 {
		t.Fatalf("結果が違います: changed=%v tables=%d blocks=%d skipped=%v", r.Changed, r.Tables, r.Blocks, r.Skipped)
	}
	got := r.Body
	for _, want := range []string{
		"<h2>見積もり計算</h2><p>※計算用の項目です</p><h3>ロット20・塗装無し</h3>",
		"<th>工程</th><th>価格</th><th>備考</th>",
		// 単価が空・総計も空 → 原価は価格の合計（365）、利益率 10 → 402。備考は「、」の後ろと「移行中」。
		"<td>20</td><td>塗装無し</td><td>365</td><td>10</td><td>402</td><td>試作価格　移行中</td>",
		"<p>あと</p>",
		// ロットを列に並べた表は列ごとに1組（見出しの残りは表面処理）。すぐ前が移した節なので見出しは重ねない。
		"<p>あと</p><h3>ロット6</h3>",
		"<h3>ロット10・黒塗装</h3>",
		"<td>10</td><td>黒塗装</td><td>120</td><td>10</td><td>132</td>",
		// 移せない形の表の節はそのまま。
		"<section><h2>見積計算表</h2><table><caption>見積計算表</caption><tbody><tr><th>品名</th>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q がありません:\n%s", want, got)
		}
	}
	if strings.Count(got, "<h2>見積もり計算</h2>") != 1 || strings.Contains(got, "<td>総計</td>") || strings.Contains(got, "<td>ロット</td>") {
		t.Errorf("見出しが重なっているか、総計・ロットの行が見積計算表に残っています:\n%s", got)
	}
	// 2回目は何も変えない（何度流しても二重にならない）。
	if r2, _ := ConvertPage(got); r2.Changed {
		t.Errorf("2回目に変わりました")
	}
}
