package auth

import "testing"

// 前段のプロキシとして信じるのは、ループバックと WCMS_TRUSTED_PROXIES の範囲だけ（LAN の端末に接続元を偽らせない・2026-10-22 の見直し）。
func TestTrustedProxyOnlyConfigured(t *testing.T) {
	t.Setenv("WCMS_TRUSTED_PROXIES", "")
	if !isTrustedProxy("127.0.0.1") {
		t.Error("ループバックを信じません")
	}
	if isTrustedProxy("192.168.179.4") {
		t.Error("⚠ LAN の端末を前段のプロキシとして信じます")
	}
	t.Setenv("WCMS_TRUSTED_PROXIES", "10.8.0.1, 192.168.10.0/24")
	if !isTrustedProxy("10.8.0.1") || !isTrustedProxy("192.168.10.7") {
		t.Error("書いた範囲を信じません")
	}
	if isTrustedProxy("10.8.0.2") {
		t.Error("⚠ 書いていないアドレスを信じます")
	}
}
