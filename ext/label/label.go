// Package label は、ラベルプリンターで札を刷る拡張です（2026-10-30）。業務の言葉は持ちません。
//
// > 2026-10-30 利用者（【考察】現品票の印刷 §6 の問い1）: プリンターへ送る部分は「別の拡張 ext/label」
//
// 札の中身は、使う拡張が PDF で作って渡します（1ページが1枚の札）。ここは PDF を白黒の絵にし、業務サーバーのプリンターの道具
// （cmd/w-cms-label）へ Unix ソケットで渡すだけです。
//
//   - 道具のソケットは環境変数 WCMS_LABEL_SOCK です。無ければ「刷れない」と答えます（Available）。
//   - PDF を絵にするのは、PDF を絵にして見せる道具と同じ mutool です（設定 `pdf_pages` の tool・無ければ `mutool`）。
//   - 札は横長で作ります（長さ × ロールの幅）。2026-10-30 利用者:「現品票は上下62ミリで横に長く使っています。大きな文字で印刷しています」
//     ——PDF のページの高さがロールの印字の幅（62mm のロールなら 696 点＝約 58.9mm）、幅が札の長さです。刷るときに 90 度回して送ります。
//
// ⚠ w-cms は守りのため機械の部品に触れません（deploy/server/w-cms.service の PrivateDevices）。プリンターの口を持つのは道具だけです。
package label

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"w-cms/internal/cms"
)

// Dots62 は 62mm の長尺の印字の幅（点・300dpi）です（手順書の表）。
const Dots62 = 696

// DPI は札の解像度です。
const DPI = 300

// HeightPt は、印字の幅 dots を PDF の高さ（ポイント）にします。
func HeightPt(dots int) float64 { return float64(dots) * 72 / DPI }

// maxLengthDots は札の長さの上限です（道具と同じ・約 254mm）。
const maxLengthDots = 3000

// sock は道具のソケットの場所です（空なら刷れない）。
func sock() string { return os.Getenv("WCMS_LABEL_SOCK") }

// Available は、プリンターの道具に頼めるか（ソケットの場所が決まっていて、そこにソケットがあるか）です。試験で差し替える。
var Available = func() bool {
	s := sock()
	if s == "" {
		return false
	}
	st, err := os.Stat(s)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// State はプリンターのいまの様子です。
type State struct {
	Connected bool   `json:"connected"`
	WidthMM   int    `json:"width_mm,omitempty"`
	Dots      int    `json:"dots,omitempty"`
	Error     string `json:"error,omitempty"`
}

// call は道具に頼みます。試験で差し替える。
var call = func(ctx context.Context, method, path string, body any) (int, []byte, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock())
	}}}
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://label"+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		return 0, nil, err
	}
	return res.StatusCode, buf.Bytes(), nil
}

// ErrUnavailable は、プリンターの道具が動いていないときの誤りです。
var ErrUnavailable = errors.New("ラベルプリンターの道具が動いていません")

