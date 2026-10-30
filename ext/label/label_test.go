package label

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"strings"
	"testing"
)

// TestRotatedClockwise は、横長の札を時計回りに 90 度回して送ることを固定します——札の左端（x=0）が先に出る行、
// 札の下端（y=H-1）が行の頭の点です。
func TestRotatedClockwise(t *testing.T) {
	b := Bitmap{W: 5, H: 3, Dark: make([]bool, 15)}
	b.Dark[0*5+0] = true // 左上
	b.Dark[2*5+4] = true // 右下
	w, h, bits := b.rotated()
	if w != 3 || h != 5 {
		t.Fatalf("回した大きさ: %dx%d", w, h)
	}
	on := func(u, r int) bool { return bits[r*1+u>>3]&(0x80>>(u&7)) != 0 }
	if !on(2, 0) {
		t.Error("左上の点は、1行目の末尾（u=2）のはず")
	}
	if !on(0, 4) {
		t.Error("右下の点は、最後の行の頭（u=0）のはず")
	}
}

func TestToBitmapThresholdAndHeight(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, Dots62+1)) // mutool の丸めで1点多い
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < 4; x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	img.SetGray(1, 2, color.Gray{Y: 40})
	img.SetGray(2, 2, color.Gray{Y: 200})
	b, err := toBitmap(img, Dots62)
	if err != nil {
		t.Fatal(err)
	}
	if b.H != Dots62 || b.W != 4 {
		t.Fatalf("大きさ: %dx%d", b.W, b.H)
	}
	if !b.at(1, 2) || b.at(2, 2) {
		t.Error("暗い点だけが黒のはず")
	}
	if _, err := toBitmap(image.NewGray(image.Rect(0, 0, 4, 300)), Dots62); err == nil {
		t.Error("高さの違う絵は断るはず")
	}
}

func TestPrintSendsRotatedPages(t *testing.T) {
	old, oldAvail := call, Available
	defer func() { call, Available = old, oldAvail }()
	Available = func() bool { return true }
	var got struct {
		Pages []struct {
			Width, Height int
			Bits          string
		}
	}
	call = func(_ context.Context, method, path string, body any) (int, []byte, error) {
		if method != http.MethodPost || path != "/print" {
			t.Fatalf("頼み先: %s %s", method, path)
		}
		b, _ := json.Marshal(body)
		_ = json.Unmarshal(b, &got)
		return 200, []byte(`{"ok":true,"printed":1}`), nil
	}
	b := Bitmap{W: 10, H: Dots62, Dark: make([]bool, 10*Dots62)}
	n, err := Print(context.Background(), []Bitmap{b})
	if err != nil || n != 1 {
		t.Fatalf("刷れるはず: %d %v", n, err)
	}
	if len(got.Pages) != 1 || got.Pages[0].Width != Dots62 || got.Pages[0].Height != 10 {
		t.Fatalf("送った札: %+v", got.Pages)
	}
	if bits, _ := base64.StdEncoding.DecodeString(got.Pages[0].Bits); len(bits) != 87*10 {
		t.Errorf("絵の大きさ: %d", len(bits))
	}

	call = func(context.Context, string, string, any) (int, []byte, error) {
		return 503, []byte(`{"ok":false,"printed":0,"error":"QL-800 の電源が切れているか、USB が抜けています"}`), nil
	}
	if _, err := Print(context.Background(), []Bitmap{b}); err == nil || !strings.Contains(err.Error(), "電源") {
		t.Errorf("道具の断りをそのまま返すはず: %v", err)
	}
}

func TestUnavailableWithoutSocket(t *testing.T) {
	t.Setenv("WCMS_LABEL_SOCK", "")
	if Available() {
		t.Fatal("ソケットの場所が無ければ刷れないはず")
	}
	if _, err := Print(context.Background(), nil); err != ErrUnavailable {
		t.Errorf("道具が無いときの誤り: %v", err)
	}
}
