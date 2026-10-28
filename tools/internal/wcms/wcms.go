// Package wcms は、リポジトリの道具（tools/）が共有する小物です——デスクトップの場所と、手元の w-cms へ
// ログインして口を呼ぶ係（2026-10-09 に tools/mail/fetch・tools/mail/push・tools/onenote/build の写しを寄せた）。
//
// ⚠ 道具だけの持ち物です（本体のサーバーは使わない）。
package wcms

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Desktop はデスクトップの場所です（OneDrive へ移されていればそちら）。
func Desktop() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, "OneDrive", "デスクトップ"), filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "Desktop")} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return home
}

// trustedRoots は、ほかの機械へ繋ぐときに信じる証明書です（2026-10-28）——この機械の信頼ストアに、WCMS_CA（CA の
// 証明書のファイル）か、無ければデスクトップの w-cms/サーバー機/ の *.crt（業務サーバーの証明書・リポジトリには入れない）を足す。
// 足すものが無ければ nil（この機械の信頼ストアだけ）。JS の道具の tools/tls-trust.js と同じ形。
func trustedRoots() *x509.CertPool {
	var files []string
	if f := os.Getenv("WCMS_CA"); f != "" {
		files = []string{f}
	} else {
		files, _ = filepath.Glob(filepath.Join(Desktop(), "w-cms", "サーバー機", "*.crt"))
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	added := false
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && pool.AppendCertsFromPEM(b) {
			added = true
		}
	}
	if !added {
		return nil
	}
	return pool
}

// Client は手元の w-cms へログインした係です。
type Client struct {
	base string
	hc   *http.Client
}

// Login は WCMS_BASE（既定 https://localhost:8443）の w-cms へ、WCMS_USER・WCMS_PASS（既定はローカル開発の
// 管理者 a/a）でログインした係を返します。timeout は1回の呼び出しの上限です。
func Login(timeout time.Duration) (*Client, error) {
	base := strings.TrimRight(os.Getenv("WCMS_BASE"), "/")
	if base == "" {
		base = "https://localhost:8443"
	}
	jar, _ := cookiejar.New(nil)
	// 証明書を確かめないのは、同じ機械の中（localhost・127.0.0.1・::1）だけ——ほかの機械へ繋ぐときに確かめないと、
	// 管理者のパスワードとセッションを途中で盗み見られる（2026-10-22 の見直し）。ほかの機械の自己署名の証明書は、
	// その機械の信頼ストアに入れてから使う。
	insecure := false
	if u, err := url.Parse(base); err == nil {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			insecure = true
		}
	}
	c := &Client{base: base, hc: &http.Client{
		Jar:           jar,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure, RootCAs: trustedRoots()}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       timeout,
	}}
	user, pass := os.Getenv("WCMS_USER"), os.Getenv("WCMS_PASS")
	if user == "" {
		user, pass = "a", "a" // ローカル開発の管理者（引き継ぎ・環境の節）
	}
	res, err := c.Do("POST", "/api/login", strings.NewReader(url.Values{"username": {user}, "password": {pass}}.Encode()),
		"application/x-www-form-urlencoded", "")
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	if strings.Contains(res.Header.Get("Location"), "error") {
		return nil, errors.New("ログインできません")
	}
	return c, nil
}

// Do は口を1つ呼びます（CSRF の守りのため Origin を付ける・ctype と token〔編集ロック〕は空なら付けない）。
func (c *Client) Do(method, path string, body io.Reader, ctype, token string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.base)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	return c.hc.Do(req)
}
