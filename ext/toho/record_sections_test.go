package toho

import (
	"strings"
	"testing"
)

// 記録の箱の「まとめて解析」のボタン（record_sections.go・2026-10-20 夕からサーバーが描く）——件数は解析済みを除いた数で、
// 全部解析済みなら出さない。押したときの働き（引き直して飛ばす）は e2e/verify-zip-analyze.js の④。
func TestZipAnalyzeButtonCountsOnlyUnanalyzed(t *testing.T) {
	pdfs := []zipPDF{{ID: "aaaa", File: "aaaa.pdf", Path: "a/A-100.pdf"}, {ID: "bbbb", File: "bbbb.pdf", Path: "b/B-200.pdf"}}

	got := zipAnalyzeButtonHTML("000010", "zzzz", pdfs, map[string]analyzedResult{})
	if !strings.Contains(got, "（2件）") || !strings.Contains(got, `data-zip="zzzz"`) || strings.Contains(got, "飛ばします") {
		t.Errorf("解析済みが無いときの形が違います: %s", got)
	}
	got = zipAnalyzeButtonHTML("000010", "zzzz", pdfs, map[string]analyzedResult{"aaaa": {Kind: "図面", PageID: "000011"}})
	if !strings.Contains(got, "（1件）") || !strings.Contains(got, "解析済みの 1件は飛ばします") {
		t.Errorf("解析済みを除いて数えていません: %s", got)
	}
	if got := zipAnalyzeButtonHTML("000010", "zzzz", pdfs, map[string]analyzedResult{"aaaa": {}, "bbbb": {}}); got != "" {
		t.Errorf("⚠ 全部解析済みなのにボタンを出しています: %s", got)
	}
	if strings.Contains(zipAnalyzeButtonHTML("000010", "zzzz", pdfs, nil), "attach-expand") {
		t.Errorf("⚠ コアの .attach-expand を付けています（コアが添付の札を描き直すたびに消す）")
	}
}
