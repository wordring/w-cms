package cms

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// quadPNG は左上が赤・右上が緑・左下が青・右下が白の w×h の PNG です（回した向きを色で確かめる）。
func quadPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			switch {
			case x >= w/2 && y < h/2:
				c = color.NRGBA{0, 255, 0, 255}
			case x < w/2 && y >= h/2:
				c = color.NRGBA{0, 0, 255, 255}
			case x >= w/2 && y >= h/2:
				c = color.NRGBA{255, 255, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("PNG として読めません: %v", err)
	}
	return img
}

func redOf(img image.Image, x, y int) (r, g, b uint32) {
	r, g, b, _ = img.At(x, y).RGBA()
	return r >> 8, g >> 8, b >> 8
}

// TestApplyPhotoEditRotates は、右へ回すと左上の色が右上へ、左へ回すと左上の色が左下へ行くことを固定します。
func TestApplyPhotoEditRotates(t *testing.T) {
	src := quadPNG(t, 40, 20)
	out, size, err := applyPhotoEdit(src, photoEditOp{Op: "rotate-right"})
	if err != nil || size != (image.Point{20, 40}) {
		t.Fatalf("右へ回せません: size=%v err=%v", size, err)
	}
	if r, g, b := redOf(decodePNG(t, out), 15, 5); r != 255 || g != 0 || b != 0 {
		t.Errorf("右へ回すと、左上の赤は右上へ行くはず: %d %d %d", r, g, b)
	}
	out, size, err = applyPhotoEdit(src, photoEditOp{Op: "rotate-left"})
	if err != nil || size != (image.Point{20, 40}) {
		t.Fatalf("左へ回せません: size=%v err=%v", size, err)
	}
	if r, g, b := redOf(decodePNG(t, out), 5, 35); r != 255 || g != 0 || b != 0 {
		t.Errorf("左へ回すと、左上の赤は左下へ行くはず: %d %d %d", r, g, b)
	}
}

// TestApplyPhotoEditCrop は、割合の範囲を切り抜き、はみ出しは丸め、小さすぎる範囲は断ることを固定します。
func TestApplyPhotoEditCrop(t *testing.T) {
	src := quadPNG(t, 100, 100)
	out, size, err := applyPhotoEdit(src, photoEditOp{Op: "crop", X: 0.5, Y: 0.5, W: 0.6, H: 0.25})
	if err != nil || size != (image.Point{50, 25}) {
		t.Fatalf("切り抜けません: size=%v err=%v", size, err)
	}
	if r, g, b := redOf(decodePNG(t, out), 0, 0); r != 255 || g != 255 || b != 255 {
		t.Errorf("右下の白を切り抜いたはず: %d %d %d", r, g, b)
	}
	for _, bad := range []photoEditOp{
		{Op: "crop", X: 0.5, Y: 0.5, W: 0.01, H: 0.5},
		{Op: "crop", X: 1.5, Y: 0, W: 0.5, H: 0.5},
		{Op: "crop", X: 0, Y: 0, W: 0, H: 0},
	} {
		if _, _, err := applyPhotoEdit(src, bad); err != errPhotoEditCrop {
			t.Errorf("%+v は断るはず: %v", bad, err)
		}
	}
}

// TestApplyPhotoEditBrighten は、暗い所が明るくなり、白は白のままで、透明は変わらないことを固定します。
func TestApplyPhotoEditBrighten(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	img.SetNRGBA(0, 0, color.NRGBA{64, 64, 64, 255})
	img.SetNRGBA(1, 0, color.NRGBA{255, 255, 255, 255})
	img.SetNRGBA(2, 0, color.NRGBA{64, 64, 64, 100})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, _, err := applyPhotoEdit(buf.Bytes(), photoEditOp{Op: "brighten"})
	if err != nil {
		t.Fatal(err)
	}
	got := decodePNG(t, out).(*image.NRGBA)
	if c := got.NRGBAAt(0, 0); c.R <= 64 || c.A != 255 {
		t.Errorf("暗い所が明るくなっていません: %+v", c)
	}
	if c := got.NRGBAAt(1, 0); c.R != 255 {
		t.Errorf("白は白のままのはず: %+v", c)
	}
	if c := got.NRGBAAt(2, 0); c.A != 100 {
		t.Errorf("透明の度合いは変えないはず: %+v", c)
	}
}