// GetState はプリンターの様子を道具に聞きます。
func GetState(ctx context.Context) (State, error) {
	if !Available() {
		return State{}, ErrUnavailable
	}
	code, b, err := call(ctx, http.MethodGet, "/state", nil)
	if err != nil {
		return State{}, fmt.Errorf("ラベルプリンターの道具に聞けませんでした: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil || code != http.StatusOK {
		return State{}, fmt.Errorf("ラベルプリンターの道具の答えが読めません（%d）", code)
	}
	return s, nil
}

// Bitmap は横長の札の白黒の絵です（幅＝札の長さ・高さ＝ロールの印字の幅）。Dark は y*W+x。
type Bitmap struct {
	W, H int
	Dark []bool
}

func (b Bitmap) at(x, y int) bool { return b.Dark[y*b.W+x] }

// PNG は絵を PNG にします（見本に使う——刷るのと同じ白黒）。
func (b Bitmap) PNG() ([]byte, error) {
	img := image.NewGray(image.Rect(0, 0, b.W, b.H))
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			c := uint8(255)
			if b.at(x, y) {
				c = 0
			}
			img.SetGray(x, y, color.Gray{Y: c})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// rotated は横長の絵を、道具が受ける形（1行がロールの幅・行が送る向き）の詰めたビットにします。
// 時計回りに 90 度——札の左端から先に出る。行 r は絵の x、行の中の点 u は絵の下から数えた y（u = H-1-y）。
func (b Bitmap) rotated() (width, height int, bits []byte) {
	width, height = b.H, b.W
	rb := (width + 7) / 8
	bits = make([]byte, rb*height)
	for r := 0; r < height; r++ {
		for u := 0; u < width; u++ {
			if b.at(r, b.H-1-u) {
				bits[r*rb+u>>3] |= 0x80 >> (u & 7)
			}
		}
	}
	return width, height, bits
}

// mutoolPath は PDF を絵にする道具です（設定 pdf_pages の tool・無ければ mutool）。
func mutoolPath() string {
	if c := cms.PDFPagesConfig(); c != nil && c.Tool != "" {
		return c.Tool
	}
	return "mutool"
}

// Render は PDF の各ページを、横長の札の絵にします（高さを dots にそろえる）。
func Render(ctx context.Context, pdf []byte, dots int) ([]Bitmap, error) {
	dir, err := os.MkdirTemp("", "w-cms-label-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, mutoolPath(), "draw", "-q", "-r", strconv.Itoa(DPI), "-c", "gray", "-F", "png",
		"-o", filepath.Join(dir, "p%d.png"), in)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("札を絵にできません（mutool）: %v %s", err, firstLine(stderr.String()))
	}
	var out []Bitmap
	for i := 1; ; i++ {
		f, err := os.Open(filepath.Join(dir, "p"+strconv.Itoa(i)+".png"))
		if err != nil {
			break
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		bm, err := toBitmap(img, dots)
		if err != nil {
			return nil, fmt.Errorf("%d 枚目: %w", i, err)
		}
		out = append(out, bm)
	}
	if len(out) == 0 {
		return nil, errors.New("札が1枚もありません")
	}
	return out, nil
}

// toBitmap は灰色の絵を白黒にします（半分より暗い点を黒）。高さは dots にそろえる（mutool の丸めで1点ずれることがある）。
func toBitmap(img image.Image, dots int) (Bitmap, error) {
	r := img.Bounds()
	if d := r.Dy() - dots; d > 2 || d < -2 {
		return Bitmap{}, fmt.Errorf("札の高さが %d 点です（ロールの印字の幅は %d 点）", r.Dy(), dots)
	}
	if r.Dx() < 1 || r.Dx() > maxLengthDots {
		return Bitmap{}, fmt.Errorf("札の長さが %d 点です（1〜%d）", r.Dx(), maxLengthDots)
	}
	b := Bitmap{W: r.Dx(), H: dots, Dark: make([]bool, r.Dx()*dots)}
	for y := 0; y < dots && y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			g := color.GrayModel.Convert(img.At(r.Min.X+x, r.Min.Y+y)).(color.Gray)
			b.Dark[y*b.W+x] = g.Y < 128
		}
	}
	return b, nil
}

// Print は札を刷ります。刷れた枚数を返します（途中で止まれば、そこまでの数と誤り）。
func Print(ctx context.Context, labels []Bitmap) (int, error) {
	if !Available() {
		return 0, ErrUnavailable
	}
	type pg struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Bits   string `json:"bits"`
	}
	req := struct {
		Pages []pg `json:"pages"`
	}{}
	for _, l := range labels {
		w, h, bits := l.rotated()
		req.Pages = append(req.Pages, pg{Width: w, Height: h, Bits: base64.StdEncoding.EncodeToString(bits)})
	}
	code, b, err := call(ctx, http.MethodPost, "/print", req)
	if err != nil {
		return 0, fmt.Errorf("ラベルプリンターの道具に頼めませんでした: %w", err)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Printed int    `json:"printed"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return 0, fmt.Errorf("ラベルプリンターの道具の答えが読めません（%d）", code)
	}
	if code != http.StatusOK || !out.OK {
		msg := out.Error
		if msg == "" {
			msg = http.StatusText(code)
		}
		return out.Printed, errors.New(msg)
	}
	return out.Printed, nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
