package toho

import (
	"strings"
	"testing"
)

// 手配状況の表の「一台」は、表の値が小数なら「⚠ 0.5 → 1」と切り上げた数を並べる。整数ならそのまま（2026-10-22）。
func TestProcPerShowsFraction(t *testing.T) {
	if got := procPer(ProcurementItem{Per: 1, PerExact: 0.5}); !strings.Contains(got, "⚠ 0.5 → 1") {
		t.Errorf("⚠ 小数を知らせていません: %s", got)
	}
	if got := procPer(ProcurementItem{Per: 2, PerExact: 2}); got != "2" {
		t.Errorf("整数に印が付きました: %s", got)
	}
}
