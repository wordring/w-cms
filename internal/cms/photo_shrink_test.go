package cms

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// noisyJPEG は w×h のざらついた写真（縮めると確かに小さくなる）を返します。
func noisyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(x*255/w) ^ uint8(rng.Intn(64))
			img.Set(x, y, color.RGBA{v, uint8(y * 255 / h), v / 2, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func putAttachment(t *testing.T, pageID, name string, content []byte) string {
	t.Helper()
	dir := page.AttachmentDir(pageID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func jpegDims(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("JPEG として読めません: %v", err)
	}
	return cfg.Width, cfg.Height
}

func postPhotoShrink(user, ref string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/photo-shrink", strings.NewReader(`{"ref":"`+ref+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, &auth.User{Username: user})
	rr := httptest.NewRecorder()
	PhotoShrinkAPIHandler(rr, req)
	return rr
}

// TestReencodeJPEGShrinksOnlyLongPhotos は、長い辺が設定より長い写真だけを縮め、縦横の比を保つことを固定します（2026-10-27）。
func TestReencodeJPEGShrinksOnlyLongPhotos(t *testing.T) {
	big := noisyJPEG(t, 3000, 1800)
	out, before, after, resized, err := reencodeJPEG(big, 2000, 85)
	if err != nil || !resized {
		t.Fatalf("縮めていません: resized=%v err=%v", resized, err)
	}
	if before != (image.Point{3000, 1800}) || after != (image.Point{2000, 1200}) {
		t.Errorf("大きさが違います: %v → %v", before, after)
	}
	if w, h := jpegDims(t, out); w != 2000 || h != 1200 {
		t.Errorf("書き出した写真の大きさが違います: %d×%d", w, h)
	}
	if len(out) >= len(big) {
		t.Errorf("小さくなっていません: %d → %d", len(big), len(out))
	}
	// 縦長でも長い辺で縮める。
	_, _, after, _, _ = reencodeJPEG(noisyJPEG(t, 900, 2400), 2000, 85)
	if after != (image.Point{750, 2000}) {
		t.Errorf("縦長の写真の大きさが違います: %v", after)
	}
	// もとから短い写真は縮めない（メタデータを落とすための作り直しだけ）。
	if _, _, _, resized, err := reencodeJPEG(noisyJPEG(t, 1600, 1200), 2000, 85); err != nil || resized {
		t.Errorf("短い写真を縮めました: resized=%v err=%v", resized, err)
	}
}

// TestPhotoShrinkReplacesAttachmentAndKeepsOriginalFacts は、写真ごとの「🗜 写真を小さくする」が
//   - 写真を縮めて同じ名前で上書きし、目録に縮める前の大きさ・指紋・画素を残す（指紋で元の写真を見分ける道具のため）
//   - 2回目は「もう小さい」で、元の事実を上書きしない
//   - 書けない人・届いた事実を残す範囲（attachment_readonly）・JPEG でないもの・設定が無いときは断る
//
// ことを固定します（2026-10-27）。
func TestPhotoShrinkReplacesAttachmentAndKeepsOriginalFacts(t *testing.T) {
	setupAttachmentTest(t)
	withSettings(t, func(s *Settings) { s.PhotoShrink = &PhotoShrinkSettings{LongSide: 2000, Quality: 85} })
	orig := noisyJPEG(t, 3000, 1800)
	path := putAttachment(t, "000101", "ph01.jpg", orig)
	if err := RecordAttachmentMeta("000101", "ph01.jpg", NewAttachmentMeta("現場.jpg", "alice", AttachmentSourceUpload, orig)); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(orig)

	rr := postPhotoShrink("alice", "000101-ph01")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"to":"2000×1200"`) {
		t.Fatalf("縮められません: %d %s", rr.Code, rr.Body.String())
	}
	now, _ := os.ReadFile(path)
	if w, _ := jpegDims(t, now); w != 2000 || len(now) >= len(orig) {
		t.Errorf("上書きされていません: 幅 %d・%d バイト", w, len(now))
	}
	m, _ := AttachmentMetaOf("000101", "ph01.jpg")
	nowSum := sha256.Sum256(now)
	if m.ShrunkFrom == nil || m.ShrunkFrom.SHA256 != hex.EncodeToString(sum[:]) || m.ShrunkFrom.Size != int64(len(orig)) ||
		m.ShrunkFrom.Width != 3000 || m.ShrunkFrom.By != "alice" {
		t.Errorf("縮める前の事実が目録にありません: %+v", m.ShrunkFrom)
	}
	if m.SHA256 != hex.EncodeToString(nowSum[:]) || m.Size != int64(len(now)) || m.Name != "現場.jpg" || m.Source != AttachmentSourceUpload {
		t.Errorf("目録の今の事実が違います: %+v", m)
	}
	// 2回目は「もう小さい」（中身も目録も変えない）。
	if rr := postPhotoShrink("alice", "000101-ph01"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "もう小さい") {
		t.Errorf("2回目を断っていません: %d %s", rr.Code, rr.Body.String())
	}
	if m2, _ := AttachmentMetaOf("000101", "ph01.jpg"); m2.ShrunkFrom == nil || m2.ShrunkFrom.SHA256 != m.ShrunkFrom.SHA256 {
		t.Errorf("元の事実が変わりました: %+v", m2.ShrunkFrom)
	}

	// 読めるが書けない人（bob は「読むだけ」を読める）は断る。
	putAttachment(t, "000104", "ph02.jpg", orig)
	if rr := postPhotoShrink("bob", "000104-ph02"); rr.Code != http.StatusForbidden {
		t.Errorf("書けない人を断っていません: %d %s", rr.Code, rr.Body.String())
	}
	// 届いた事実を残す範囲は断る。
	putAttachment(t, "000101", "ph03.jpg", orig)
	withSettings(t, func(s *Settings) { s.AttachmentReadOnly = []string{"部品A"} })
	if rr := postPhotoShrink("alice", "000101-ph03"); rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "attachment_readonly") {
		t.Errorf("読み取り専用の範囲を断っていません: %d %s", rr.Code, rr.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(page.AttachmentDir("000101"), "ph03.jpg")); !bytes.Equal(b, orig) {
		t.Error("読み取り専用の範囲の写真が書き換わりました")
	}
	withSettings(t, func(s *Settings) { s.AttachmentReadOnly = nil })
	// JPEG でないものは断る。
	if rr := postPhotoShrink("alice", "000101-a1b2"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "JPEG") {
		t.Errorf("JPEG でないものを断っていません: %d %s", rr.Code, rr.Body.String())
	}
	// 設定が無ければ縮めない。
	withSettings(t, func(s *Settings) { s.PhotoShrink = nil })
	if rr := postPhotoShrink("alice", "000101-ph03"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "photo_shrink") {
		t.Errorf("設定が無いのに縮めました: %d %s", rr.Code, rr.Body.String())
	}
}

// TestPhotoShrinkAllCountsAndShrinks は、管理画面のまとめて縮めるが、書き換えてよい範囲の長い JPEG だけを数えて縮めることを
// 固定します（2026-10-27）。
func TestPhotoShrinkAllCountsAndShrinks(t *testing.T) {
	setupAttachmentTest(t)
	withSettings(t, func(s *Settings) {
		s.PhotoShrink = &PhotoShrinkSettings{LongSide: 2000, Quality: 85}
		s.AttachmentReadOnly = []string{"RE: 見積り"}
	})
	big := noisyJPEG(t, 2600, 1400)
	putAttachment(t, "000101", "pa01.jpg", big)
	putAttachment(t, "000101", "pa02.jpg", noisyJPEG(t, 1200, 900)) // もとから短い
	putAttachment(t, "000102", "pa03.jpg", big)                     // 届いた事実を残す範囲
	putAttachment(t, "000104", "pa04.jpeg", big)

	list, err := photoShrinkCandidates(2000)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range list {
		got = append(got, c.PageID+"/"+c.Stored)
	}
	if strings.Join(got, ",") != "000101/pa01.jpg,000104/pa04.jpeg" {
		t.Fatalf("数える写真が違います: %v", got)
	}

	admin := &auth.User{Username: "root", IsAdmin: true}
	if !startPhotoShrinkAll(admin, list) {
		t.Fatal("始められません")
	}
	waitPhotoJob(t)
	photoJobMu.Lock()
	job := photoJob
	photoJobMu.Unlock()
	if job.Shrunk != 2 || job.Failed != 0 || job.After >= job.Before {
		t.Errorf("縮めた数が違います: %+v", job)
	}
	if again, _ := photoShrinkCandidates(2000); len(again) != 0 {
		t.Errorf("縮めたあとも数えています: %v", again)
	}
	if b, _ := os.ReadFile(filepath.Join(page.AttachmentDir("000102"), "pa03.jpg")); !bytes.Equal(b, big) {
		t.Error("届いた事実を残す範囲の写真が書き換わりました")
	}
}

func waitPhotoJob(t *testing.T) {
	t.Helper()
	for i := 0; i < 600; i++ {
		photoJobMu.Lock()
		running := photoJob.Running
		photoJobMu.Unlock()
		if !running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("まとめて縮める仕事が終わりません")
}

// TestUploadImageShrinksLongPhotos は、これから上げる写真を保存するときに自動で縮めることを固定します（2026-10-27）。
func TestUploadImageShrinksLongPhotos(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	withSettings(t, func(s *Settings) { s.PhotoShrink = &PhotoShrinkSettings{LongSide: 2000, Quality: 85} })
	alice := &auth.User{Username: "alice"}
	orig := noisyJPEG(t, 2400, 1800)
	rr := postImage(t, id, "現場.jpg", orig, alice)
	if rr.Code != http.StatusOK {
		t.Fatalf("上げられません: %d %s", rr.Code, rr.Body.String())
	}
	entries, _ := os.ReadDir(page.AttachmentDir(id))
	var stored string
	for _, e := range entries {
		if isPhotoName(e.Name()) {
			stored = e.Name()
		}
	}
	b, _ := os.ReadFile(filepath.Join(page.AttachmentDir(id), stored))
	if w, h := jpegDims(t, b); w != 2000 || h != 1500 {
		t.Errorf("上げた写真が縮んでいません: %d×%d", w, h)
	}
	m, _ := AttachmentMetaOf(id, stored)
	sum := sha256.Sum256(orig)
	if m.ShrunkFrom == nil || m.ShrunkFrom.SHA256 != hex.EncodeToString(sum[:]) || m.ShrunkFrom.Width != 2400 {
		t.Errorf("縮める前の事実が目録にありません: %+v", m.ShrunkFrom)
	}
	// 設定が無ければ縮めない（メタデータを落とすだけ）。
	withSettings(t, func(s *Settings) { s.PhotoShrink = nil })
	rr = postImage(t, id, "現場2.jpg", orig, alice)
	if rr.Code != http.StatusOK {
		t.Fatalf("上げられません: %d %s", rr.Code, rr.Body.String())
	}
	entries, _ = os.ReadDir(page.AttachmentDir(id))
	found := false
	for _, e := range entries {
		if isPhotoName(e.Name()) && e.Name() != stored {
			b, _ := os.ReadFile(filepath.Join(page.AttachmentDir(id), e.Name()))
			if w, _ := jpegDims(t, b); w != 2400 {
				t.Errorf("設定が無いのに縮めました: 幅 %d", w)
			}
			found = true
		}
	}
	if !found {
		t.Error("2枚目が保存されていません")
	}
}
