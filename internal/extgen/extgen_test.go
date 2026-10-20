package extgen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanFindsExtensionsAndTags は、RegisterExtension を呼ぶパッケージだけを拡張とみなし、`//w-cms:build` の式を読むことを固定します。
func TestScanFindsExtensionsAndTags(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ext/a/a.go", "package a\nfunc init() { cms.RegisterExtension(\"a\", \"A\") }\n")
	write("ext/a/b/b.go", "package b\n//w-cms:build !minimal && !nob\nfunc init() {\n\tcms.RegisterExtension(\"a/b\", \"B\")\n}\n")
	write("ext/a/util/u.go", "package util\n// cms.RegisterExtension( はコメントの中だけ\n")
	write("ext/c/c_test.go", "package c\nfunc init() { cms.RegisterExtension(\"c\", \"C\") }\n")
	write("ext/a/assets/x.go", "package x\nfunc init() { cms.RegisterExtension(\"x\", \"X\") }\n")
	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Ext{Dir: "a", Tags: "!minimal"}) || got[1] != (Ext{Dir: "a/b", Tags: "!minimal && !nob"}) {
		t.Fatalf("見つけた拡張が違います: %+v", got)
	}
	if FileName(got[1]) != "ext_a_b_gen.go" {
		t.Errorf("ファイルの名前が違います: %s", FileName(got[1]))
	}
}
