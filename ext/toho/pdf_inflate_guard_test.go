package toho

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// TestPDFInflateGuard は、展開すると上限を超える PDF（展開爆弾）を断り、ふつうの PDF は通すことを固定します（2026-10-26）。
func TestPDFInflateGuard(t *testing.T) {
	flate := func(n int) []byte {
		var b bytes.Buffer
		w := zlib.NewWriter(&b)
		w.Write(bytes.Repeat([]byte{0}, n))
		w.Close()
		return b.Bytes()
	}
	pdf := func(streams ...[]byte) []byte {
		var b bytes.Buffer
		b.WriteString("%PDF-1.4\n")
		for i, s := range streams {
			b.WriteString("1 0 obj\n<< /Length " + string(rune('0'+i)) + " /Filter /FlateDecode >>\nstream\r\n")
			b.Write(s)
			b.WriteString("\nendstream\nendobj\n")
		}
		b.WriteString("%%EOF\n")
		return b.Bytes()
	}
	old := pdfInflateBudget
	pdfInflateBudget = 1 << 20 // 1 MB
	t.Cleanup(func() { pdfInflateBudget = old })
	if !pdfInflateWithinBudget(pdf(flate(100<<10), flate(100<<10))) {
		t.Errorf("ふつうの PDF（200 KB に広がる）を断りました")
	}
	if pdfInflateWithinBudget(pdf(flate(600<<10), flate(600<<10))) {
		t.Errorf("⚠ 合わせて 1.2 MB に広がる PDF を通しました")
	}
	if pdfInflateWithinBudget(pdf(flate(4 << 20))) {
		t.Errorf("⚠ 4 MB に広がる stream を通しました")
	}
	if !pdfInflateWithinBudget([]byte("%PDF-1.4\nstream\nnot flate\nendstream\n%%EOF")) {
		t.Errorf("Flate でない stream で断りました")
	}
}
