package cms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSiteNameReplacesHeaderAndTitle は、WCMS_SITE_NAME を書くと画面の左上の名前とタブの題の末尾が替わり、
// 書かなければいままでどおりのことを固定します（2026-10-27 利用者:「本番サーバーのw-cmsページのタイトルをw-cms Wiki Editorから板金部に変えます」）。
// ⚠ 置き換えは本物の殻の文字列が目印なので、assets/index.html の目印が変わっていないことも見ます（変わると黙って替わらない）。
func TestSiteNameReplacesHeaderAndTitle(t *testing.T) {
	real, err := os.ReadFile(filepath.Join("..", "..", shellPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, mark := range []string{siteNamePlaceholder, titlePlaceholder} {
		if !strings.Contains(string(real), mark) {
			t.Fatalf("⚠ 本物の殻に目印 %q がありません（サイトの名前が替わらなくなる）", mark)
		}
	}
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd); shellCache.reset() })
	os.MkdirAll("assets", 0755)
	shell := `<html><head><title>w-cms エディタ</title></head><body><h1 class="title">w-cms Wiki Editor</h1><!--WCMS_CONTENT--></body></html>`
	os.WriteFile(filepath.Join("assets", "index.html"), []byte(shell), 0644)
	shellCache.reset()

	t.Setenv("WCMS_SITE_NAME", "")
	got, _ := RenderPageShell("", "", "カバー右")
	if !strings.Contains(got, `<h1 class="title">w-cms Wiki Editor</h1>`) || !strings.Contains(got, "<title>カバー右 - w-cms</title>") {
		t.Errorf("書かなければいままでどおりのはず: %s", got)
	}
	t.Setenv("WCMS_SITE_NAME", " 板金部<b> ")
	got, _ = RenderPageShell("", "", "カバー右")
	if !strings.Contains(got, `<h1 class="title">板金部&lt;b&gt;</h1>`) || !strings.Contains(got, "<title>カバー右 - 板金部&lt;b&gt;</title>") {
		t.Errorf("⚠ 名前（エスケープした形）が左上とタブの題に出ていません: %s", got)
	}
}
