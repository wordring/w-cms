package subcon

import (
	"strconv"
	"strings"
	"testing"
)

// 寸法の分解（2026-09-21）。ユーザー:「あとで**材質形状寸法で検索**したいときが
// あります。**問題は寸法**ですが、検索できるように**分解してDBに入れる**ことは
// 出来ますか？」。
//
// ⚠ **中身は実データから採っています**（移植した6枚とワンノートの `■外注加工` 40枚）。
// 作り話の寸法で固めると、**実物で外れたときに気づけません**。

// dimSummary は分解を「役割=数」の並びにして、比べやすくします。
func dimSummary(parts []DimPart) string {
	var b []string
	for _, p := range parts {
		if p.HasNum() {
			b = append(b, p.Role+"="+trimNum(p.Num))
			continue
		}
		b = append(b, p.Role+"="+p.Raw)
	}
	return strings.Join(b, " ")
}

func trimNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// TestParseDimensionOnRealData は**実データの寸法**が分解できることを固定します。
func TestParseDimensionOnRealData(t *testing.T) {
	cases := []struct{ in, want string }{
		// 移植した加工製品ページの材料表から
		{"□75*75*t3.2*1090", "角=75 寸法値=75 厚み=3.2 寸法値=1090"},
		{"□60*60*t3.2*507", "角=60 寸法値=60 厚み=3.2 寸法値=507"},
		{"C125*65*t6*507", "寸法値=125 寸法値=65 厚み=6 寸法値=507"},
		{"t4.5*75*1090", "厚み=4.5 寸法値=75 寸法値=1090"},
		{"t22*50*200", "厚み=22 寸法値=50 寸法値=200"},
		{"Φ40*t7*301", "径=40 厚み=7 寸法値=301"},
		// ワンノートの外注加工・購入品から
		{"t9*25*475", "厚み=9 寸法値=25 寸法値=475"},
		{"Φ16*55", "径=16 寸法値=55"},
		{"Φ20*43.5", "径=20 寸法値=43.5"},
		{"M16", "ねじ=16"},
		{"φ27.2", "径=27.2"},
		// ⚠ **数でない語も落とさない**（畳めなかった納期を落とさないのと同じ）
		{"t3.4*定尺", "厚み=3.4 印=定尺"},
	}
	for _, c := range cases {
		if got := dimSummary(ParseDimension(c.in)); got != c.want {
			t.Errorf("%q\n  got  %s\n  want %s", c.in, got, c.want)
		}
	}
}

// TestParseDimensionFoldsDiameterMarks は、⚠ **直径記号の揺れを越える**ことを
// 固定します。`Φ`・`⌀`・`Ø` は**系統から違う文字**で、NFKC では畳めません
// ——設定の `char_folding` が `φ` へ揃えます。
func TestParseDimensionFoldsDiameterMarks(t *testing.T) {
	want := dimSummary(ParseDimension("φ40*t7*301"))
	for _, s := range []string{"Φ40*t7*301", "⌀40*t7*301", "Ø40*t7*301"} {
		if got := dimSummary(ParseDimension(s)); got != want {
			t.Errorf("%q の直径記号が畳めていません\n  got  %s\n  want %s", s, got, want)
		}
	}
	// 全角の数字・記号も揃うこと（NFKC）。
	if got := dimSummary(ParseDimension("ｔ４．５＊７５")); got != "厚み=4.5 寸法値=75" {
		t.Errorf("全角が畳めていません: %s", got)
	}
}

// TestParseDimensionSeparators は区切りの種類を固定します。
func TestParseDimensionSeparators(t *testing.T) {
	want := "厚み=3.2 寸法値=75 寸法値=100"
	for _, s := range []string{"t3.2*75*100", "t3.2×75×100", "t3.2x75x100", "t3.2X75X100"} {
		if got := dimSummary(ParseDimension(s)); got != want {
			t.Errorf("%q\n  got  %s\n  want %s", s, got, want)
		}
	}
}

// TestParseDimensionDoesNotGuessLength は、⚠ **役割を推測しない**ことを固定します。
//
// ⚠ **いちばん大きい数を「長さ」と名乗らせてはいけません。** たいていは長さですが、
// **外れたときに黙って間違えます**。役割を名乗るのは、表記が言っているものだけ。
func TestParseDimensionDoesNotGuessLength(t *testing.T) {
	for _, p := range ParseDimension("□75*75*t3.2*1090") {
		if strings.Contains(p.Role, "長さ") || strings.Contains(p.Role, "幅") {
			t.Errorf("⚠ 表記が言っていない役割を名乗っています: %s=%v", p.Role, p.Raw)
		}
	}
	// 知らない接頭辞（チャンネルの `C`）は `寸法値`——**数は検索できるようにする**。
	parts := ParseDimension("C125*65")
	if len(parts) != 2 || parts[0].Role != RoleValue || parts[0].Num != 125 {
		t.Errorf("知らない接頭辞の数が拾えていません: %s", dimSummary(parts))
	}
	// ⚠ **書かれたままも残すこと**（`C125` と `125` は人には別物）。
	if parts[0].Raw != "C125" {
		t.Errorf("書かれたままが残っていません: %q", parts[0].Raw)
	}
}

// TestParseDimensionEmpty は、空と空白を静かに無視することを固定します。
func TestParseDimensionEmpty(t *testing.T) {
	for _, s := range []string{"", "   ", "**", "* *"} {
		if got := ParseDimension(s); len(got) != 0 {
			t.Errorf("%q から %d 片が出ました: %s", s, len(got), dimSummary(got))
		}
	}
}
