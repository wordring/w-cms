package main

import (
	"strings"
	"testing"
)

// TestEffectiveSettingsLineHidesSecrets は、起動時の「有効な設定」の1行が TLS・Secure Cookie を言い、
// 秘密（Gemini のキー・メールのアプリ登録）は**有無だけ**で、値を出さないことを固定します（2026-10-01・【要求】運用 §3）。
func TestEffectiveSettingsLineHidesSecrets(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "secret-gemini-value")
	t.Setenv("WCMS_MAIL_CLIENT_ID", "secret-client-id")
	t.Setenv("WCMS_SECURE_COOKIES", "0")
	t.Setenv("WCMS_ADDR", "") // 既定の待ち受けで見る（listen_test.go が WCMS_ADDR を渡した形を見る）
	line := effectiveSettingsLine("cert.pem", "key.pem")
	for _, want := range []string{"https://localhost:8443（TLS あり）", "Secure Cookie なし",
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

// TestEffectiveSettingsLineHasNoWebDAV は、WebDAV をやめたので（2026-10-15）、有効な設定の1行に WebDAV が出ないことを固定します。
func TestEffectiveSettingsLineHasNoWebDAV(t *testing.T) {
	if line := effectiveSettingsLine("cert.pem", "key.pem"); strings.Contains(line, "WebDAV") {
		t.Errorf("有効な設定の1行に WebDAV が残っています: %s", line)
	}
}

// TestEffectiveSettingsLineSaysNewerSettings は、2026-10-14 より後に増えた設定の有無も1行に出し、値は出さないことを固定します（2026-10-30）。
func TestEffectiveSettingsLineSaysNewerSettings(t *testing.T) {
	t.Setenv("WCMS_MACHINE_TOKEN", "secret-machine-token")
	t.Setenv("WCMS_LOCAL_SOCKET", "/run/secret-sock")
	t.Setenv("WCMS_TRUSTED_PROXIES", "")
	line := effectiveSettingsLine("cert.pem", "key.pem")
	for _, want := range []string{"機械の入口の鍵 あり", "中の入口 あり", "信じるプロキシ なし", "ラベルプリンターの道具"} {
		if !strings.Contains(line, want) {
			t.Errorf("有効な設定の1行に %q がありません: %s", want, line)
		}
	}
	if strings.Contains(line, "secret-") {
		t.Errorf("⚠ 値がログに出ています: %s", line)
	}
}
