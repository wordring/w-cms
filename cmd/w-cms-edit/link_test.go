//go:build windows

package main

import "testing"

// TestParseLink は、w-cms-edit: が**登録した w-cms にしか繋がない**ことを固定します——⚠ このリンクはどのページからでも
// 叩けるので、よその場所からファイルを落として開かせない。
func TestParseLink(t *testing.T) {
	allowed := []string{"https://localhost:8443"}
	tok := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde"
	base, token, err := parseLink("w-cms-edit:https://LOCALHOST:8443#"+tok+"/", allowed)
	if err != nil || base != "https://localhost:8443" || token != tok {
		t.Fatalf("parseLink = %q %q %v", base, token, err)
	}
	for _, bad := range []string{
		"w-cms-edit:https://evil.example#" + tok,       // 登録していない w-cms
		"w-cms-edit:https://localhost:9443#" + tok,     // ポート違い
		"w-cms-edit:https://localhost:8443",            // 鍵が無い
		"w-cms-edit:https://localhost:8443#short",      // 鍵が短い
		"w-cms-edit:https://localhost:8443#" + tok + "&x=1",
		"w-cms-edit:file:///C:/Windows#" + tok,          // http(s) でない
		"http://localhost:8443/#" + tok,                 // 別のスキーム
	} {
		if _, _, err := parseLink(bad, allowed); err == nil {
			t.Errorf("断るはずのリンクを通しました: %q", bad)
		}
	}
}

// TestSafeLocalName は、サーバーが言う名前を手元に置ける形にし、実行できる種類を断ることを固定します。
func TestSafeLocalName(t *testing.T) {
	for in, want := range map[string]string{
		"展開　保持ブラケット2.rpcd": "展開　保持ブラケット2.rpcd",
		`..\..\a.dxf`:        "a.dxf",
		"a:b?.xlsx":          "a_b_.xlsx",
	} {
		if got, err := safeLocalName(in); err != nil || got != want {
			t.Errorf("safeLocalName(%q) = %q, %v（期待 %q）", in, got, err, want)
		}
	}
	for _, bad := range []string{"setup.exe", "a.LNK", "x.ps1", "..", ""} {
		if got, err := safeLocalName(bad); err == nil {
			t.Errorf("断るはずの名前を通しました: %q → %q", bad, got)
		}
	}
}
