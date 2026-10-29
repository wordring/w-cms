package cms

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// TestUploadedFile は、JSON で答える口のファイルの受け取り（ParseUploadForm → UploadedFile・2026-10-29）を固定します。
//
//   - 名前は人のアップロードと同じ関門を通る——逆スラッシュで区切ったフォルダも落とす（OS に依らない）・制御文字とドットで始まる名前は断る
//   - 許す拡張子の外・中身が名乗りと違う（PDF の先頭の印が無い）は断る
//   - 添付の上限を超えるものは 413
//
// それまでは FAX の2つの口が `filepath.Base` で名前を落としていて、Linux では `..\x.pdf` の逆スラッシュが残り、
// 改行の入った名前も監査と記録の本文へ写っていた。
func TestUploadedFile(t *testing.T) {
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	call := func(name string, content []byte) (int, string, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "x")
		fw.Write(content)
		mw.Close()
		// CreateFormFile は名前の引用符をエスケープするので、名前は見出しを書き換えて生のまま渡す。
		raw := strings.Replace(buf.String(), `filename="x"`, `filename="`+name+`"`, 1)
		req := httptest.NewRequest("POST", "/api/x", strings.NewReader(raw))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rr := httptest.NewRecorder()
		got := ""
		if ParseUploadForm(rr, req) {
			if n, _, ok := UploadedFile(rr, req, "file", map[string]bool{".pdf": true}, "PDF だけです"); ok {
				got = n
			}
		}
		var out struct {
			Message string `json:"message"`
		}
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, got, out.Message
	}

	if code, got, msg := call(`..\..\見積.pdf`, pdf); code != http.StatusOK || got != "見積.pdf" {
		t.Errorf("逆スラッシュのフォルダ: %d %q %s（見積.pdf だけが残るはず）", code, got, msg)
	}
	if code, got, msg := call("a/b/見積.pdf", pdf); code != http.StatusOK || got != "見積.pdf" {
		t.Errorf("スラッシュのフォルダ: %d %q %s", code, got, msg)
	}
	for _, c := range []struct {
		name    string
		content []byte
		code    int
	}{
		{"見\t積.pdf", pdf, http.StatusBadRequest},
		{".hidden.pdf", pdf, http.StatusBadRequest},
		{"見積.docx", pdf, http.StatusBadRequest},
		{"見積.pdf", []byte("PDF ではない"), http.StatusBadRequest},
		{"見積.pdf", append([]byte("%PDF-"), bytes.Repeat([]byte("x"), int(MaxUploadBytes()))...), http.StatusRequestEntityTooLarge},
	} {
		if code, got, msg := call(c.name, c.content); code != c.code || got != "" {
			t.Errorf("%q: %d %q %s（%d で断るはず）", c.name, code, got, msg, c.code)
		}
	}
}

// TestGateJSONAdminPost は、管理者だけの JSON の書き込み口の入口（2026-10-29）を固定します。
func TestGateJSONAdminPost(t *testing.T) {
	call := func(method string, u *auth.User) (int, string) {
		req := httptest.NewRequest(method, "/api/x", nil)
		if u != nil {
			req = auth.WithUser(req, u)
		}
		rr := httptest.NewRecorder()
		if _, ok := GateJSONAdminPost(rr, req, "過去のデータの移し"); ok {
			return 0, ""
		}
		var out struct {
			Message string `json:"message"`
		}
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out.Message
	}
	if code, msg := call("POST", &auth.User{Username: "bob"}); code != http.StatusForbidden || msg != "管理者だけが使えます（過去のデータの移し）" {
		t.Errorf("管理者でない人: %d %q（403 で断るはず）", code, msg)
	}
	if code, _ := call("POST", nil); code != http.StatusForbidden {
		t.Errorf("ログインしていない: %d（403 のはず）", code)
	}
	if code, _ := call("GET", &auth.User{Username: "a", IsAdmin: true}); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d（405 のはず）", code)
	}
	if code, _ := call("POST", &auth.User{Username: "a", IsAdmin: true}); code != 0 {
		t.Errorf("管理者の POST は通るはず: %d", code)
	}
}
