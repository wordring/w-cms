//go:build windows

package main

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

// scheme はブラウザから受けるリンクの頭です（w-cms の画面の「📝 ローカル編集」が
// `w-cms-edit:<w-cms のアドレス>#<鍵>` を作る——internal/cms/local_edit.go）。
const scheme = "w-cms-edit:"

// blockedExts は開かない種類です——実行できるもの・ショートカット・スクリプト。⚠ w-cms-edit: は**どのページから
// でも**叩けるので、登録した w-cms からでも、この種類は起こさない（この道具の持ち物の守り）。
var blockedExts = map[string]bool{
	".exe": true, ".com": true, ".bat": true, ".cmd": true, ".ps1": true, ".psm1": true, ".psd1": true,
	".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true, ".wsh": true, ".msi": true, ".msp": true,
	".lnk": true, ".url": true, ".scr": true, ".pif": true, ".hta": true, ".cpl": true, ".reg": true, ".jar": true,
	".appref-ms": true, ".application": true, ".gadget": true, ".inf": true, ".scf": true, ".dll": true,
	".sys": true, ".msc": true, ".settingcontent-ms": true, ".library-ms": true, ".search-ms": true,
}

// normBase は w-cms のアドレスを比べられる形（`https://localhost:8443`・小文字・末尾の / なし）にします。
func normBase(address string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("w-cms のアドレスは https://ホスト:ポート の形で渡してください: " + address)
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

// parseLink はブラウザから受けたリンクを、w-cms のアドレスと鍵に分けます。allowed は登録した w-cms のアドレス。
// **登録していない w-cms には繋がない**（どのページからでも叩けるリンクで、よその場所からファイルを落として開かない）。
func parseLink(link string, allowed []string) (base, token string, err error) {
	if len(link) < len(scheme) || !strings.EqualFold(link[:len(scheme)], scheme) {
		return "", "", errors.New("w-cms-edit: のリンクではありません")
	}
	rest := strings.TrimRight(link[len(scheme):], "/")
	i := strings.LastIndex(rest, "#")
	if i < 0 {
		return "", "", errors.New("リンクに鍵がありません")
	}
	base, err = normBase(rest[:i])
	if err != nil {
		return "", "", err
	}
	token = strings.TrimRight(rest[i+1:], "/")
	if len(token) < 20 || strings.ContainsAny(token, `/\?&#% `) {
		return "", "", errors.New("鍵の形が違います")
	}
	for _, a := range allowed {
		if b, e := normBase(a); e == nil && b == base {
			return base, token, nil
		}
	}
	return "", "", errors.New("登録していない w-cms（" + base + "）には繋ぎません——このアドレスで登録し直してください（-install）")
}

// safeLocalName は、サーバーが言うファイルの名前を手元に置ける名前にします（道筋の区切り・使えない文字を除く）。
// 実行できる種類なら断る。
func safeLocalName(name string) (string, error) {
	// 最後の区切りの後ろだけ（filepath.Base は Windows で `a:` をドライブ名として外すので使わない）。
	n := name
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimRight(strings.TrimSpace(n), ". ")
	if n == "" || n == "." || n == ".." {
		return "", errors.New("ファイルの名前が使えません: " + name)
	}
	if blockedExts[strings.ToLower(filepath.Ext(n))] {
		return "", errors.New("この種類のファイルは開きません（実行できるもの）: " + n)
	}
	return n, nil
}
