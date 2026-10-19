package cms

import "testing"

// TestEndOfFirstTagListFindsListsWithAttributes は、最初の可変タグの並びの終わりを、属性（エディタが付ける data-id）付きの並びでも
// 見つけることを固定します（2026-10-19——文字どおりに探していたので見落とし、タグを足す道具が h1 の下に2つ目の並びを作った）。
func TestEndOfFirstTagListFindsListsWithAttributes(t *testing.T) {
	for _, c := range []struct {
		body string
		want int
	}{
		{`<h1>a</h1><dl data-type="tags"><dt>x</dt><dd>1</dd></dl>`, len(`<h1>a</h1><dl data-type="tags"><dt>x</dt><dd>1</dd>`)},
		{`<h1>a</h1><dl data-type="tags" data-id="7yob"><dt>x</dt><dd>1</dd></dl>`, len(`<h1>a</h1><dl data-type="tags" data-id="7yob"><dt>x</dt><dd>1</dd>`)},
		{`<h1>a</h1><dl data-id="7yob" data-type="tags"><dt>x</dt></dl>`, len(`<h1>a</h1><dl data-id="7yob" data-type="tags"><dt>x</dt>`)},
		{`<h1>a</h1><dl><dt>x</dt></dl>`, -1}, // 素の dl は可変タグではない
		{`<h1>a</h1><dl data-type="tagsx"><dt>x</dt></dl>`, -1},
	} {
		if got := EndOfFirstTagList(c.body); got != c.want {
			t.Errorf("EndOfFirstTagList(%q) = %d, want %d", c.body, got, c.want)
		}
	}
}
