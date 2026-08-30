package cms

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// 画像添付の入口検査と無害化のテスト（要件定義書 §2.6）。
//
// 拡張子は名乗りにすぎないので中身のマジックナンバーで判定し、EXIF は保存時に
// 除去する（カメラ写真の GPS が公開サイトに載ると撮影者の所在が漏れる）。
// SVG はスクリプトを内包できるので、入口でも拒否条件を掛ける（本体の守りは配信側）。

// pngChunk は型とデータから PNG のチャンク（長さ・型・データ・CRC）を組み立てます。
// テストが素材を作るのに使います（CRC-32/ISO-HDLC は crc32.ChecksumIEEE と同じもの）。
func pngChunk(typ string, data []byte) []byte {
	buf := make([]byte, 0, 12+len(data))
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	buf = append(buf, l[:]...)
	buf = append(buf, typ...)
	buf = append(buf, data...)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
	return append(buf, c[:]...)
}

// pngBytes は w×h の PNG を作ります。
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("PNG生成エラー: %v", err)
	}
	return buf.Bytes()
}

// jpegBytes は w×h の JPEG を作ります（EXIF なし）。
func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("JPEG生成エラー: %v", err)
	}
	return buf.Bytes()
}

// gifBytes は 2×2 の GIF を作ります。
func gifBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("GIF生成エラー: %v", err)
	}
	return buf.Bytes()
}

// jpegWithEXIF は JPEG の SOI 直後へ APP1（Exif）を差し込みます。
// orientation は EXIF の Orientation タグ（1=そのまま・6=時計回り90度）。
func jpegWithEXIF(t *testing.T, base []byte, orientation uint16) []byte {
	t.Helper()
	// TIFF ヘッダ（ビッグエンディアン）＋ IFD0（1エントリ: Orientation）＋ 次IFDなし
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // ヘッダ: MM / 42 / IFD0 オフセット8
		0x00, 0x01, // エントリ数 1
		0x01, 0x12, // タグ 0x0112 = Orientation
		0x00, 0x03, // 型 SHORT
		0x00, 0x00, 0x00, 0x01, // 個数 1
		byte(orientation >> 8), byte(orientation), 0x00, 0x00, // 値（左詰め）
		0x00, 0x00, 0x00, 0x00, // 次IFDなし
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	size := len(payload) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(size >> 8), byte(size)}, payload...)

	out := append([]byte{}, base[:2]...) // SOI
	out = append(out, app1...)
	out = append(out, base[2:]...)
	return out
}

// TestSniffImageKind は「拡張子ではなく中身で種別を決める」規則を固定します。
func TestSniffImageKind(t *testing.T) {
	webp := append([]byte("RIFF"), 0x1a, 0x00, 0x00, 0x00)
	webp = append(webp, []byte("WEBPVP8 ")...)
	// HEIC: ftyp ボックスのブランドが heic
	heic := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypheic")...)

	cases := []struct {
		name    string
		content []byte
		want    string
	}{
		{"PNG", pngBytes(t, 2, 2), "png"},
		{"JPEG", jpegBytes(t, 2, 2), "jpeg"},
		{"GIF", gifBytes(t), "gif"},
		{"WebP", webp, "webp"},
		{"SVG", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), "svg"},
		{"HEIC", heic, "heic"},
		{"PDFは画像ではない", []byte("%PDF-1.4\n"), ""},
		{"HTMLは画像ではない", []byte("<html><body>x</body></html>"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SniffImageKind(c.content); got != c.want {
				t.Errorf("SniffImageKind = %q, want %q", got, c.want)
			}
		})
	}
}

// TestValidateSVG は SVG の入口検査（多層防御の網）を固定します。
func TestValidateSVG(t *testing.T) {
	ok := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`)
	if err := ValidateSVG(ok); err != nil {
		t.Errorf("正当なSVGが拒否されました: %v", err)
	}

	bad := map[string]string{
		"script":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"foreignObject": `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><b>x</b></foreignObject></svg>`,
		"onイベント":        `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><rect/></svg>`,
		"javascriptURL": `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><rect/></a></svg>`,
		"ルートがsvgでない":    `<html><body/></html>`,
		"整形式でない":        `<svg><rect>`,
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSVG([]byte(src)); err == nil {
				t.Errorf("危険なSVGが通ってしまいました")
			}
		})
	}
}

// TestStripImageMetadataRemovesEXIF は、EXIF が保存前に消えることを検証します。
func TestStripImageMetadataRemovesEXIF(t *testing.T) {
	src := jpegWithEXIF(t, jpegBytes(t, 8, 4), 1)
	if !bytes.Contains(src, []byte("Exif\x00\x00")) {
		t.Fatal("テスト素材にEXIFが入っていません")
	}
	out, err := StripImageMetadata("jpeg", src)
	if err != nil {
		t.Fatalf("StripImageMetadataエラー: %v", err)
	}
	if bytes.Contains(out, []byte("Exif\x00\x00")) {
		t.Error("EXIFが残っています")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("除去後のJPEGが壊れています: %v", err)
	}
}

// TestStripImageMetadataAppliesOrientation は、回転情報を捨てる前に画素へ反映する
// ことを検証します（捨てるだけだと表示が横倒しになる）。
func TestStripImageMetadataAppliesOrientation(t *testing.T) {
	src := jpegWithEXIF(t, jpegBytes(t, 8, 4), 6) // 6 = 時計回りに90度回して表示
	out, err := StripImageMetadata("jpeg", src)
	if err != nil {
		t.Fatalf("StripImageMetadataエラー: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("除去後のJPEGを読めません: %v", err)
	}
	if cfg.Width != 4 || cfg.Height != 8 {
		t.Errorf("向きが補正されていません: %dx%d（8x4 を90度回して 4x8 になるはず）", cfg.Width, cfg.Height)
	}
}

// TestStripImageMetadataRemovesPNGText は PNG のメタデータ塊が落ちることを検証します。
func TestStripImageMetadataRemovesPNGText(t *testing.T) {
	base := pngBytes(t, 4, 4)
	// IHDR の直後へ tEXt チャンクを差し込む（PNG 署名8 + IHDR(4+4+13+4)=25）
	at := 8 + 25
	text := pngChunk("tEXt", []byte("Comment\x00ひみつ"))
	src := append(append(append([]byte{}, base[:at]...), text...), base[at:]...)
	if !bytes.Contains(src, []byte("tEXt")) {
		t.Fatal("テスト素材に tEXt が入っていません")
	}

	out, err := StripImageMetadata("png", src)
	if err != nil {
		t.Fatalf("StripImageMetadataエラー: %v", err)
	}
	if bytes.Contains(out, []byte("tEXt")) {
		t.Error("PNGのメタデータが残っています")
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("除去後のPNGが壊れています: %v", err)
	}
}
