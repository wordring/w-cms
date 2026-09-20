package cms

import (
	"strings"
	"testing"
)

// **ブロックの切り出しは入れ子を数えます**（2026-09-20）。
//
// ⚠ **それまでは「最初に出会う `</section>` まで」で切っていました。** 図面ブロックは
// 中にファイル表示の節を含む（`<section data-type="file-view">`）ので、運び出すと
// **閉じ足りない壊れたHTML**になります。本文へ入れるとサニタイズが後ろの節を
// その中へ飲み込み、**図面ブロックを消したときに改訂履歴や材料まで一緒に消える**形でした。
//
// ⚠ **表に出なかったのは、取り除く側（`extractDrawingSections`）が同じ切り方で
// 「余分な `</section>`」を1つ残していたから**です——**2つの誤りが噛み合って
// 釣り合っていました**。片方だけ直すと崩れるので、直すときは両方です。
//
// **だからここで見るのは「釣り合い」そのもの**です。合流の結果だけを見ていると、
// 打ち消し合っている限り気づけません。

// drawingLikeBlock は実データと同じ形（中にファイル表示の節を持つ図面ブロック）です。
const drawingLikeBlock = `<section data-id="aaaa"><h2>図面</h2>` +
	`<dl data-type="tags"><dt>図面番号</dt><dd>X1</dd></dl>` +
	`<section data-type="file-view" data-ref="000001-bbbb"></section>` +
	`</section>`

func balanced(s string) bool {
	return strings.Count(s, "<section") == strings.Count(s, "</section>")
}

// TestFirstBlockHTMLKeepsNestedSection は、**入れ子を含むブロックを丸ごと**
// 取り出すことを固定します。
func TestFirstBlockHTMLKeepsNestedSection(t *testing.T) {
	body := `<h1>図面</h1>` + drawingLikeBlock +
		`<section data-id="cccc"><h2>改訂履歴</h2><table></table></section>`

	got := FirstBlockHTML(body)
	if got != drawingLikeBlock {
		t.Errorf("ブロックが丸ごと取れていません:\ngot  %s\nwant %s", got, drawingLikeBlock)
	}
	if !balanced(got) {
		t.Errorf("⚠ 閉じタグが釣り合っていません（壊れたブロックを運びます）: %s", got)
	}
	// **次のブロックまで飲み込んでいない**（数えすぎの逆の壊れ方）。
	if strings.Contains(got, "改訂履歴") {
		t.Errorf("後ろのブロックまで飲み込んでいます: %s", got)
	}
}

// TestSectionBlockAtCountsNesting は、切り出しの芯が**深さを数える**ことを固定します。
func TestSectionBlockAtCountsNesting(t *testing.T) {
	body := `<section><section><section></section></section></section><p>あと</p>`

	block, end, ok := SectionBlockAt(body, 0)
	if !ok {
		t.Fatal("切り出せません")
	}
	if !balanced(block) {
		t.Errorf("釣り合っていません: %s", block)
	}
	if body[end:] != `<p>あと</p>` {
		t.Errorf("終わりの位置が違います: %q", body[end:])
	}
}

// TestSectionBlockAtRefusesUnbalanced は、**閉じ足りない本文では切り出さない**ことを
// 固定します。⚠ 黙って途中まで返すと、壊れたHTMLが本文へ入ります。
func TestSectionBlockAtRefusesUnbalanced(t *testing.T) {
	if _, _, ok := SectionBlockAt(`<section><section></section>`, 0); ok {
		t.Error("閉じ足りない本文から切り出しています")
	}
}

// TestIndexSectionTagSkipsCloseTag は、`</section>` や別の要素に当たらないことを
// 固定します。
func TestIndexSectionTagSkipsCloseTag(t *testing.T) {
	if at := IndexSectionTag(`</section><section id="x">`, 0); at != len(`</section>`) {
		t.Errorf("開始タグの位置が違います: %d", at)
	}
	if at := IndexSectionTag(`<sectionfoo>`, 0); at != -1 {
		t.Errorf("別の要素に当たっています: %d", at)
	}
}
