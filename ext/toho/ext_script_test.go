package toho

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms"
)

// TestTohoRegistersItsScript は、東邦の拡張が自分の画面の JS を登録し、そのファイルがあることを固定します（2026-10-20）。
func TestTohoRegistersItsScript(t *testing.T) {
	found := false
	for _, u := range cms.ExtScriptURLs() {
		if u == "/assets/ext/toho/toho.js" {
			found = true
		}
	}
	if !found {
		t.Fatalf("東邦の画面の JS が登録されていません: %v", cms.ExtScriptURLs())
	}
	if _, err := os.Stat(filepath.Join("assets", "toho.js")); err != nil {
		t.Errorf("東邦の画面の JS がありません: %v", err)
	}
}
