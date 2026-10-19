package toho

import "testing"

// TestSortUnorderedFoldsDue は、必要部材表を納期の暦の順に並べることを固定します（2026-10-20）。納期は text 型なので、
// 文字で比べていたときは `2026/10/05` が `2026-10-20` より後ろに並んでいた。
func TestSortUnorderedFoldsDue(t *testing.T) {
	list := []UnorderedItem{{Due: "2026-10-20"}, {Due: "2026/10/05"}}
	sortUnordered(list)
	if list[0].Due != "2026/10/05" {
		t.Errorf("⚠ 納期の早い 2026/10/05 が先に来ていません: %v", list)
	}
}
