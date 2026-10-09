package comm

import "testing"

// TestBareAddress は、送る口が宛先から素のアドレスだけを取り出すことを確かめます（SMTP の RCPT TO: は
// 素のアドレスしか受けない・2026-10-09 にメールと発注書の写しを寄せた口の番人）。
func TestBareAddress(t *testing.T) {
	for in, want := range map[string]string{
		"みなと商店 <order@example.invalid>":   "order@example.invalid",
		`"山田 太郎" <taro@example.invalid>`:   "taro@example.invalid",
		"  <a@example.invalid>  ":          "a@example.invalid",
		"b@example.invalid":                "b@example.invalid",
		" c@example.invalid ":              "c@example.invalid",
		"閉じていない <d@example.invalid":        "閉じていない <d@example.invalid",
		"x <old@example.invalid> <new@example.invalid>": "new@example.invalid",
		"": "",
	} {
		if got := BareAddress(in); got != want {
			t.Errorf("BareAddress(%q) = %q（%q のはず）", in, got, want)
		}
	}
}
