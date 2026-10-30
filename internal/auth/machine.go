package auth

// 機械専用の入口の認証（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §4.5）。
//
// 電話の道具（ND6 の着信を読む）や FAX サーバーのように、ブラウザのログインを持たない機械が w-cms を呼ぶための入口です。
// 前置き MachinePathPrefix（`/api/machine/`）の下だけが対象で、次の3つがそろったときだけ通します。
//
//  1. 鍵——`Authorization: Bearer <鍵>` が環境変数 WCMS_MACHINE_TOKEN と一致する（無ければ入口は閉じている）。
//  2. 呼んだ機械のアドレス——既定は同じ機械（127.0.0.1・::1）だけ。ほかの機械から呼ぶなら WCMS_MACHINE_ALLOW に
//     CIDR をカンマ区切りで書く（例 `192.168.1.0/24`）。⚠ VPS のリバースプロキシを通った要求は、プロキシのアドレスに見える
//     ——鍵を持たない外の人は、アドレスが合っても通らない（1 と 2 の両方を要るのはこのため）。
//  3. 書き手——環境変数 WCMS_MACHINE_USER の w-cms の利用者（無効にされていない）。機械が作ったページの持ち主と監査はこの人。
//
// ブラウザが勝手に送ってくる要求には鍵が付かないので、この前置きは CSRF の確かめ（Origin）の外に出します（CSRFProtect）。

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"
)

// MachinePathPrefix は機械専用の入口の前置きです。
const MachinePathPrefix = "/api/machine/"

// machineAllowed は、呼んだ機械のアドレス remote（RemoteAddr の形）が許されているかです。
func machineAllowed(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, c := range strings.Split(os.Getenv("WCMS_MACHINE_ALLOW"), ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(ip) {
			return true
		}
		if one := net.ParseIP(c); one != nil && one.Equal(ip) {
			return true
		}
	}
	return false
}

// viaProxy は、要求が前段のプロキシを通ってきたかです（転送の見出しのどれかがある）。
func viaProxy(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host"} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// machineToken は WCMS_MACHINE_TOKEN の鍵です。短すぎる鍵は決まっていないのと同じに扱う（打ち間違い・試しの値で開けない）。
func machineToken() string {
	want := strings.TrimSpace(os.Getenv("WCMS_MACHINE_TOKEN"))
	if len(want) < 16 {
		return ""
	}
	return want
}

// machineTokenOK は、要求の鍵が WCMS_MACHINE_TOKEN と一致するかです（鍵が決まっていなければ常に false）。
func machineTokenOK(r *http.Request) bool {
	want := machineToken()
	if want == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) == 1
}

// MachineAuth は機械専用の入口を包むミドルウェアです。通ったら、WCMS_MACHINE_USER の利用者を要求に載せて次へ渡します。
// 断るときは、わけを短く返します（鍵の有無だけで、鍵の中身や利用者の名前は言わない）。
func MachineAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if machineToken() == "" {
			http.Error(w, "機械の入口は閉じています（WCMS_MACHINE_TOKEN が無いか、16文字より短い）", http.StatusServiceUnavailable)
			return
		}
		// 前段のプロキシを通った要求は入れない（2026-10-26）。VPS からのトンネルが同じ機械で終わると、外からの要求も 127.0.0.1 に
		// 見え、アドレスの確かめが効かない。機械（電話の道具・FAX の台本）は同じ機械から直に呼び、この見出しを付けない。
		if viaProxy(r) {
			http.Error(w, "前段のプロキシを通った要求は、機械の入口に入れません", http.StatusForbidden)
			return
		}
		if !machineAllowed(r.RemoteAddr) {
			http.Error(w, "この機械からは呼べません（WCMS_MACHINE_ALLOW）", http.StatusForbidden)
			return
		}
		if !machineTokenOK(r) {
			http.Error(w, "鍵が違います", http.StatusUnauthorized)
			return
		}
		name := strings.TrimSpace(os.Getenv("WCMS_MACHINE_USER"))
		if name == "" {
			http.Error(w, "機械の書き手（WCMS_MACHINE_USER）が決まっていません", http.StatusServiceUnavailable)
			return
		}
		user, err := LookupUser(name)
		if err != nil || user == nil {
			http.Error(w, "機械の書き手（WCMS_MACHINE_USER）がいません", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, WithUser(r, user))
	})
}
