package contacts

import (
	"strings"
	"testing"
)

// 「名前 <addr>」の形と全角の＠も、同じアドレスとして揃える（2026-10-22 の見直し）。
func TestNormalizeEmailForms(t *testing.T) {
	for _, in := range []string{"y@x.jp", "Y@X.JP", "山田 <y@x.jp>", "ｙ＠ｘ.ｊｐ", " <y@x.jp> "} {
		if got := normalizeEmail(in); got != "y@x.jp" {
			t.Errorf("normalizeEmail(%q) = %q", in, got)
		}
	}
	if normalizeEmail("名前だけ") != "" {
		t.Error("アドレスの無い値を通しました")
	}
}

// 字下げのある本文（エディタで保存したページ）からも、アドレスのタグを外せる（2026-10-22 の見直し）。
func TestRemoveEmailTagIndented(t *testing.T) {
	body := "<h1>山田</h1>\n<dl data-type=\"tags\">\n  <dt>" + EmailTag + "</dt>\n  <dd>Y@X.jp</dd>\n  <dt>電話番号</dt>\n  <dd>03</dd>\n</dl>"
	n := 0
	out := removeEmailTag(body, "y@x.jp", &n)
	if n != 1 || strings.Contains(out, "Y@X.jp") || !strings.Contains(out, "電話番号") {
		t.Errorf("⚠ 外し方が違います（%d）: %s", n, out)
	}
}
