package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 改定の合流が、**行き先の構造を壊さない**ことを固定します（2026-09-20）。
//
// ⚠ **運び出す側（`FirstBlockHTML`）と取り除く側（`extractDrawingSections`）は一組**です。
// どちらも「最初に出会う `</section>` まで」で切っていたころは、**閉じ足りないブロック**と
// **余分な閉じタグ**が噛み合って釣り合っていました——**片方だけ直すと崩れます**。
//
// **結果の釣り合いだけを見る試験では足りません**（打ち消し合っている限り通る）ので、
// ここでは「改訂履歴が図面ブロックの**中に入っていない**」ところまで見ます。
// ⚠ 入れ子になると、図面ブロックを人が消したとき**改訂履歴や材料まで一緒に消えます**。

func TestRevisionMergeKeepsSiblingBlocks(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	dst := makeDrawingPageFrom(t, inbox, "pdf-a", "X1", "ブラケット", "装置A", "客A")
	src := makeDrawingPageFrom(t, inbox, "pdf-b", "X2", "ブラケット", "装置A", "客A")

	// **運ぶブロックが釣り合っていること**（ここが崩れると行き先で飲み込みが起きる）。
	srcBody, err := cms.ReadPageBody(src)
	if err != nil {
		t.Fatalf("src本文: %v", err)
	}
	block := cms.FirstBlockHTML(srcBody)
	if strings.Count(block, "<section") != strings.Count(block, "</section>") {
		t.Errorf("⚠ 運ぶブロックが閉じ足りません: %s", block)
	}
	if !strings.Contains(block, "file-view") {
		t.Errorf("入れ子のファイル表示が落ちています: %s", block)
	}

	if err := mergeAsRevision(user, src, dst); err != nil {
		t.Fatalf("mergeAsRevision: %v", err)
	}

	after, err := cms.ReadPageBody(dst)
	if err != nil {
		t.Fatalf("dst本文: %v", err)
	}
	if strings.Count(after, "<section") != strings.Count(after, "</section>") {
		t.Errorf("⚠ 合流後の本文が釣り合っていません:\n%s", after)
	}

	// ⚠ **改訂履歴が図面ブロックの中に入っていないこと。** 最初のブロックを丸ごと
	// 取り出して、その中に改訂履歴が居ないことで見ます——釣り合いだけでは、
	// 「飲み込まれたうえで釣り合っている」形を見逃します。
	first := cms.FirstBlockHTML(after)
	if strings.Contains(first, "改訂履歴") {
		t.Errorf("⚠ 改訂履歴が図面ブロックに飲み込まれています（図面を消すと一緒に消えます）:\n%s", first)
	}
	if !strings.Contains(after, "改訂履歴") {
		t.Errorf("改訂履歴が消えています:\n%s", after)
	}
}

// TestExtractDrawingSectionsLeavesBalancedRest は、**取り除いた残りも釣り合う**ことを
// 固定します。
//
// ⚠ **合流の結果だけでは、ここは捕まりません。** 取り除く側だけが旧い切り方だと
// 「余分な `</section>`」が残りますが、パーサはそれを黙って捨てるので**結果は正しく
// 見えます**。変異試験で実際にそうなりました——運び出す側を戻すと落ちるのに、
// 取り除く側を戻しても通ってしまう。**一組の片方だけが守られている**状態でした。
func TestExtractDrawingSectionsLeavesBalancedRest(t *testing.T) {
	body := `<h1>ブラケット</h1>` +
		`<section data-id="aaaa"><h2>図面</h2>` +
		`<dl data-type="tags"><dt>図面番号</dt><dd>X1</dd></dl>` +
		`<section data-type="file-view" data-ref="000001-bbbb"></section>` +
		`</section>` +
		`<section data-id="cccc"><h2>改訂履歴</h2><table></table></section>`

	blocks, rest := extractDrawingSections(body)
	if len(blocks) != 1 {
		t.Fatalf("図面ブロックの数が違います: %d", len(blocks))
	}
	if strings.Count(blocks[0], "<section") != strings.Count(blocks[0], "</section>") {
		t.Errorf("⚠ 取り出したブロックが釣り合っていません: %s", blocks[0])
	}
	if !strings.Contains(blocks[0], "file-view") {
		t.Errorf("入れ子のファイル表示が落ちています: %s", blocks[0])
	}
	if strings.Count(rest, "<section") != strings.Count(rest, "</section>") {
		t.Errorf("⚠ 残りに閉じタグが余っています（運ぶ側の誤りと打ち消し合います）: %s", rest)
	}
	if !strings.Contains(rest, "改訂履歴") {
		t.Errorf("残りから改訂履歴が消えています: %s", rest)
	}
}
