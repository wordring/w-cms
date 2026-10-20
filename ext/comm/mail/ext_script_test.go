package mail

import (
	"testing"

	"w-cms/internal/cms"
)

// TestMailRegistersItsScripts は、この拡張の画面の JS が、置き場の決まり（ext/comm/mail/assets/）で拾われて配られることを固定します（2026-10-20）。
func TestMailRegistersItsScripts(t *testing.T) {
	cms.SetExtAssetRoot("../../..") // 試験はこのパッケージのフォルダで走る
	t.Cleanup(func() { cms.SetExtAssetRoot(".") })
	got := map[string]bool{}
	for _, u := range cms.ExtScriptURLs() {
		got[u] = true
	}
	for _, u := range []string{"/assets/ext/comm/mail/mail-compose.js", "/assets/ext/comm/mail/mail.js"} {
		if !got[u] {
			t.Errorf("画面の JS %s が拾われていません: %v", u, cms.ExtScriptURLs())
		}
	}
}
