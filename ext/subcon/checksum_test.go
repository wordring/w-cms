package subcon

import (
	"strings"
	"testing"
)

// **発注書の検算**（2026-09-20 ユーザー:「顧客の発注書に数量と単価があって、金額や
// 総額もあるなら、検算が出来ると思います」）。
//
// ⚠ **これは OCR を外の正解なしに確かめられる、たぶん唯一の手段**です。他の検査は
// すべて「人が紙と見比べる」に帰着しますが、**数どうしの辻褄は機械が見られます**。

// poTable は実物と同じ見出しの原本を組みます
// （`No. / 品名 / サイズ / 図面番号 / 数量 / 単位 / 単価 / 金額`）。
func poTable(rows ...[]string) orderSourceTable {
	return orderSourceTable{
		Headers: []string{"No.", "品名", "サイズ", "図面番号", "数量", "単位", "単価", "金額"},
		Rows:    rows,
	}
}

func TestChecksumAllAgree(t *testing.T) {
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39,000"},
		[]string{"2", "カバー", "t1.6", "K120-2", "2", "セット", "1,500", "3,000"},
	), "42,000", "4,200", "46,200")

	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("合っているのに ⚠ が出ています: %v", w)
	}
	if c.Checked != 2 {
		t.Errorf("検算した行が %d です（2を期待）", c.Checked)
	}
	if s := c.Skipped(); s != "" {
		t.Errorf("飛ばした行がないのに知らせています: %s", s)
	}
}

// TestChecksumCatchesRowMisread は、**行の数の読み違い**を捕まえることを固定します。
func TestChecksumCatchesRowMisread(t *testing.T) {
	// ⚠ 単価を `390` → `890` と読み違えた形（金額は紙のまま）。
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "890", "39000"},
	), "39000", "3900", "42900")

	w := c.Warnings()
	if len(w) == 0 {
		t.Fatal("数量×単価≠金額 を見逃しています")
	}
	if !strings.Contains(w[0], "ブラケット") {
		t.Errorf("どの行か分かりません: %s", w[0])
	}
	// ⚠ **差が分かること**——89000 のはずが 39000。
	if !strings.Contains(w[0], "89000") || !strings.Contains(w[0], "39000") {
		t.Errorf("両方の額が出ていません: %s", w[0])
	}
}

// TestChecksumCatchesDroppedRow は、⚠ **行の落丁**を捕まえることを固定します。
//
// **これがこの仕掛けの本命です。** 落丁は他のどの検査でも捕まりません——残った行は
// 互いに辻褄が合ったままなので、行の検算は全部通ります。
func TestChecksumCatchesDroppedRow(t *testing.T) {
	// 紙には2行あったが、1行しか読めなかった（小計は2行ぶん）。
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
	), "42000", "4200", "46200")

	// ⚠ **行の検算は全部通ります**——だから小計が要ります。
	if len(c.RowIssues) != 0 {
		t.Fatalf("行の検算が落ちています（落丁は行では捕まらないはず）: %+v", c.RowIssues)
	}
	w := c.Warnings()
	if len(w) == 0 {
		t.Fatal("⚠ 落丁を見逃しています（Σ金額 ≠ 小計）")
	}
	if !strings.Contains(w[0], "3000") {
		t.Errorf("差額が出ていません（どの行が抜けたか当たりが付きません）: %s", w[0])
	}
	if !strings.Contains(w[0], "抜けて") {
		t.Errorf("落丁を疑う文言がありません: %s", w[0])
	}
}

// TestChecksumTotalIsTaxRateFree は、**税率を当てにいかない**ことを固定します。
//
// ⚠ 軽減税率（8%）の発注書で ⚠ が出てはいけません。見るのは
// `小計 + 消費税 = 合計` だけで、**税率は使いません**。
func TestChecksumTotalIsTaxRateFree(t *testing.T) {
	for _, c := range []struct{ name, tax, total string }{
		{"10%", "4200", "46200"},
		{"8%（軽減）", "3360", "45360"},
		{"非課税", "0", "42000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkOrderArithmetic(poTable(
				[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "420", "42000"},
			), "42000", c.tax, c.total)
			if w := got.Warnings(); len(w) != 0 {
				t.Errorf("⚠ 税率を当てにいっています: %v", w)
			}
		})
	}
}

// TestChecksumCatchesBadTotal は、**合計そのものの読み違い**を捕まえることを固定します。
func TestChecksumCatchesBadTotal(t *testing.T) {
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "420", "42000"},
	), "42000", "4200", "46000") // 合計を 46200 → 46000 と読み違えた

	w := c.Warnings()
	if len(w) != 1 || !strings.Contains(w[0], "消費税") {
		t.Errorf("合計の食い違いを捕まえていません: %v", w)
	}
}

