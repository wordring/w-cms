package main

import (
	"strings"
	"testing"
)

// TestEffectiveSettingsLineHidesSecrets は、起動時の「有効な設定」の1行が TLS・WebDAV・Secure Cookie を言い、
// 秘密（Gemini のキー・メールのアプリ登録）は**有無だけ**で、値を出さないことを固定します（2026-10-01・【要求】運用 §3）。
func TestEffectiveSettingsLineHidesSecrets(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "secret-gemini-value")
	t.Setenv("WCMS_MAIL_CLIENT_ID", "secret-client-id")
	t.Setenv("WCMS_DAV", "1")
	t.Setenv("WCMS_SECURE_COOKIES", "0")
	line := effectiveSettingsLine("cert.pem", "key.pem")
	for _, want := range []string{"https://localhost:8443（TLS あり）", "WebDAV あり", "Secure Cookie なし",
		"Gemini のキー あり", "メールのアプリ登録 あり", "設定 config/settings.json"} {
		if !strings.Contains(line, want) {
			t.Errorf("有効な設定の1行に %q がありません: %s", want, line)
		}
	}
	if strings.Contains(line, "secret-") {
		t.Errorf("⚠ 秘密の値がログに出ています: %s", line)
	}

	t.Setenv("GEMINI_API_KEY", "")
	if line := effectiveSettingsLine("", ""); !strings.Contains(line, "http://localhost:8080（TLS なし）") ||
		!strings.Contains(line, "Gemini のキー なし") {
		t.Errorf("TLS なし・キーなしが言えていません: %s", line)
	}
}
