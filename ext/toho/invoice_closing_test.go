package toho

import (
	"testing"
	"time"
)

// 30日締めは、31日のある月でも30日で締める。その日が無い月（2月）は末日（2026-10-22 の見直し）。
func TestClosingDay30(t *testing.T) {
	d, ok := closingDayOf("30日")
	if !ok || d != 30 {
		t.Fatalf("30日 → %d %v", d, ok)
	}
	if got := closingIn(2026, time.October, d); got.Day() != 30 {
		t.Errorf("10月の30日締め = %v", got)
	}
	if got := closingIn(2026, time.February, d); got.Month() != time.February || got.Day() != 28 {
		t.Errorf("2月の30日締め = %v（2月28日を期待）", got)
	}
	if got := closingIn(2026, 0, d); got.Year() != 2025 || got.Month() != time.December || got.Day() != 30 {
		t.Errorf("前の年の12月 = %v", got)
	}
	if d, _ := closingDayOf("31"); d != 0 {
		t.Errorf("31日締めは末日のはず: %d", d)
	}
}