// TestApplyPhotoEditKeepsJPEG は、JPEG は JPEG のまま書き出し、向きが変わることを固定します。
func TestApplyPhotoEditKeepsJPEG(t *testing.T) {
	out, _, err := applyPhotoEdit(noisyJPEG(t, 60, 30), photoEditOp{Op: "rotate-right"})
	if err != nil {
		t.Fatal(err)
	}
	if w, h := jpegDims(t, out); w != 30 || h != 60 {
		t.Errorf("大きさが違います: %d×%d", w, h)
	}
	if _, _, err := applyPhotoEdit([]byte("GIF89a..."), photoEditOp{Op: "brighten"}); err != errPhotoEditKind {
		t.Errorf("GIF は断るはず: %v", err)
	}
	if _, _, err := applyPhotoEdit(noisyJPEG(t, 60, 30), photoEditOp{Op: "消す"}); err != errPhotoEditOp {
		t.Errorf("知らない操作は断るはず: %v", err)
	}
}

// TestAttachmentVersionsOrder は、同じ秒の控えも書いた順の逆で並ぶことを固定します（名前のまま並べると `-1` が先に来る）。
func TestAttachmentVersionsOrder(t *testing.T) {
	setupAttachmentTest(t)
	pageID := "000101"
	dir := page.AttachmentDir(pageID) + string(os.PathSeparator) + attachmentVersionsDir + string(os.PathSeparator) + "a.jpg"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"20261030T010000Z.jpg", "20261030T010000Z-1.jpg", "20261030T010000Z-2.jpg", "20261030T010000Z-10.jpg", "20261030T005959Z.jpg", ".tmp123"}
	for _, n := range names {
		if err := os.WriteFile(dir+string(os.PathSeparator)+n, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(attachmentVersions(pageID, "a.jpg"), " ")
	want := "20261030T010000Z-10.jpg 20261030T010000Z-2.jpg 20261030T010000Z-1.jpg 20261030T010000Z.jpg 20261030T005959Z.jpg"
	if got != want {
		t.Errorf("並びが違います:\n got %s\nwant %s", got, want)
	}
}

func photoEditCall(user, method, body string) (*httptest.ResponseRecorder, map[string]any) {
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest("GET", "/api/photo-edit?ref="+body, nil)
	} else {
		req = httptest.NewRequest("POST", "/api/photo-edit", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req = auth.WithUser(req, &auth.User{Username: user})
	rr := httptest.NewRecorder()
	PhotoEditAPIHandler(rr, req)
	out := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out
}

func photoEditPost(t *testing.T, user, ref, v, rest string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return photoEditCall(user, "POST", `{"ref":"`+ref+`","v":"`+v+`",`+rest+`}`)
}

// TestPhotoEditAPISaveAndUndo は、押すたびに保存し、戻すと元の中身に1バイトも違わず戻ることを固定します。
func TestPhotoEditAPISaveAndUndo(t *testing.T) {
	setupAttachmentTest(t)
	original := noisyJPEG(t, 200, 100)
	path := putAttachment(t, "000101", "pe01.jpg", original)
	ref := "000101-pe01"

	_, st := photoEditCall("alice", http.MethodGet, ref)
	if st["can_edit"] != true || st["can_undo"] != false || st["v"] == "" {
		t.Fatalf("初めの様子が違います: %v", st)
	}
	rr, st := photoEditPost(t, "alice", ref, st["v"].(string), `"op":"rotate-right"`)
	if rr.Code != 200 || st["can_undo"] != true {
		t.Fatalf("回せません: %d %v", rr.Code, st)
	}
	now, _ := os.ReadFile(path)
	if w, h := jpegDims(t, now); w != 100 || h != 200 {
		t.Errorf("保存した画像が回っていません: %d×%d", w, h)
	}
	if m, _ := AttachmentMetaOf("000101", "pe01.jpg"); m.EditedFrom != sha256Hex(original) || m.EditedBy != "alice" || m.SHA256 != sha256Hex(now) {
		t.Errorf("目録が進んでいません: %+v", m)
	}
	// 版の印を変えるために時刻をずらす（同じミリ秒だと印が変わらない）。
	time.Sleep(3 * time.Millisecond)
	rr, st = photoEditPost(t, "alice", ref, st["v"].(string), `"op":"crop","x":0,"y":0,"w":0.5,"h":0.5`)
	if rr.Code != 200 {
		t.Fatalf("切り抜けません: %d %v", rr.Code, st)
	}
	time.Sleep(3 * time.Millisecond)
	_, st = photoEditPost(t, "alice", ref, st["v"].(string), `"op":"undo"`)
	time.Sleep(3 * time.Millisecond)
	back1, _ := os.ReadFile(path)
	if !bytes.Equal(back1, now) {
		t.Errorf("1回戻すと、回したあとの中身に戻るはず")
	}
	rr, st = photoEditPost(t, "alice", ref, st["v"].(string), `"op":"undo"`)
	if rr.Code != 200 || st["can_undo"] != false {
		t.Fatalf("2回目を戻せません: %d %v", rr.Code, st)
	}
	back2, _ := os.ReadFile(path)
	if !bytes.Equal(back2, original) {
		t.Errorf("元の中身に戻っていません")
	}
	if m, _ := AttachmentMetaOf("000101", "pe01.jpg"); m.EditedFrom != "" || m.SHA256 != sha256Hex(original) {
		t.Errorf("編集の印が消えていません: %+v", m)
	}
	rr, _ = photoEditPost(t, "alice", ref, st["v"].(string), `"op":"undo"`)
	if rr.Code != http.StatusConflict {
		t.Errorf("控えが無いときは断るはず: %d", rr.Code)
	}
}

// TestPhotoEditAPIRefuses は、古い版の印・書けない人・読み取り専用の範囲・編集できない形式を断ることを固定します。
func TestPhotoEditAPIRefuses(t *testing.T) {
	setupAttachmentTest(t)
	content := noisyJPEG(t, 100, 100)
	path := putAttachment(t, "000101", "pe01.jpg", content)
	putAttachment(t, "000101", "pe02.webp", []byte("RIFF....WEBP"))
	ref := "000101-pe01"
	_, st := photoEditCall("alice", http.MethodGet, ref)

	rr, got := photoEditPost(t, "alice", ref, "ふるい", `"op":"brighten"`)
	if rr.Code != http.StatusConflict || got["v"] != st["v"] {
		t.Errorf("古い版の印は断り、いまの印を返すはず: %d %v", rr.Code, got)
	}
	putAttachment(t, "000104", "pe03.jpg", content)
	_, st4 := photoEditCall("alice", http.MethodGet, "000104-pe03")
	rr, _ = photoEditPost(t, "bob", "000104-pe03", st4["v"].(string), `"op":"brighten"`)
	if rr.Code != http.StatusForbidden {
		t.Errorf("書けない人は断るはず: %d", rr.Code)
	}
	if _, g := photoEditCall("bob", http.MethodGet, "000104-pe03"); g["can_edit"] != false {
		t.Errorf("書けない人には編集を出さないはず: %v", g)
	}
	if _, g := photoEditCall("alice", http.MethodGet, "000101-pe02"); g["can_edit"] != false {
		t.Errorf("WebP は編集を出さないはず: %v", g)
	}
	now, _ := os.ReadFile(path)
	if !bytes.Equal(now, content) {
		t.Errorf("断ったのに中身が変わりました")
	}
	if v := attachmentVersions("000101", "pe01.jpg"); len(v) != 0 {
		t.Errorf("断ったのに控えができました: %v", v)
	}

	// 届いた添付の範囲（設定 attachment_readonly）。
	withSettings(t, func(s *Settings) { s.AttachmentReadOnly = []string{"部品A"} })
	if _, g := photoEditCall("alice", http.MethodGet, ref); g["can_edit"] != false {
		t.Errorf("読み取り専用の範囲は編集を出さないはず: %v", g)
	}
	rr, _ = photoEditPost(t, "alice", ref, st["v"].(string), `"op":"brighten"`)
	if rr.Code != http.StatusForbidden {
		t.Errorf("読み取り専用の範囲は断るはず: %d", rr.Code)
	}
}
