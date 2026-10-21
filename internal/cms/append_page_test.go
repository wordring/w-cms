package cms

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 属性のある h1（エディタで保存したページ）でも、題を2つにせず中身だけ替える（2026-10-22 の見直し）。
func TestSetPageH1KeepsOneTitle(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000061", `<h1 data-id="ab12">古い題</h1><p>本文</p>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	if err := SetPageH1("000061", "a", "新しい題"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(page.BodyPath("000061"))
	s := string(body)
	if strings.Count(s, "<h1") != 1 || !strings.Contains(s, "新しい題") || strings.Contains(s, "古い題") || !strings.Contains(s, `data-id="ab12"`) {
		t.Errorf("⚠ 題の替え方が違います: %s", s)
	}
}
