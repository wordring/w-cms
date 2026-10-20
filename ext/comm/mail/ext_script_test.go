package mail

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms"
)

// TestMailRegistersItsScripts は、メールの拡張が自分の画面の JS を登録し、そのファイルがあることを固定します（2026-10-20）。
func TestMailRegistersItsScripts(t *testing.T) {
	got := map[string]bool{}
	for _, u := range cms.ExtScriptURLs() {
		got[u] = true
	}
	for _, u := range []string{"/assets/ext/comm/mail/mail-compose.js", "/assets/ext/comm/mail/mail.js"} {
		if !got[u] {
			t.Errorf("メールの画面の JS %s が登録されていません: %v", u, cms.ExtScriptURLs())
		}
	}
	for _, f := range []string{"mail-compose.js", "mail.js"} {
		if _, err := os.Stat(filepath.Join("assets", f)); err != nil {
			t.Errorf("メールの画面の JS がありません: %v", err)
		}
	}
}
