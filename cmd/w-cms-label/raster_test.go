package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// statusBytes は試験の答え（62mm の長尺・誤りなし）を組みます。
func statusBytes(typ byte, err1, err2 byte) []byte {
	b := make([]byte, statusLength)
	b[0], b[1] = 0x80, 0x20
	b[8], b[9] = err1, err2
	b[10], b[11] = 62, mediaContinuous
	b[18] = typ
	return b
}

// TestRasterLineMirrorsWithMargin は、絵の左端の点が行の末尾の側（余白 12 ピンの手前）に、右端の点が頭の側（12 ピン目）に
// 置かれることを固定します（2026-10-30 の試し刷りで、この置き方で左右が正しく出た）。
func TestRasterLineMirrorsWithMargin(t *testing.T) {
	tp := tapes[62]
	p := page{Width: tp.dots, Height: 1, Bits: make([]byte, (tp.dots+7)/8)}
	p.Bits[0] = 0x80                  // x=0（左端）
	p.Bits[len(p.Bits)-1] = 0x80 >> 7 // x=695（右端・696 は 87 バイトちょうど）
	line := rasterLine(p, tp, 0)
	if len(line) != lineBytes {
		t.Fatalf("1行は %d バイトのはず: %d", lineBytes, len(line))
	}
	on := func(i int) bool { return line[i>>3]&(0x80>>(i&7)) != 0 }
	if !on(12 + 695) {
		t.Errorf("左端の点は %d ピン目のはず", 12+695)
	}
	if !on(12) {
		t.Errorf("右端の点は 12 ピン目のはず")
	}
	count := 0
	for i := 0; i < headPins; i++ {
		if on(i) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("点は2つのはず: %d", count)
	}
}

// TestPageCommandsLayout は、1枚分の命令の並び（手順書の流れ）を固定します。
func TestPageCommandsLayout(t *testing.T) {
	tp := tapes[62]
	p := page{Width: tp.dots, Height: 3, Bits: make([]byte, 87*3)}
	b := pageCommands(p, tp, 62, true, true)
	want := []byte{0x1b, 'i', 'a', 0x01, 0x1b, 'i', 'z', 0xC6, 0x0A, 62, 0, 3, 0, 0, 0, 0, 0,
		0x1b, 'i', 'M', 0x40, 0x1b, 'i', 'A', 0x01, 0x1b, 'i', 'K', 0x08, 0x1b, 'i', 'd', 35, 0}
	if !bytes.HasPrefix(b, want) {
		t.Fatalf("頭の命令が違います:\n got % x\nwant % x", b[:len(want)], want)
	}
	if len(b) != len(want)+3*(3+lineBytes)+1 {
		t.Errorf("長さが違います: %d", len(b))
	}
	if b[len(b)-1] != 0x1a {
		t.Errorf("最後の1枚は 1Ah で終わるはず: %x", b[len(b)-1])
	}
	if mid := pageCommands(p, tp, 62, false, false); mid[15] != 1 || mid[len(mid)-1] != 0x0c {
		t.Errorf("2枚目からは n9=1・途中の札は 0Ch で終わるはず: n9=%d 末尾=%x", mid[15], mid[len(mid)-1])
	}
}

func TestStatusErrors(t *testing.T) {
	st, err := parseStatus(statusBytes(statusReply, 0x01, 0x10))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.errorText(); !strings.Contains(got, "ロールが入っていません") || !strings.Contains(got, "ふたが開いています") {
		t.Errorf("誤りの言葉: %q", got)
	}
	if _, err := st.tapeOf(); err == nil {
		t.Error("誤りのあるときは刷れないはず")
	}
	die := statusBytes(statusReply, 0, 0)
	die[11] = 0x0B
	st, _ = parseStatus(die)
	if _, err := st.tapeOf(); err == nil || !strings.Contains(err.Error(), "長尺") {
		t.Errorf("決まった大きさの札は断るはず: %v", err)
	}
	if _, err := parseStatus([]byte{1, 2, 3}); err == nil {
		t.Error("短い答えは読めないはず")
	}
}

// fakeDevice は、書いたものを貯め、決めた答えを順に返します。
type fakeDevice struct {
	written [][]byte
	replies [][]byte
}

func (f *fakeDevice) Write(b []byte, _ time.Duration) error {
	f.written = append(f.written, append([]byte(nil), b...))
	return nil
}

func (f *fakeDevice) Read(_ time.Duration) ([]byte, error) {
	// 初めの読み捨て（書く前の読み）には何も返さない。
	if len(f.written) == 0 || len(f.replies) == 0 {
		return nil, errNoAnswer
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

func (f *fakeDevice) Close() error { return nil }

func TestPrintWaitsEachPage(t *testing.T) {
	f := &fakeDevice{replies: [][]byte{
		statusBytes(statusReply, 0, 0),
		statusBytes(0x06, 0, 0), statusBytes(statusPrinted, 0, 0), // 1枚目: 段の変わり目 → 刷り終わった
		statusBytes(statusPrinted, 0, 0), // 2枚目
	}}
	p := &printer{open: func() (device, error) { return f, nil }}
	pg := page{Width: 696, Height: 2, Bits: make([]byte, 87*2)}
	n, err := p.Print([]page{pg, pg})
	if err != nil || n != 2 {
		t.Fatalf("2枚刷れるはず: %d %v", n, err)
	}
	if len(f.written) != 3 { // 初め・1枚目・2枚目
		t.Fatalf("書いた回数が違います: %d", len(f.written))
	}
}

func TestPrintStopsOnError(t *testing.T) {
	f := &fakeDevice{replies: [][]byte{
		statusBytes(statusReply, 0, 0),
		statusBytes(statusError, 0, 0x40),
	}}
	p := &printer{open: func() (device, error) { return f, nil }}
	pg := page{Width: 696, Height: 2, Bits: make([]byte, 87*2)}
	n, err := p.Print([]page{pg, pg})
	if err == nil || n != 0 || !strings.Contains(err.Error(), "紙を送れません") {
		t.Fatalf("1枚目で止まるはず: %d %v", n, err)
	}
}

func TestPrintRefusesWrongWidth(t *testing.T) {
	f := &fakeDevice{replies: [][]byte{statusBytes(statusReply, 0, 0)}}
	p := &printer{open: func() (device, error) { return f, nil }}
	_, err := p.Print([]page{{Width: 306, Height: 1, Bits: make([]byte, 39)}})
	if err == nil || !strings.Contains(err.Error(), "696") {
		t.Fatalf("幅の違う札は断るはず: %v", err)
	}
	if len(f.written) != 1 {
		t.Errorf("断るときは札の命令を書かないはず: %d", len(f.written))
	}
}

func TestStateWhenOff(t *testing.T) {
	p := &printer{open: func() (device, error) { return nil, errOff }}
	s := p.State()
	if s.Connected || !strings.Contains(s.Error, "電源") {
		t.Fatalf("電源が切れているときの答え: %+v", s)
	}
}
