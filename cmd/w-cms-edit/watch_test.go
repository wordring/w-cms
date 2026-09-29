//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestWatchUploadsOnSave は、手元のファイルが**保存で変わるたびに**上がり、ETag を引き継いで次も上がることを
// 固定します（アプリで開く所は除く——窓が出るため）。⚠ WebDAV は Ctrl+S をアプリを閉じるまで送らなかったので、
// 「保存のたびに届く」がこの道具のいちばんの約束。
func TestWatchUploadsOnSave(t *testing.T) {
	var mu sync.Mutex
	content := []byte("v1")
	tag := func(b []byte) string { s := sha256.Sum256(b); return `"` + hex.EncodeToString(s[:8]) + `"` }
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPut {
			http.Error(w, "x", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("If-Match") != tag(content) {
			http.Error(w, "ほかの誰かが先に書き換えています", http.StatusConflict)
			return
		}
		b, _ := io.ReadAll(r.Body)
		content = b
		got = append(got, string(b))
		w.Header().Set("ETag", tag(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	local := filepath.Join(t.TempDir(), "図面.dxf")
	os.WriteFile(local, []byte("v1"), 0o644)
	t.Setenv("LOCALAPPDATA", t.TempDir()) // 記録.log を試験の置き場へ
	done := make(chan error, 1)
	go func() { done <- watch(local, srv.URL+"/api/local-edit/file?token=x", srv.URL, tag([]byte("v1")), []byte("v1")) }()

	save := func(s string) {
		time.Sleep(1200 * time.Millisecond) // 時刻の刻みが変わるように
		os.WriteFile(local, []byte(s), 0o644)
	}
	waitFor := func(n int) {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			l := len(got)
			mu.Unlock()
			if l >= n {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%d 回目の保存が上がりません: %v", n, got)
	}
	save("v2")
	waitFor(1)
	save("v3") // 2回目——前の ETag を引き継いでいれば通る（引き継がないと 409 で止まる）
	waitFor(2)
	mu.Lock()
	if got[0] != "v2" || got[1] != "v3" {
		t.Errorf("上がった中身が違います: %v", got)
	}
	mu.Unlock()
	os.Remove(local) // 手元のファイルが無くなると見張りは終わる
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("手元のファイルを消しても見張りが終わりません")
	}
}
