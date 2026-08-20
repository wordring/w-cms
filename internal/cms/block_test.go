package cms

import (
	"errors"
	"strings"
	"testing"
)

// TestReplaceBlockUpdatesOnlyTarget は、指定したブロックだけが差し替わり、
// 前後のブロックがそのまま残ることを検証します。
func TestReplaceBlockUpdatesOnlyTarget(t *testing.T) {
	page := `<h1 data-id="aaa">見出し</h1>` +
		`<p data-id="bbb">前の段落</p>` +
		`<p data-id="ccc">後の段落</p>`

	got, err := ReplaceBlock(page, "bbb", `<p data-id="bbb">書き換えた段落</p>`)
	if err != nil {
		t.Fatalf("差し替えに失敗: %v", err)
	}

	if !strings.Contains(got, "書き換えた段落") {
		t.Errorf("差し替えが反映されていません: %s", got)
	}
	if strings.Contains(got, "前の段落") {
		t.Errorf("古い内容が残っています: %s", got)
	}
	for _, keep := range []string{"見出し", "後の段落"} {
		if !strings.Contains(got, keep) {
			t.Errorf("他のブロック %q が失われました: %s", keep, got)
		}
	}
}

// TestReplaceBlockKeepsNestedChildren は、明細を持つブロックを差し替えても
// 入れ子の構造がそのまま保たれることを検証します。
func TestReplaceBlockKeepsNestedChildren(t *testing.T) {
	page := `<p data-id="aaa">前書き</p>` +
		`<m-file data-id="bbb" src="a.pdf" name="旧.pdf"></m-file>`

	newBlock := `<m-file data-id="bbb" src="b.pdf" name="新.pdf">` +
		`<m-client-order order-no="PO-1"><m-item item-name="部品" quantity="2"></m-item></m-client-order>` +
		`</m-file>`

	got, err := ReplaceBlock(page, "bbb", newBlock)
	if err != nil {
		t.Fatalf("差し替えに失敗: %v", err)
	}
	for _, want := range []string{`src="b.pdf"`, `<m-client-order order-no="PO-1">`, `item-name="部品"`, "前書き"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が結果に含まれません: %s", want, got)
		}
	}
	if strings.Contains(got, "a.pdf") {
		t.Errorf("古いブロックが残っています: %s", got)
	}
}

