package cms

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// startLocalEdit は「📝 ローカル編集」の鍵を出し、鍵と書けるかを返します。
func startLocalEdit(t *testing.T, user, ref string) (token string, writable bool) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/local-edit/start", strings.NewReader(`{"ref":"`+ref+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, &auth.User{Username: user})
	rr := httptest.NewRecorder()
	LocalEditStartAPIHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("鍵を出せません: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	i := strings.Index(body, "#")
	j := strings.Index(body[i:], `"`)
	if i < 0 || j < 0 || !strings.Contains(body, `"link":"w-cms-edit:`) {
		t.Fatalf("リンクの形が違います: %s", body)
	}
	return body[i+1 : i+j], strings.Contains(body, `"writable":true`)
}

func localEditFile(method, token, ifMatch, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/local-edit/file?token="+token, strings.NewReader(body))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	LocalEditFileAPIHandler(rr, req)
	return rr
}

// TestLocalEditRoundTrip は、常駐ヘルパーの往復（2026-09-29）を固定します——鍵で落とし、保存のたびに上げると、
// **前の中身は版に残り**、ETag が進む。⚠ WebDAV は Ctrl+S をアプリを閉じるまで送らなかった（2026-09-18）ので、
// この口がローカルのアプリで編集する本命。
func TestLocalEditRoundTrip(t *testing.T) {
	setupDavTest(t)
	token, writable := startLocalEdit(t, "alice", "000101-a1b2")
	if !writable {
		t.Fatal("alice は部品Aの添付を書けるはず")
	}
	get := localEditFile("GET", token, "", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "SECTION") || get.Header().Get("ETag") == "" {
		t.Fatalf("落とせません: %d %q", get.Code, get.Body.String())
	}
	etag := get.Header().Get("ETag")

	put := localEditFile("PUT", token, etag, "0\nSECTION\nEDITED\n")
	if put.Code != http.StatusNoContent || put.Header().Get("ETag") == etag {
		t.Fatalf("上げられません: %d %s", put.Code, put.Body.String())
	}
	fp, _ := page.AttachmentPath("000101", "a1b2.dxf")
	if b, _ := os.ReadFile(fp); !strings.Contains(string(b), "EDITED") {
		t.Errorf("中身が差し替わっていません: %q", b)
	}
	if v := AttachmentVersions("000101", "a1b2.dxf"); len(v) != 1 {
		t.Errorf("前の中身が版に残っていません: %v", v)
	}
	// ⚠ 古い ETag のままの上書き（ほかの誰かが先に書き換えた）は断る——黙って上書きしない。
	if rr := localEditFile("PUT", token, etag, "LATE"); rr.Code != http.StatusConflict {
		t.Errorf("古い ETag の上書きを断っていません: %d", rr.Code)
	}
	// ⚠ 空の中身では差し替えない（開いただけで消えるのを防ぐ・WebDAV と同じ）。
	if rr := localEditFile("PUT", token, put.Header().Get("ETag"), ""); rr.Code != http.StatusBadRequest {
		t.Errorf("空の上書きを断っていません: %d", rr.Code)
	}
	// If-Match の無い上書きは断る。
	if rr := localEditFile("PUT", token, "", "X"); rr.Code != http.StatusPreconditionRequired {
		t.Errorf("If-Match の無い上書きを断っていません: %d", rr.Code)
	}
	// 知らない鍵は通さない。
	if rr := localEditFile("GET", "nope", "", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("知らない鍵で読めています: %d", rr.Code)
	}
}

// TestLocalEditRespectsWritePermission は、書けない所（権限・読み取り専用の範囲）への上書きを断ることを固定します
// ——読めるだけの人も、落として開くことはできる。
func TestLocalEditRespectsWritePermission(t *testing.T) {
	setupDavTest(t)
	token, writable := startLocalEdit(t, "bob", "000104-c3d4") // bob は「読むだけ」を読めるが書けない
	if writable {
		t.Fatal("bob は書けないはず")
	}
	get := localEditFile("GET", token, "", "")
	if get.Code != http.StatusOK {
		t.Fatalf("読める人は落とせるはず: %d", get.Code)
	}
	if rr := localEditFile("PUT", token, get.Header().Get("ETag"), "X"); rr.Code != http.StatusForbidden {
		t.Errorf("書けない人の上書きを断っていません: %d", rr.Code)
	}

	withSettings(t, func(s *Settings) { s.WebDAVReadOnly = []string{"部品A"} })
	token, writable = startLocalEdit(t, "alice", "000101-a1b2")
	if writable {
		t.Error("読み取り専用の範囲は書けないはず")
	}
	get = localEditFile("GET", token, "", "")
	if rr := localEditFile("PUT", token, get.Header().Get("ETag"), "X"); rr.Code != http.StatusForbidden {
		t.Errorf("読み取り専用の範囲への上書きを断っていません: %d", rr.Code)
	}
}
