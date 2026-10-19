package toho

import "testing"

// TestEstimateMatchesFormulaRounding は、見積表の単価がテンプレートの式 `=ROUND(原価*(1+利益率/100))`（まとめて1回だけ丸める）で
// 書かれていても、記録が計算と「合っている」と読むことを固定します（2026-10-20）。原価 50・利益率 15 だと、
// 計算（原価の円＋利益の円）は 58、式は 57。それまでは 📌 で記録した直後に「⚠ 記録が計算表と合いません」が出ていた。
func TestEstimateMatchesFormulaRounding(t *testing.T) {
	p := estimatePlan{OK: true, Cost: 50, Rate: 15, Profit: 8, Unit: 58}
	if !p.matches(estimateRecord{Cost: "50", Rate: "15", Unit: "57"}) {
		t.Errorf("⚠ 式で丸めた単価（57）を、合っていないと読んでいます")
	}
	if !p.matches(estimateRecord{Cost: "50", Rate: "15", Unit: "58"}) {
		t.Errorf("計算の単価（58）を、合っていないと読んでいます")
	}
	if p.matches(estimateRecord{Cost: "50", Rate: "15", Unit: "60"}) {
		t.Errorf("⚠ 違う単価（60）を合っていると読んでいます")
	}
}
