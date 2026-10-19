package toho

import (
	"strings"
	"testing"
)

// 人が一度保存したページは字下げされます（作業引き継ぎの罠の節「エディタで保存した本文は字下げされる」）。
// 整理が本文の項目を書き戻す道（replaceFirstFieldValue）と、社名のページを連絡帳に結んであるかの確かめ（hasTagPair）が、
// 字下げのある本文でも当たることを固定します（2026-10-20）。

const indentedTags = "<dl data-type=\"tags\">\n    <dt>客先</dt>\n    <dd>株式会社みなと商店</dd>\n    <dt>装置名称</dt>\n    <dd><br/></dd>\n    <dt>連絡帳</dt>\n    <dd>000321</dd>\n</dl>"

func TestReplaceFirstFieldValueIndented(t *testing.T) {
	got := replaceFirstFieldValue(indentedTags, "客先", "みなと商店")
	if !strings.Contains(got, "<dd>みなと商店</dd>") {
		t.Errorf("⚠ 字下げのある本文で客先を書き戻せていません:\n%s", got)
	}
	got = replaceFirstFieldValue(indentedTags, "装置名称", "回転機")
	if !strings.Contains(got, "<dd>回転機</dd>") {
		t.Errorf("⚠ 字下げのある本文の空欄（<br/>）を書き戻せていません:\n%s", got)
	}
}

func TestHasTagPairIndented(t *testing.T) {
	if !hasTagPair(indentedTags, "連絡帳", "000321") {
		t.Errorf("⚠ 字下げのある本文で、結んである連絡帳のタグが見えません")
	}
	if !hasTagPair(`<dt>連絡帳</dt><dd>000321</dd>`, "連絡帳", "000321") {
		t.Errorf("詰めた形で見えません")
	}
	if hasTagPair(indentedTags, "連絡帳", "000322") || hasTagPair(indentedTags, "連絡帳", "00032") {
		t.Errorf("別の値に当たっています")
	}
}
