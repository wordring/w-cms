package cms

import (
	"bytes"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 画像添付のアップロードと配信のテスト（要件定義書 §2.6）。
// PDF と同じ入口・出口の二層で守れていることを固定します。

// postImage は UploadImageHandler を multipart で叩きます（編集ロックは取り直す）。
func postImage(t *testing.T, pageID, fileName string, content []byte, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	idInt, _ := strconv.Atoi(pageID)
	editlock.Locks.ForceRelease(idInt)
	t.Cleanup(func() { editlock.Locks.ForceRelease(idInt) })
	a := editlock.Locks.TryAcquire(idInt, u.Username, "")
	if !a.Acquired {
		t.Fatalf("%s のロック取得に失敗", u.Username)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("page_id", pageID)
	part, err := mw.CreateFormFile("image_file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFileエラー: %v", err)
	}
	part.Write(content)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/upload-image", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Lock-Token", a.Token)
	req = auth.WithUser(req, u)

	rr := httptest.NewRecorder()
	UploadImageHandler(rr, req)
	return rr
}

// TestUploadImageAcceptsAllowedKinds は許可種別が保存され、本文から参照できる
// 絶対URLが返ることを検証します。
func TestUploadImageAcceptsAllowedKinds(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	alice := &auth.User{Username: "alice"}

	rr := postImage(t, id, "写真.png", pngBytes(t, 4, 4), alice)
	if rr.Code != 200 {
		t.Fatalf("PNGのアップロードに失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/data/master/00/000012/") {
		t.Errorf("本文から参照できるURLが返っていません: %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), "写真.png")); err != nil {
		t.Errorf("PNGが保存されていません: %v", err)
	}
}

// TestUploadImageRejectsHEIC は、iOS のカメラ写真が HEIC で届いたときに
// **理由が分かる形で**拒否されることを検証します（要件 §2.6）。
func TestUploadImageRejectsHEIC(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	heic := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 32)...)
	rr := postImage(t, id, "IMG_0001.heic", heic, &auth.User{Username: "alice"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("HEICが拒否されていません: code=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "HEIC") {
		t.Errorf("拒否の理由に形式名がありません: %s", rr.Body.String())
	}
}

// TestUploadImageRejectsExtensionLie は「.png という名前のJPEG」のように
// 名乗りと中身が食い違うファイルを拒否することを検証します。
func TestUploadImageRejectsExtensionLie(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	rr := postImage(t, id, "うそ.png", jpegBytes(t, 4, 4), &auth.User{Username: "alice"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("名乗りと中身の食い違いが通ってしまいました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), "うそ.png")); err == nil {
		t.Error("拒否したのにファイルが残っています")
	}
}

// TestUploadImageRejectsNonImage は画像でないファイルを拒否することを検証します。
func TestUploadImageRejectsNonImage(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	alice := &auth.User{Username: "alice"}

	if rr := postImage(t, id, "x.png", []byte("<html><b>x</b></html>"), alice); rr.Code != http.StatusBadRequest {
		t.Errorf("画像でないファイルが通ってしまいました: code=%d", rr.Code)
	}
	if rr := postImage(t, id, "x.pdf", []byte("%PDF-1.4\n"), alice); rr.Code != http.StatusBadRequest {
		t.Errorf("画像アップロード口でPDFが通ってしまいました: code=%d", rr.Code)
	}
}

// TestUploadImageStripsEXIF は、保存された正本にEXIFが残らないことを検証します。
// カメラ写真のGPSが公開サイトへ載る事故を、経路の一律処理で防ぐのが要件です。
func TestUploadImageStripsEXIF(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	src := jpegWithEXIF(t, jpegBytes(t, 8, 4), 6)
	if rr := postImage(t, id, "撮影.jpg", src, &auth.User{Username: "alice"}); rr.Code != 200 {
		t.Fatalf("JPEGのアップロードに失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(page.AttachmentDir(id), "撮影.jpg"))
	if err != nil {
		t.Fatalf("保存されたJPEGを読めません: %v", err)
	}
	if bytes.Contains(saved, []byte("Exif\x00\x00")) {
		t.Error("保存された正本にEXIFが残っています")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(saved))
	if err != nil {
		t.Fatalf("保存されたJPEGが壊れています: %v", err)
	}
	if cfg.Width != 4 || cfg.Height != 8 {
		t.Errorf("向きが補正されていません: %dx%d", cfg.Width, cfg.Height)
	}
}

// TestUploadImageRejectsDangerousSVG は、スクリプトを含むSVGを入口で拒否する
// ことを検証します（安全性の本体は配信側だが、これは多層防御の網）。
func TestUploadImageRejectsDangerousSVG(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	alice := &auth.User{Username: "alice"}

	bad := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if rr := postImage(t, id, "わな.svg", bad, alice); rr.Code != http.StatusBadRequest {
		t.Errorf("スクリプト入りのSVGが通ってしまいました: code=%d", rr.Code)
	}
	ok := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="4" height="4"/></svg>`)
	if rr := postImage(t, id, "図.svg", ok, alice); rr.Code != 200 {
		t.Errorf("正当なSVGが拒否されました: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUploadImageCannotEscapePageDir は、画像の口からも本文・サイドカーを
// 上書きできず、ページのフォルダの外へも書けないことを検証します
// （PDF の口と同じ守り。名前の検査は safeAttachmentName が1箇所で担う）。
func TestUploadImageCannotEscapePageDir(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})
	alice := &auth.User{Username: "alice"}

	for _, name := range []string{id + ".meta.json", id + ".html", "x.exe", ".hidden.png"} {
		if rr := postImage(t, id, name, pngBytes(t, 2, 2), alice); rr.Code == 200 {
			t.Errorf("%q が受け入れられてしまいました", name)
		}
	}

	// パス要素は「拒否」ではなく「落として正規化」する（PDF の口と同じ）。
	// 守りたいのは**ページのフォルダの外に書けないこと**なので、そこを見る。
	if rr := postImage(t, id, "../evil.png", pngBytes(t, 2, 2), alice); rr.Code != 200 {
		t.Fatalf("正規化されるはずの名前が拒否されました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), "evil.png")); err != nil {
		t.Errorf("正規化した名前でページのフォルダへ保存されていません: %v", err)
	}
	outside := filepath.Join(filepath.Dir(page.AttachmentDir(id)), "evil.png")
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("ページのフォルダの外へ書かれています: %s", outside)
	}
}

// TestUploadImageRequiresEditLock は、添付の追加が本文編集と同じロックで
// 直列化されることを検証します（PDF と同じ扱い）。
// ロックは誰も持っていなければ通る（日和見的）ので、**他人が保持している**状態を作る。
func TestUploadImageRequiresEditLock(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Group: "team", Mode: "330"})
	editlock.Locks.ForceRelease(12)
	t.Cleanup(func() { editlock.Locks.ForceRelease(12) })

	if a := editlock.Locks.TryAcquire(12, "alice", ""); !a.Acquired {
		t.Fatal("alice のロック取得に失敗")
	}

	// bob は同じ group で write を持つが、ロックは alice が保持している。
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("page_id", id)
	part, _ := mw.CreateFormFile("image_file", "a.png")
	part.Write(pngBytes(t, 2, 2))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/upload-image", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = auth.WithUser(req, &auth.User{Username: "bob", Groups: []string{"team"}})
	rr := httptest.NewRecorder()
	UploadImageHandler(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("他者ロック中のアップロードが 409 になりません: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDataFileServesImagesInline は、検証済みの画像が実際の種別で
// インライン配信されることを検証します（本文の <img> から見えるため）。
func TestDataFileServesImagesInline(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})
	dir := page.GetPageDir(id)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "a.png"), pngBytes(t, 2, 2), 0644)
	os.WriteFile(filepath.Join(dir, "b.jpg"), jpegBytes(t, 2, 2), 0644)

	user := &auth.User{Username: "alice"}
	base := "/data/master/00/" + id + "/"

	for name, want := range map[string]string{"a.png": "image/png", "b.jpg": "image/jpeg"} {
		rr := getData(t, base+name, user)
		if rr.Code != 200 {
			t.Fatalf("%s の配信に失敗: code=%d", name, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != want {
			t.Errorf("%s の Content-Type が違います: %q（期待 %q）", name, ct, want)
		}
		if d := rr.Header().Get("Content-Disposition"); d != "" {
			t.Errorf("%s がダウンロード扱いになっています: %q", name, d)
		}
		if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s に nosniff がありません", name)
		}
	}
}

// TestDataFileServesSVGDefused は、SVG が「不活性な画像」として配信される
// ことを検証します（要件 §2.6 の②）。直接開いても描画させず、万一描画されても
// 実行させない。<img> のサブリソース読み込みはこれらのヘッダを無視するので、
// 本文での表示は影響を受けません。
func TestDataFileServesSVGDefused(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})
	dir := page.GetPageDir(id)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "logo.svg"),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), 0644)

	rr := getData(t, "/data/master/00/"+id+"/logo.svg", &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("SVGの配信に失敗: code=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type が違います: %q", ct)
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Error("直接開いたときにダウンロードになりません")
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("この応答限定のCSPがありません: %q", csp)
	}
}
