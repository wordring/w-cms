package cms

import "testing"

// TestRefValueNamesLists は、参照の値に番号が並んでいても、2つ目以降の番号に当たることを固定します（2026-10-26。それまでは
// 区切りが1つ前の当たりに食われて、「001234、001235」の 001235 に当たらなかった）。
func TestRefValueNamesLists(t *testing.T) {
	for _, c := range []struct {
		v, id string
		want  bool
	}{
		{"001234、001235", "001235", true},
		{"001234,001235", "001235", true},
		{"001234 001235", "001235", true},
		{"a,001235", "001235", true},
		{"001234-abcd", "001234", true},
		{"/001234", "001234", true},
		{"0012345", "001234", false}, // 7桁の中の6桁は番号ではない
		{"1001234", "001234", false},
		{"001234、001235", "001236", false},
	} {
		if got := refValueNames(c.v, c.id); got != c.want {
			t.Errorf("refValueNames(%q, %q) = %v（%v のはず）", c.v, c.id, got, c.want)
		}
	}
}
