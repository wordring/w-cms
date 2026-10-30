package htmldoc

import (
	"strings"
	"testing"
)

// TestDeepNestingKeepsText は、入れ子が深すぎる本文でも、全部を捨てず文字を残すことを固定します（2026-10-30）。
// それまでは x/net の上限（開いた要素 512）に当たると、出力が空だった——1か所が深いだけで、保存した本文や開いたページが丸ごと空になった。
func TestDeepNestingKeepsText(t *testing.T) {
	s := New()
	for _, c := range []struct {
		name, in string
		want     []string
	}{
		{"閉じた div", "<p>前</p>" + strings.Repeat("<div>", 2000) + "中" + strings.Repeat("</div>", 2000) + "<p>後</p>", []string{"前", "中", "後"}},
		{"ちょうど上限", "<p>前</p>" + strings.Repeat("<div>", 512) + "中" + strings.Repeat("</div>", 512) + "<p>後</p>", []string{"前", "中", "後"}},
		{"閉じていない", "<p>前</p>" + strings.Repeat("<div>", 2000) + "中", []string{"前", "中"}},
		{"入れ子の表", strings.Repeat("<table><tr><td>", 600) + "中" + strings.Repeat("</td></tr></table>", 600) + "<p>後</p>", []string{"中", "後"}},
		{"入れ子の箇条書き", strings.Repeat("<ul><li>", 800) + "中" + strings.Repeat("</li></ul>", 800), []string{"中"}},
	} {
		out, changed := s.SanitizeReport(c.in)
		if !changed {
			t.Errorf("%s: 変えたことを知らせるはず", c.name)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: 文字 %q が消えました（出力 %d バイト）", c.name, w, len(out))
			}
		}
		if _, err := ParseFragment(out); err != nil {
			t.Errorf("%s: 出力がまた読めません: %v", c.name, err)
		}
		// 2度通しても変わらない（保存のたびに崩れない）。
		if again, _ := s.SanitizeReport(out); again != out {
			t.Errorf("%s: 2度目で変わりました", c.name)
		}
	}
	// 上限より浅い本文は、いままでどおり触らない。
	shallow := strings.Repeat("<div>", 300) + "中" + strings.Repeat("</div>", 300)
	if out, changed := s.SanitizeReport(shallow); changed || out != shallow {
		t.Errorf("浅い本文を変えています: changed=%v", changed)
	}
}

// TestFlattenStillCleans は、枠を外した本文もサニタイズを通ることを固定します（読めない入力が守りを抜けない）。
func TestFlattenStillCleans(t *testing.T) {
	s := New()
	in := strings.Repeat("<div>", 600) + `<img src=x onerror="alert(1)"><script>alert(2)</script>文字` + strings.Repeat("</div>", 600)
	out, _ := s.SanitizeReport(in)
	// 深い所の <script> はタグが外れて、中身はただの文字として残る（実行されない）。
	for _, bad := range []string{"onerror", "<script"} {
		if strings.Contains(out, bad) {
			t.Errorf("⚠ 枠を外した本文に %q が残っています", bad)
		}
	}
	if !strings.Contains(out, "文字") {
		t.Errorf("文字が消えました")
	}
}
