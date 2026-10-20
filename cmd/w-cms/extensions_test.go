package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/extgen"
)

// TestExtensionMountsAreGenerated は、取り付け口（ext_*_gen.go）が ext/ の下の拡張のフォルダと揃っていることを固定します（2026-10-20）。
// 拡張のフォルダを足し・外したのに `go generate ./cmd/w-cms` を流し忘れると、ここで落ちます（足した拡張が黙って入らない、を防ぐ）。
func TestExtensionMountsAreGenerated(t *testing.T) {
	exts, err := extgen.Scan(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) == 0 {
		t.Fatal("拡張が1つも見つかりません（試験の前提が崩れている）")
	}
	want := extgen.Files("w-cms", exts)
	for name, body := range want {
		got, err := os.ReadFile(name)
		// 改行は畳んで比べる（Windows で取り出すと CRLF になる——git の autocrlf）。
		if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != body {
			t.Errorf("⚠ 取り付け口 %s が古いか、ありません——`go generate ./cmd/w-cms` を流してください", name)
		}
	}
	ents, _ := os.ReadDir(".")
	for _, e := range ents {
		if extgen.IsGenerated(e.Name()) {
			if _, ok := want[e.Name()]; !ok {
				t.Errorf("⚠ もう無い拡張の取り付け口 %s が残っています——`go generate ./cmd/w-cms` を流してください", e.Name())
			}
		}
	}
}
