package main

// ブラザーの QL-800 に送る命令（ラスターコマンド）と、プリンターが返す状態の読み方です。
// 正本は QL-800/810W/820NWB の Software Developer's Manual（Raster Command Reference）——docs/考察/【考察】現品票の印刷.md §6.2。
//
//   - 1行は 720 ピン（90 バイト）。長尺の 62mm は、両端の 12 ピンずつが余白で、印字は 696 点です（手順書の「2. Print Data」の表）。
//   - 行の中の点の並びは、絵の左右を裏返して置きます（試し刷りで確かめた・2026-10-30）。
//   - QL-800 は、圧縮（`M`）と、空の行の命令（`Z`）を受けません。行は全部 `g` で送ります。

import (
	"errors"
	"fmt"
	"strings"
)

const (
	headPins     = 720
	lineBytes    = headPins / 8
	statusLength = 32
	// feedMargin は長尺の前後の余白（点・ESC i d）です。手順書の例と同じ 35 点（約 3mm）。
	feedMargin = 35
	// maxLines は1枚の長さの上限（点）です。約 254mm——長尺は 1m まで刷れますが、札にはこれで足りる。
	maxLines = 3000
)

// tape は長尺のロールの幅ごとの印字の点と余白です（手順書の「Continuous length tape」の表）。
type tape struct {
	dots        int // 印字の点（行の幅）
	rightMargin int // 行の頭（first byte の側）から数えた余白のピン
}

var tapes = map[int]tape{
	12: {dots: 106, rightMargin: 29},
	29: {dots: 306, rightMargin: 6},
	38: {dots: 413, rightMargin: 12},
	50: {dots: 554, rightMargin: 12},
	54: {dots: 590, rightMargin: 0},
	62: {dots: 696, rightMargin: 12},
}

// page は1枚の札の白黒の絵です。行は送る向き（長さの向き）に並び、1行の幅は tape の dots です。
// Bits は1行 (Width+7)/8 バイトで、左の点が上の桁（PBM の P4 と同じ）。
type page struct {
	Width  int
	Height int
	Bits   []byte
}

func (p page) rowBytes() int { return (p.Width + 7) / 8 }

func (p page) check(t tape) error {
	if p.Width != t.dots {
		return fmt.Errorf("札の幅が %d 点です（いまのロールは %d 点）", p.Width, t.dots)
	}
	if p.Height < 1 || p.Height > maxLines {
		return fmt.Errorf("札の長さが %d 点です（1〜%d）", p.Height, maxLines)
	}
	if len(p.Bits) != p.rowBytes()*p.Height {
		return fmt.Errorf("札の絵の大きさが合いません（%d バイト・%d 行なら %d バイト）", len(p.Bits), p.Height, p.rowBytes()*p.Height)
	}
	return nil
}

// rasterLine は絵の y 行目を、プリンターの1行（90 バイト）にします。
func rasterLine(p page, t tape, y int) []byte {
	out := make([]byte, lineBytes)
	row := p.Bits[y*p.rowBytes() : (y+1)*p.rowBytes()]
	for x := 0; x < p.Width; x++ {
		if row[x>>3]&(0x80>>(x&7)) == 0 {
			continue
		}
		i := t.rightMargin + (p.Width - 1 - x) // 左右を裏返して置く
		out[i>>3] |= 0x80 >> (i & 7)
	}
	return out
}

// startCommands は、仕事の初めに送る命令です（止まっている送りを流す → 初期化 → 状態を問う）。
func startCommands() []byte {
	b := make([]byte, 200, 220)
	b = append(b, 0x1b, '@')
	b = append(b, 0x1b, 'i', 'S')
	return b
}

// statusRequest は状態を問う命令です。
func statusRequest() []byte { return []byte{0x1b, 'i', 'S'} }

