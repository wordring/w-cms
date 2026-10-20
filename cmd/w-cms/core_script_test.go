package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/cms"
)

// TestCoreJSDoesNotCallExtensionRoutes は、コアの画面の JS（assets/app.js）が拡張の口を叩かず、拡張の名前で出し分けないことを
// 固定します（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md）。拡張の画面は、その拡張の JS（ext/<拡張>/assets/*.js）が持ち、
// コアの window.wcms の口に登録します。⚠ この試験のビルドには全部の拡張が載っている（cmd/w-cms の取り付け口）。
func TestCoreJSDoesNotCallExtensionRoutes(t *testing.T) {
	file := filepath.Join("..", "..", "assets", "app.js")
	if p := os.Getenv("WCMS_CORE_JS_FOR_TEST"); p != "" {
		file = p // 番人が効くかを、前の版の写しで確かめるときだけ（ディスクの assets を変えない）
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// コメントの中の名前は数えない（経緯を書いた所がある）。
	code := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(b), "")
	routes := cms.PluginRoutes()
	if len(routes) == 0 {
		t.Fatal("拡張の口が1つもありません（試験の前提が崩れている）")
	}
	for _, r := range routes {
		p := strings.TrimSpace(r.Pattern)
		if i := strings.Index(p, " "); i >= 0 { // "GET /api/…" の形
			p = p[i+1:]
		}
		if strings.Contains(code, "'"+p+"'") || strings.Contains(code, "'"+p+"?") {
			t.Errorf("⚠ コアの app.js が拡張の口 %s を叩いています（拡張の JS へ移すこと）", p)
		}
	}
	if m := regexp.MustCompile(`hasExtension\('[^']+'\)`).FindString(code); m != "" {
		t.Errorf("⚠ コアの app.js が拡張の名前で出し分けています: %s", m)
	}
}
