package main

// 待受アドレス（WCMS_ADDR）とデータの場所（WCMS_DATA_DIR）——2026-10-14・【要求】運用 §3・§4。

import (
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/database"
)

// TestListenAddr は、⚠ **渡さなければいままでと同じ**（平文 :8080・TLS :8443）で、
// WCMS_ADDR を渡せば TLS のときもその値で待ち受けることを固定します。
func TestListenAddr(t *testing.T) {
	cases := []struct {
		env  string
		tls  bool
		want string
	}{
		{"", false, ":8080"},
		{"", true, ":8443"},
		{"   ", true, ":8443"},
		{"127.0.0.1:9080", false, "127.0.0.1:9080"},
		{"127.0.0.1:9443", true, "127.0.0.1:9443"},
		{":9000", true, ":9000"},
	}
	for _, c := range cases {
		t.Setenv("WCMS_ADDR", c.env)
		if got := listenAddr(c.tls); got != c.want {
			t.Errorf("WCMS_ADDR=%q tls=%v → %q（%q のはず）", c.env, c.tls, got, c.want)
		}
	}
}

// TestListenURL は、ログに出す「開くURL」が既定でいままでと同じ文字になることを固定します。
func TestListenURL(t *testing.T) {
	cases := []struct {
		addr string
		tls  bool
		want string
	}{
		{":8080", false, "http://localhost:8080"},
		{":8443", true, "https://localhost:8443"},
		{"127.0.0.1:9443", true, "https://127.0.0.1:9443"},
		{"0.0.0.0:9080", false, "http://localhost:9080"},
		{"[::]:9080", false, "http://localhost:9080"},
		{"[::1]:9080", false, "http://[::1]:9080"},
		{"読めない", false, "http://読めない"},
	}
	for _, c := range cases {
		if got := listenURL(c.addr, c.tls); got != c.want {
			t.Errorf("listenURL(%q, %v) = %q（%q のはず）", c.addr, c.tls, got, c.want)
		}
	}
}

// TestEffectiveSettingsLineSaysAddrAndDataDir は、起動時の1行が待ち受けとデータの場所を言うことを固定します
// （1台に2つ同居させたとき、どちらがどこで動いているかを1行で見分ける）。
func TestEffectiveSettingsLineSaysAddrAndDataDir(t *testing.T) {
	t.Cleanup(func() { database.SetDataDir("") })
	t.Setenv("WCMS_ADDR", "")
	if line := effectiveSettingsLine("", ""); !strings.Contains(line, "データ data/・") {
		t.Errorf("既定のデータの場所が言えていません: %s", line)
	}

	t.Setenv("WCMS_ADDR", "127.0.0.1:9443")
	database.SetDataDir(filepath.Join("srv", "site", "data"))
	line := effectiveSettingsLine("cert.pem", "key.pem")
	for _, want := range []string{"待ち受け https://127.0.0.1:9443（TLS あり）", "データ srv/site/data/・"} {
		if !strings.Contains(line, want) {
			t.Errorf("有効な設定の1行に %q がありません: %s", want, line)
		}
	}
}

// TestGencertGoesToDataDir は、`-gencert` の出力先がデータの場所の下の tls であることを固定します。
func TestGencertGoesToDataDir(t *testing.T) {
	t.Cleanup(func() { database.SetDataDir("") })
	if got := defaultCertDir(); got != filepath.Join("data", "tls") {
		t.Errorf("既定の証明書の置き場が %q です（data/tls のはず）", got)
	}
	other := filepath.Join(t.TempDir(), "site", "data")
	database.SetDataDir(other)
	cert, key, err := generateSelfSignedCert(defaultCertDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(cert) != filepath.Join(other, "tls") || filepath.Dir(key) != filepath.Join(other, "tls") {
		t.Errorf("証明書がデータの場所の下にありません: %s %s", cert, key)
	}
}
