package toho

import (
	"bytes"
	"compress/zlib"
	"strconv"
	"testing"
)

// TestPDFInflateGuard は、展開すると上限を超える PDF（展開爆弾）を断り、ふつうの PDF は通すことを固定します（2026-10-26）。
// 2026-10-27 の見直しで、/Length で切ること（中に文字の endstream を混ぜてもすり抜けない）・ファイルより大きな /Length を断る
// こと・間接の /Length も読むことを足しました。
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
			b.WriteString(strconv.Itoa(i+1) + " 0 obj\n<< /Length " + strconv.Itoa(len(s)) + " /Filter /FlateDecode >>\nstream\r\n")
			b.Write(s)
			b.WriteString("\nendstream\nendobj\n")
		}
		b.WriteString("%%EOF\n")
		return b.Bytes()
	}
	old := pdfInflateBudget
	pdfInflateBudget = 1 << 20 // 1 MB
	t.Cleanup(func() { pdfInflateBudget = old })
	if !pdfInflateWithinBudget(pdf(flate(100<<10), flate(100<<10)), true) {
		t.Errorf("ふつうの PDF（200 KB に広がる）を断りました")
	}
	if pdfInflateWithinBudget(pdf(flate(600<<10), flate(600<<10)), true) {
		t.Errorf("⚠ 合わせて 1.2 MB に広がる PDF を通しました")
	}
	if pdfInflateWithinBudget(pdf(flate(4<<20)), true) {
		t.Errorf("⚠ 4 MB に広がる stream を通しました")
	}
	if !pdfInflateWithinBudget([]byte("%PDF-1.4\n1 0 obj\n<< /Length 9 >>\nstream\nnot flate\nendstream\nendobj\n%%EOF"), true) {
		t.Errorf("Flate でない stream で断りました")
	}

	// 縮めたデータの中に、文字の「endstream」を混ぜる（縮めずに入れる形）——最初の endstream で切ると、その先を見なかった。
	var trick bytes.Buffer
	w, _ := zlib.NewWriterLevel(&trick, zlib.NoCompression)
	w.Write([]byte("endstream"))
	w.Write(bytes.Repeat([]byte{0}, 4<<20))
	w.Close()
	if pdfInflateWithinBudget(pdf(trick.Bytes()), true) {
		t.Errorf("⚠ 中に endstream を混ぜた 4 MB の stream を通しました")
	}

	// ファイルより大きな /Length（gofpdi がその大きさのメモリを取って落ちる）。
	huge := []byte("%PDF-1.4\n1 0 obj\n<< /Length 1099511627776 /Filter /FlateDecode >>\nstream\nxx\nendstream\nendobj\n%%EOF")
	if pdfInflateWithinBudget(huge, false) {
		t.Errorf("⚠ ファイルより大きな /Length を通しました")
	}
	// 間接の /Length（「2 0 R」）も読む。参照先が大きすぎれば断る。
	indirect := []byte("%PDF-1.4\n1 0 obj\n<< /Length 2 0 R >>\nstream\nxx\nendstream\nendobj\n2 0 obj\n99999999999\nendobj\n%%EOF")
	if pdfInflateWithinBudget(indirect, false) {
		t.Errorf("⚠ 間接の /Length がファイルより大きいのを通しました")
	}
	// 作り直した PDF（strict）で /Length が読めない stream は断る。作り直していなければ、endstream まで見る。
	noLen := []byte("%PDF-1.4\n1 0 obj\n<< >>\nstream\nxx\nendstream\nendobj\n%%EOF")
	if pdfInflateWithinBudget(noLen, true) {
		t.Errorf("⚠ 作り直した PDF で /Length の無い stream を通しました")
	}
	if !pdfInflateWithinBudget(noLen, false) {
		t.Errorf("作り直していない PDF の /Length の無い stream を断りました")
	}
}
