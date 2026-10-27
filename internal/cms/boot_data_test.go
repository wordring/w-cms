package cms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 最初の答えをページと一緒に送る（2026-10-27・boot_data.go）。
// 利用者:「最初の問い合わせをページと一緒に送る直しと、版の問い合わせを2回から1回にする直しを進めてください。」

// TestBootDataHTML は、最初の答えが、内側で呼んだ口の 200 で JSON の答えだけを住所ごとに持ち、同じ Cookie で呼び、
// `</script>` で閉じられないことを固定します。
func TestBootDataHTML(t *testing.T) {
	var cookies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		cookies = append(cookies, r.Header.Get("Cookie"))
		w.Write([]byte(`{"authenticated":true,"username":"alice"}`))
	})
	mux.HandleFunc("/api/versions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "001351" || r.Method != http.MethodGet {
			http.Error(w, "x", 400)
			return
		}
		w.Write([]byte(`[{"id":"v1","by":"</script><script>alert(1)</script>"}]`))
	})
	mux.HandleFunc("/api/page-perms", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "権限がありません", 403) })
	mux.HandleFunc("/api/children", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`読めない`)) })
	SetBootHandler(mux)
	t.Cleanup(func() { SetBootHandler(nil) })

	req := httptest.NewRequest("POST", "/001351", strings.NewReader("x"))
	req.Header.Set("Cookie", "wcms_session=abc")
	req.Header.Set("If-None-Match", `"x"`)
	out := bootDataHTML(req, "001351")
	if !strings.HasPrefix(out, `<script type="application/json" id="w-boot-data">`) || !strings.HasSuffix(out, `</script>`) {
		t.Fatalf("⚠ 形が違います: %s", out)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(out, `<script type="application/json" id="w-boot-data">`), `</script>`)
	if strings.Contains(inner, "<") {
		t.Errorf("⚠ 中に < が残っています（</script> で閉じられる）: %s", inner)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inner), &got); err != nil {
		t.Fatalf("JSON として読めません: %v %s", err, inner)
	}
	if _, ok := got["/api/me"]; !ok {
		t.Errorf("⚠ /api/me の答えがありません: %s", inner)
	}
	if _, ok := got["/api/versions?id=001351"]; !ok {
		t.Errorf("⚠ 版の答えが、画面の住所と同じ鍵で入っていません: %s", inner)
	}
	for _, k := range []string{"/api/page-perms?id=001351", "/api/children?parent_id=001351"} {
		if _, ok := got[k]; ok {
			t.Errorf("⚠ 失敗した口の答え（%s）を埋めました", k)
		}
	}
	if len(cookies) != 1 || cookies[0] != "wcms_session=abc" {
		t.Errorf("⚠ 同じ Cookie で呼んでいません: %q", cookies)
	}
}

// TestBootPlaceholderInShell は、編集用の殻に最初の答えを差し込む印があることを固定します（無いと黙って埋まらない）。
func TestBootPlaceholderInShell(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", shellPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), bootPlaceholder) {
		t.Errorf("⚠ 殻に %s がありません", bootPlaceholder)
	}
}
