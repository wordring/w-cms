package page

import "testing"

// Windows が別の名前で開く名前を配らない（.json の検査を抜けて属性と目録が配られていた・2026-10-22 の見直し）。
func TestAliasedFileName(t *testing.T) {
	for _, n := range []string{"000001.meta.json.", "meta.json::$DATA", "a.pdf ", "x:y.pdf"} {
		if !aliasedFileName(n) {
			t.Errorf("⚠ %q を配ろうとします", n)
		}
	}
	for _, n := range []string{"図面.pdf", "a.b.c.dxf"} {
		if aliasedFileName(n) {
			t.Errorf("ふつうの名前 %q を断りました", n)
		}
	}
}