// TestReplaceBlockDoesNotAccumulateBlankLines は、同じブロックを何度差し替えても
// 空行が増えていかないことを検証します。
//
// エディタが送るブロックHTMLは末尾に改行を持ち、本文側にもブロック間の改行が
// テキストノードとして存在するため、素朴に差し込むと保存のたびに空行が1行ずつ増え、
// 正本HTMLが少しずつ汚れていきます（実際に発生した不具合）。
func TestReplaceBlockDoesNotAccumulateBlankLines(t *testing.T) {
	page := "<h1 data-id=\"aaa\">見出し</h1>\n<p data-id=\"bbb\">本文</p>\n<hr data-id=\"ccc\">"

	// エディタの出力を模して末尾に改行を付けて送る
	const sent = "<p data-id=\"bbb\">本文</p>\n"

	first, err := ReplaceBlock(page, "bbb", sent)
	if err != nil {
		t.Fatalf("差し替えに失敗: %v", err)
	}
	// 2回目以降は結果が変わらない（＝差し替えが冪等で、空行が増えない）
	got := first
	for i := 2; i <= 5; i++ {
		got, err = ReplaceBlock(got, "bbb", sent)
		if err != nil {
			t.Fatalf("%d回目の差し替えで失敗: %v", i, err)
		}
		if got != first {
			t.Fatalf("%d回目で本文が変化しました（空行の蓄積など）:\n1回目: %q\n%d回目: %q", i, first, i, got)
		}
	}

	if strings.Contains(got, "\n\n") {
		t.Errorf("空行が入っています: %q", got)
	}
	// 他のブロックは無傷であること
	for _, keep := range []string{"見出し", `data-id="ccc"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q が失われました: %q", keep, got)
		}
	}
}

// TestReplaceBlockNotFound は、IDが無い本文（手で書いたHTML等）や未知のIDでは
// ErrBlockNotFound を返すことを検証します。呼び出し側はこれを見て全文保存へ切り替えます。
func TestReplaceBlockNotFound(t *testing.T) {
	cases := []struct {
		name string
		page string
		id   string
	}{
		{"IDを持たない本文", `<h1>見出し</h1><p>本文</p>`, "aaa"},
		{"未知のID", `<p data-id="aaa">本文</p>`, "zzz"},
		{"IDが空", `<p data-id="aaa">本文</p>`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ReplaceBlock(c.page, c.id, `<p>x</p>`); !errors.Is(err, ErrBlockNotFound) {
				t.Errorf("ErrBlockNotFound を期待しましたが %v", err)
			}
		})
	}
}

// TestReplaceBlockAmbiguous は、同じIDが複数あるとき差し替えを拒否することを検証します。
// ページ間でブロックをコピーするとIDが重複しうるため、安全側に倒して全文保存へ落とします。
func TestReplaceBlockAmbiguous(t *testing.T) {
	page := `<p data-id="dup">1つめ</p><p data-id="dup">2つめ</p>`
	if _, err := ReplaceBlock(page, "dup", `<p data-id="dup">x</p>`); !errors.Is(err, ErrBlockAmbiguous) {
		t.Errorf("ErrBlockAmbiguous を期待しましたが %v", err)
	}
}

// TestSanitizeKeepsBlockID は、ブロックIDがサニタイズで落ちないことを検証します。
// 落ちるとブロックの同一性が静かに失われ、以後ずっと全文保存に戻ってしまいます。
func TestSanitizeKeepsBlockID(t *testing.T) {
	got := Sanitize(`<p data-id="abc123">本文</p><dl data-id="def456" data-type="tags"><dt>n</dt><dd>v</dd></dl>`)
	for _, want := range []string{`data-id="abc123"`, `data-id="def456"`} {
		if !strings.Contains(got, want) {
			t.Errorf("ブロックIDが除去されました（%q）: %s", want, got)
		}
	}
	// 危険な属性は従来どおり除去されること
	if bad := Sanitize(`<p data-id="x" onclick="alert(1)">本文</p>`); strings.Contains(bad, "onclick") {
		t.Errorf("on* 属性が残っています: %s", bad)
	}
}

// TestSanitizeDropsIDAttribute は、本文の `id` 属性が除去されることを検証します。
//
// 本文はシェル（assets/index.html）と同じDOMへ合成されるため、`id` を許すと
// 本文から `html-preview` などシェル側の要素を乗っ取れてしまう
// （getElementById は文書順で最初の要素を返し、これらは #editor-content より後ろにある）。
// 保存や権限UIが壊れる経路になるので、ブロック識別子には data-id を使い `id` は通さない。
func TestSanitizeDropsIDAttribute(t *testing.T) {
	got := Sanitize(`<p id="html-preview" data-id="ab12">乗っ取りを狙う本文</p>`)
	if strings.Contains(got, "html-preview") || strings.Contains(got, "id=\"") && !strings.Contains(got, "data-id=\"ab12\"") {
		t.Errorf("id 属性が残っています: %s", got)
	}
	if !strings.Contains(got, `data-id="ab12"`) {
		t.Errorf("ブロック識別子まで落ちています: %s", got)
	}
	if strings.Contains(got, `id="html-preview"`) {
		t.Errorf("シェルの要素を乗っ取れる id が残っています: %s", got)
	}
}

// TestSanitizeDropsLegacyBlockIDAttr は、旧属性名 data-block-id が除去されることを確認します。
// 除去されることで、既存ページは次回保存時に新しい data-id で採番し直されます。
func TestSanitizeDropsLegacyBlockIDAttr(t *testing.T) {
	got := Sanitize(`<p data-block-id="old12">本文</p>`)
	if strings.Contains(got, "data-block-id") {
		t.Errorf("旧属性名が残っています: %s", got)
	}
	if !strings.Contains(got, "本文") {
		t.Errorf("本文まで失われました: %s", got)
	}
}
