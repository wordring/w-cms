package page

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMentionsWhole は、匿名に配る添付の確かめが名前の前方一致で通らないことを固定します（2026-10-30）。
func TestMentionsWhole(t *testing.T) {
	const body = `<p><img src="/000001/a.pdf"></p><a href="/000001/b.png?v=1">b</a><p>/000001/c.txt</p>`
	for needle, want := range map[string]bool{
		"/000001/a.pdf": true,
		"/000001/a":     false,
		"/000001/a.p":   false,
		"/000001/b.png": true,
		"/000001/c.txt": true,
		"/000001/d.pdf": false,
	} {
		if got := mentionsWhole(body, needle); got != want {
			t.Errorf("%s: %v（%v のはず）", needle, got, want)
		}
	}
	if !mentionsWhole(`x /000001/a.pdf.bak y /000001/a.pdf"`, "/000001/a.pdf") {
		t.Error("前方一致の先に本物があるときは、見つけるはず")
	}
}

// TestEnsureSidecarKeepsBrokenFile は、壊れたサイドカーを上書きしないことを固定します（2026-10-30——権限の正本を黙って作り直さない）。
func TestEnsureSidecarKeepsBrokenFile(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)
	const id = "000123"
	p := SidecarPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{こわれた"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSidecar(id, "alice", "", "", ""); err == nil || !strings.Contains(err.Error(), "読めません") {
		t.Errorf("壊れたサイドカーは断るはず: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "{こわれた" {
		t.Errorf("⚠ 上書きされました: %s", b)
	}
	// 無いときは、いままでどおり作る。
	if err := EnsureSidecar("000124", "alice", "", "", ""); err != nil {
		t.Errorf("新しいページのサイドカーを作れません: %v", err)
	}
}
