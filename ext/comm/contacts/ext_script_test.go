package contacts

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms"
)

// TestContactsRegistersItsScripts は、連絡帳の拡張が自分の画面の JS を登録し、そのファイルがあることを固定します（2026-10-20）。
func TestContactsRegistersItsScripts(t *testing.T) {
	got := map[string]bool{}
	for _, u := range cms.ExtScriptURLs() {
		got[u] = true
	}
	for _, u := range []string{"/assets/ext/comm/contacts/contacts.js"} {
		if !got[u] {
			t.Errorf("連絡帳の画面の JS %s が登録されていません: %v", u, cms.ExtScriptURLs())
		}
	}
	for _, f := range []string{"contacts.js"} {
		if _, err := os.Stat(filepath.Join("assets", f)); err != nil {
			t.Errorf("連絡帳の画面の JS がありません: %v", err)
		}
	}
}
