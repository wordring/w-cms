package cms

import (
	"strings"
	"testing"
)

// TestWriteTagTrimsValue は、タグの値が前後の空白を落として書かれることを固定します。
//
// **落とさないと逆引きが静かに外れます。** 索引の逆引き（PagesByTag）は
// 生テキストの完全一致で引くので、` <id@example.jp> ` のように空白付きで
// 書かれた値は ` `無しの鍵では見つかりません——取り込みの重複検知
// （メッセージID）がまさにこの経路です。
//
// かつて取り込み係とメール拡張が同名の関数を別々に持ち、**trim の有無だけが
// 違って**いました（2026-09-05 に1つへ寄せた）。どちらの経路で作られたページかで
// 値が変わる、という形の食い違いだったので、ここで固定します。
func TestWriteTagTrimsValue(t *testing.T) {
	var b strings.Builder
	WriteTag(&b, "メッセージID", "  <abc@example.jp>\t")
	got := b.String()
	want := "<dt>メッセージID</dt><dd>&lt;abc@example.jp&gt;</dd>"
	if got != want {
		t.Errorf("値が畳まれていません:\n got  %s\n want %s", got, want)
	}

	// 空白だけの値は書かない（空欄のタグを本文へ残さない）。
	var b2 strings.Builder
	WriteTag(&b2, "宛先", "   ")
	if b2.String() != "" {
		t.Errorf("空白だけの値が書かれました: %q", b2.String())
	}

	// 名前と値の両方をエスケープする（本文の書き手が入れた文字がそのまま出ない）。
	var b3 strings.Builder
	WriteTag(&b3, "差出人", `<script>x</script>`)
	if strings.Contains(b3.String(), "<script>") {
		t.Errorf("値がエスケープされていません: %s", b3.String())
	}
}
