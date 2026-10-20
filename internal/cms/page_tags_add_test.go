package cms

import (
	"strings"
	"testing"
)

// AddPageTags・PageTagListEnd（append_page.go・2026-10-20）——ページのタグは、ページの並び（最初の節より前）の末尾へ足す。
// 節の中の並び（図面ブロックのタグなど）しか無ければ、h1 の直後に新しい並びを作る（ブロックのタグへ混ぜない）。
func TestAddPageTags(t *testing.T) {
	pair := `<dt>対応</dt><dd>済</dd>`

	withList := `<h1>題</h1><dl data-type="tags" data-id="ab12"><dt>向き</dt><dd>受信</dd></dl><p>本文</p>`
	if got := AddPageTags(withList, pair); !strings.Contains(got, `<dd>受信</dd>`+pair+`</dl>`) {
		t.Errorf("ページの並びの末尾に足していません:\n%s", got)
	}

	onlyInBlock := `<h1>題</h1><section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>A-1</dd></dl></section>`
	got := AddPageTags(onlyInBlock, pair)
	if strings.Contains(got, `<dd>A-1</dd>`+pair) {
		t.Errorf("⚠ 図面ブロックの並びに混ぜています:\n%s", got)
	}
	if !strings.HasPrefix(got, `<h1>題</h1><dl data-type="tags">`+pair+`</dl><section>`) {
		t.Errorf("h1 の直後に新しい並びを作っていません:\n%s", got)
	}
	if PageTagListEnd(onlyInBlock) != -1 {
		t.Errorf("節の中の並びをページの並びと見ています")
	}
	if AddPageTags(withList, "") != withList {
		t.Errorf("足すものが無いのに本文を変えています")
	}
}