// TestChecksumIgnoresSubtotalRowInsideTable は、⚠ **小計を二重に数えない**ことを
// 固定します。
//
// ユーザー:「変形した表になっていて、**表の右下の方に、小計、消費税、合計金額が
// 飛び出しています**」（2026-09-20）。Gemini はそれを明細の行として返してきます。
// そのまま足すと**小計が2回入り**、必ず ⚠ が出ます（狼少年になります）。
func TestChecksumIgnoresSubtotalRowInsideTable(t *testing.T) {
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "100", "個", "390", "39000"},
		[]string{"2", "カバー", "t1.6", "K120-2", "2", "セット", "1500", "3000"},
		[]string{"", "", "", "", "", "", "小計", "42000"},   // ⚠ 飛び出した行
		[]string{"", "", "", "", "", "", "消費税", "4200"},   // ⚠
		[]string{"", "", "", "", "", "", "合計金額", "46200"}, // ⚠
	), "42000", "4200", "46200")

	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("⚠ 飛び出した行を明細として足しています: %v", w)
	}
	if c.Checked != 2 {
		t.Errorf("検算した行が %d です（明細2行ぶんの2を期待）", c.Checked)
	}
	// ⚠ **飛ばしたことは黙らない。**
	if s := c.Skipped(); !strings.Contains(s, "3行") {
		t.Errorf("飛ばした行数を知らせていません: %q", s)
	}
}

// TestChecksumStaysSilentWithoutColumns は、**見出しが見つからなければ黙る**ことを
// 固定します。
//
// ⚠ 当て推量で別の列を掛け算すると、**正しい発注書に ⚠ を出します**。狼少年に
// なると本物の ⚠ も読まれなくなるので、黙るほうが安全です。
func TestChecksumStaysSilentWithoutColumns(t *testing.T) {
	c := checkOrderArithmetic(orderSourceTable{
		Headers: []string{"品名", "摘要", "備考"},
		Rows:    [][]string{{"ブラケット", "至急", ""}},
	}, "", "", "")

	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("見出しが無いのに検算しています: %v", w)
	}
	if c.Checked != 0 || c.Unchecked != 1 {
		t.Errorf("行の数え方が違います: 検算=%d 未検算=%d", c.Checked, c.Unchecked)
	}
}

// TestChecksumDoesNotMatchTotalColumnAsAmount は、⚠ **`合計金額` を `金額` と
// 取り違えない**ことを固定します（部分一致を採らない理由）。
func TestChecksumDoesNotMatchTotalColumnAsAmount(t *testing.T) {
	c := checkOrderArithmetic(orderSourceTable{
		Headers: []string{"品名", "数量", "単価", "合計金額"},
		Rows:    [][]string{{"ブラケット", "100", "390", "39000"}},
	}, "", "", "")

	if c.Checked != 0 {
		t.Errorf("⚠ `合計金額` を明細の `金額` として扱っています（小計が二重になります）")
	}
}

// TestParseMoneyReadsRealWorldCells は、実物のセルの書き方を読めることを固定します。
func TestParseMoneyReadsRealWorldCells(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want float64
	}{
		{"39,000", 39000},
		{"￥39,000", 39000},
		{"39000円", 39000},
		{"３９０", 390},  // 全角
		{"１，５００", 1500}, // 全角＋全角カンマ
		{"390.5", 390.5},
		{"△3,000", -3000}, // ⚠ 会計のマイナス（値引きの行）
		{"▲3,000", -3000},
	} {
		got, ok := parseMoney(c.raw)
		if !ok || got != c.want {
			t.Errorf("%q → %v, %v（%v を期待）", c.raw, got, ok, c.want)
		}
	}
	for _, raw := range []string{"", "  ", "一式", "—", "小計"} {
		if _, ok := parseMoney(raw); ok {
			t.Errorf("%q を数として読んでいます", raw)
		}
	}
}

// TestChecksumToleratesRounding は、**単価の小数による端数で ⚠ を出さない**ことを
// 固定します。
//
// ⚠ 幅を持たせないと、正しい発注書で毎回 ⚠ が出ます。
func TestChecksumToleratesRounding(t *testing.T) {
	// 390.5 × 3 = 1171.5 → 紙は 1172（四捨五入）。
	c := checkOrderArithmetic(poTable(
		[]string{"1", "ブラケット", "t3.2", "K120-1", "3", "個", "390.5", "1172"},
	), "", "", "")
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("端数処理で ⚠ が出ています: %v", w)
	}
}
