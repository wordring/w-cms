package comm

import (
	"strings"
	"testing"
)

// 深すぎる入れ子のメールでも、上限で止まって読めた分を返す（読み手が止まらない・2026-10-22 の見直し）。
func TestCollectPartsStopsAtDepth(t *testing.T) {
	var b strings.Builder
	depth := 2000
	for i := 0; i < depth; i++ {
		b.WriteString("--b\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n")
	}
	body := b.String()
	parts, err := collectParts("multipart/mixed; boundary=b", "", strings.NewReader(body))
	if err != nil {
		t.Fatalf("深い入れ子で誤りになりました: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("本文の無いメールから %d 個のパートを返しました", len(parts))
	}
}

// 空白が混じり、詰め物（=）の無い base64 の添付も読む（厳しく読むと黙って消えた・2026-10-22 の見直し）。
func TestCollectPartsLenientBase64(t *testing.T) {
	// "hello world" を base64 にして、空白を混ぜ、詰め物を外す。
	parts, err := collectParts("application/pdf", "base64", strings.NewReader("aGVsbG8g d29y\r\nbGQ"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || string(parts[0].body) != "hello world" {
		t.Errorf("⚠ 寛く読めません: %+v", parts)
	}
}

// 名前の無い PDF のパートも、添付として残す（黙って消えていた・2026-10-22 の見直し）。本文の候補（text/plain）には名前を付けない。
func TestCollectPartsNamesUnnamedAttachments(t *testing.T) {
	body := "--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n本文\r\n" +
		"--b\r\nContent-Type: application/pdf\r\n\r\n%PDF-1.4 x\r\n--b--\r\n"
	parts, err := collectParts("multipart/mixed; boundary=b", "", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].fileName != "" || !strings.HasSuffix(parts[1].fileName, ".pdf") {
		t.Errorf("⚠ 名前の付け方が違います: %+v", parts)
	}
}
