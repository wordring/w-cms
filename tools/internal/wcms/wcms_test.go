package wcms

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoginAndDo は、ログインがフォーム形式で名前とパスワードを送り、口を呼ぶときに Origin（CSRF の守り）と
// 編集ロックの札を付けることを確かめます（道具3本の写しを寄せた口の番人・2026-10-09）。
func TestLoginAndDo(t *testing.T) {
	var gotLogin, gotOrigin, gotToken, gotCookie string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			r.ParseForm()
			gotLogin = r.Form.Get("username") + "/" + r.Form.Get("password") + " " + r.Header.Get("Content-Type")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusFound)
		case "/api/x":
			gotOrigin, gotToken = r.Header.Get("Origin"), r.Header.Get("X-Lock-Token")
			if c, err := r.Cookie("session"); err == nil {
				gotCookie = c.Value
			}
		}
	}))
	defer srv.Close()
	t.Setenv("WCMS_BASE", srv.URL+"/")
	t.Setenv("WCMS_USER", "u1")
	t.Setenv("WCMS_PASS", "p1")
	c, err := Login(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if gotLogin != "u1/p1 application/x-www-form-urlencoded" {
		t.Errorf("ログインで送ったもの = %q", gotLogin)
	}
	res, err := c.Do("POST", "/api/x", strings.NewReader("{}"), "application/json", "tok")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if gotOrigin != srv.URL || gotToken != "tok" || gotCookie != "s1" {
		t.Errorf("Origin=%q 札=%q クッキー=%q（Origin は末尾の / を落とした基の URL・札とログインのクッキーが付くはず）", gotOrigin, gotToken, gotCookie)
	}
	if _, err := c.Do("GET", "/api/x", nil, "", ""); err != nil || gotToken != "" {
		t.Errorf("札が空なのに付けています: %q %v", gotToken, err)
	}
}

// TestLoginRefused は、ログインの戻り先に error が付いていたら断ることを確かめます。
func TestLoginRefused(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/login?error=1")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	t.Setenv("WCMS_BASE", srv.URL)
	if _, err := Login(time.Minute); err == nil {
		t.Error("ログインに失敗したのに係を返しています")
	}
}

// TestTrustedRootsFromWCMSCA は、ほかの機械の自己署名の証明書を、WCMS_CA のファイルで信じて繋げること・
// 信じる証明書が無ければ断ることを固定します（2026-10-28——それまで道具は確かめを切るか、この機械の信頼ストアだけを見ていた）。
func TestTrustedRootsFromWCMSCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	get := func() error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trustedRoots()}}}
		res, err := c.Get(srv.URL)
		if err == nil {
			res.Body.Close()
		}
		return err
	}

	t.Setenv("WCMS_CA", filepath.Join(t.TempDir(), "無い.crt"))
	if err := get(); err == nil {
		t.Fatal("⚠ 信じる証明書が無いのに、自己署名のサーバーへ繋がりました")
	}

	ca := filepath.Join(t.TempDir(), "server.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WCMS_CA", ca)
	if err := get(); err != nil {
		t.Fatalf("WCMS_CA の証明書で繋がりません: %v", err)
	}
}

// TestFileForm は、ファイル1つを送る multipart を、前に書く欄の順・ファイルの欄の名前と送る名前・中身のとおりに組むことを固定します
// （2026-10-29——メールの取り込みとワンノートの製造が写していた手順を寄せた）。
func TestFileForm(t *testing.T) {
	body, ctype, err := FileForm("pdf_file", "図面.pdf", []byte("%PDF-1.4"), [2]string{"page_id", "000123"})
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(body, params["boundary"])
	p, err := r.NextPart()
	if err != nil || p.FormName() != "page_id" {
		t.Fatalf("1つ目は page_id の欄のはず: %v %v", p, err)
	}
	if b, _ := io.ReadAll(p); string(b) != "000123" {
		t.Errorf("page_id の値が違います: %q", b)
	}
	p, err = r.NextPart()
	if err != nil || p.FormName() != "pdf_file" || p.FileName() != "図面.pdf" {
		t.Fatalf("2つ目はファイルの欄のはず: %v %v", p, err)
	}
	if b, _ := io.ReadAll(p); string(b) != "%PDF-1.4" {
		t.Errorf("中身が違います: %q", b)
	}
	if _, err := r.NextPart(); err != io.EOF {
		t.Errorf("欄が多すぎます: %v", err)
	}
}
