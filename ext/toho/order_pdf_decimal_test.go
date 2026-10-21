package toho

import "testing"

// 小数の単価・数量を 0 と刷らない（7年残す紙・2026-10-22 の見直し）。
func TestOrderPDFDecimals(t *testing.T) {
	r := map[string]string{"数量": "100", "単価": "12.5"}
	if a, ok := orderLineAmount(r); !ok || a != 1250 {
		t.Errorf("金額 = %d %v（1250 を期待）", a, ok)
	}
	if got := pdfCellValue(r, orderPDFColumn{Label: "単価", Right: true}); got != "12.5" {
		t.Errorf("単価の刷り方 = %q（12.5 を期待）", got)
	}
	if got := commaDecimal(1234567.25); got != "1,234,567.25" {
		t.Errorf("commaDecimal = %q", got)
	}
	if allPriced([]map[string]string{{"数量": "", "単価": "100"}}) {
		t.Error("⚠ 数量の無い行があるのに、合計を出そうとします")
	}
}
