package comm

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms"
)

// TestCommRegistersItsScript は、通信の拡張が自分の画面の JS を登録し、そのファイルがあることを固定します（2026-10-20）。
func TestCommRegistersItsScript(t *testing.T) {
	found := false
	for _, u := range cms.ExtScriptURLs() {
		if u == "/assets/ext/comm/comm.js" {
			found = true
		}
	}
	if !found {
		t.Fatalf("通信の画面の JS が登録されていません: %v", cms.ExtScriptURLs())
	}
	if _, err := os.Stat(filepath.Join("assets", "comm.js")); err != nil {
		t.Errorf("通信の画面の JS がありません: %v", err)
	}
}