// pageCommands は1枚分の命令です（first は1枚目か・last は最後の1枚か）。
func pageCommands(p page, t tape, widthMM int, first, last bool) []byte {
	b := make([]byte, 0, 64+p.Height*(3+lineBytes))
	b = append(b, 0x1b, 'i', 'a', 0x01) // ラスターの形
	n9 := byte(1)
	if first {
		n9 = 0
	}
	h := p.Height
	b = append(b, 0x1b, 'i', 'z',
		0x02|0x04|0x40|0x80, // 種類・幅が有効・質を先に・いつも立て直す
		0x0A,                // 長尺
		byte(widthMM), 0,
		byte(h), byte(h>>8), byte(h>>16), byte(h>>24),
		n9, 0)
	b = append(b, 0x1b, 'i', 'M', 0x40) // 自動で切る
	b = append(b, 0x1b, 'i', 'A', 0x01) // 1枚ごとに切る
	b = append(b, 0x1b, 'i', 'K', 0x08) // 最後で切る
	b = append(b, 0x1b, 'i', 'd', byte(feedMargin), byte(feedMargin>>8))
	for y := 0; y < h; y++ {
		b = append(b, 'g', 0x00, lineBytes)
		b = append(b, rasterLine(p, t, y)...)
	}
	if last {
		b = append(b, 0x1a) // 刷って送り出す
	} else {
		b = append(b, 0x0c) // 次の札がある
	}
	return b
}

// status はプリンターが返す 32 バイトの状態です。
type status struct {
	Err1, Err2 byte
	WidthMM    int
	MediaType  byte // 0Ah 長尺・0Bh 決まった大きさの札
	LengthMM   int
	Type       byte // 00h 問いへの答え・01h 刷り終わった・02h 誤り・04h 電源が切れた・05h 知らせ・06h 段の変わり目
	Phase      byte // 00h 受け取る段・01h 刷る段
}

const (
	statusReply     = 0x00
	statusPrinted   = 0x01
	statusError     = 0x02
	statusOff       = 0x04
	mediaContinuous = 0x0A
)

func parseStatus(b []byte) (status, error) {
	if len(b) != statusLength || b[0] != 0x80 || b[1] != 0x20 {
		return status{}, errors.New("プリンターの答えが読めません")
	}
	return status{Err1: b[8], Err2: b[9], WidthMM: int(b[10]), MediaType: b[11], LengthMM: int(b[17]), Type: b[18], Phase: b[19]}, nil
}

// errorText は誤りのビットを、人の言葉にします（無ければ空）。
func (s status) errorText() string {
	var out []string
	for _, e := range []struct {
		on   bool
		text string
	}{
		{s.Err1&0x01 != 0, "ロールが入っていません"},
		{s.Err1&0x02 != 0, "札が終わりました"},
		{s.Err1&0x04 != 0, "カッターが詰まりました"},
		{s.Err1&0x10 != 0, "ほかの仕事を刷っています"},
		{s.Err1&0x20 != 0, "電源が切れています"},
		{s.Err2&0x01 != 0, "ロールを替えてください"},
		{s.Err2&0x02 != 0, "受け取る場所がいっぱいです"},
		{s.Err2&0x04 != 0, "やりとりに失敗しました"},
		{s.Err2&0x10 != 0, "ふたが開いています"},
		{s.Err2&0x40 != 0, "紙を送れません（ロールの終わりかもしれません）"},
		{s.Err2&0x80 != 0, "プリンターの中で誤りが起きました"},
	} {
		if e.on {
			out = append(out, e.text)
		}
	}
	return strings.Join(out, "・")
}

// tapeOf は、いま入っているロールに刷れるかを確かめ、その幅の決まりを返します。
func (s status) tapeOf() (tape, error) {
	if t := s.errorText(); t != "" {
		return tape{}, errors.New(t)
	}
	if s.MediaType != mediaContinuous {
		return tape{}, fmt.Errorf("長尺のロールではありません（%dmm × %dmm の札）——いまは長尺だけに刷れます", s.WidthMM, s.LengthMM)
	}
	t, ok := tapes[s.WidthMM]
	if !ok {
		return tape{}, fmt.Errorf("%dmm のロールの決まりを知りません", s.WidthMM)
	}
	return t, nil
}
