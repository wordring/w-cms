package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEditedSince は、製造のあとに保存されたページを見分けること（2026-10-04・editedSince）を固定します——
// 利用者:「私が編集したページは上書きされると困りますが、編集したかどうかわかりますか？」。
func TestEditedSince(t *testing.T) {
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	built := time.Date(2026, 9, 30, 14, 31, 34, 0, time.FixedZone("JST", 9*3600))
	since := built.Format(time.RFC3339)
	page := func(id string) string {
		dir := filepath.Join("data", "master", id[:2], id, "versions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := bodyPath(id)
		if err := os.WriteFile(body, []byte("<h1>x</h1>"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := built.Add(-time.Minute)
		os.Chtimes(body, old, old)
		return dir
	}
	version := func(dir, at, by string) {
		name := strings.NewReplacer(":", "", "-", "").Replace(at)
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(`{"at":"`+at+`","by":"`+by+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 製造のときの保存だけ（製造の時刻より前）——編集していない。
	d := page("000101")
	version(d, "2026-09-30T05:31:20Z", "a")
	if edited, _ := editedSince("000101", since); edited {
		t.Errorf("製造のときの保存だけなのに、編集したことになっています")
	}
	// 製造のあとの保存——編集した（日時と人を返す）。
	version(d, "2026-10-04T03:46:55Z", "みなと")
	if edited, when := editedSince("000101", since); !edited || !strings.Contains(when, "みなと") || !strings.Contains(when, "2026-10-04") {
		t.Errorf("製造のあとの保存を見分けません: %v %q", edited, when)
	}
	// 版の記録が無くても、本文のファイルが新しければ編集した。
	page("000102")
	now := built.Add(48 * time.Hour)
	os.Chtimes(bodyPath("000102"), now, now)
	if edited, _ := editedSince("000102", since); !edited {
		t.Errorf("本文のファイルが新しいのに、編集していないことになっています")
	}
	// 製造の時刻が読めない（古い記録）なら見分けられない——守らない。
	if edited, _ := editedSince("000101", ""); edited {
		t.Errorf("製造の時刻が無いのに編集したことになっています")
	}
}
