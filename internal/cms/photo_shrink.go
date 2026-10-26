package cms

// ─────────────────────────────────────────────────────────────────────────
// 写真を小さくする（2026-10-27）
//
// 利用者:「では、添付の中で容量を取るファイル形式を調べ、ワンクリックで容量を小さくする方法を考えましょう」
// ——調べると、添付 6.1 GB のうち写真（JPEG）が 3.6 GB で、ほとんどが長い辺 4000 px 前後のスマホの写真でした。
// 長い辺を 2000 px にすると、大きさは元の約 25% になり、溶接の跡や穴の縁まで見分けられます。
// 置き場は3つ（利用者の答え）:
//
//   - 写真ごとの「⋯」の「🗜 写真を小さくする」（POST /api/photo-shrink）
//   - 管理画面でまとめて（photo_shrink_all.go）
//   - これから上げる写真は、保存するときに自動で（upload_image.go）
//
// 大きさは設定 `photo_shrink`（長い辺の画素 `long_side`・JPEG の品質 `quality`）。設定が無ければ縮めません。
//
// ── 守り ──
//
//   - **書き換えてよい添付だけ**（attachment_write.go の canWriteAttachment——ページへの書き込み権限と、
//     読み取り専用の範囲 `attachment_readonly` の外）。メールで届いた添付は届いた事実の証拠なので縮めません。
//   - **JPEG だけ**。PNG は図や画面の写しが多く、JPEG にすると名前（拡張子＝住所）が変わるので触りません。
//   - ⚠ **前の大きさは w-cms の中に残しません**（残すと容量が減らない）。「📝 ローカル編集」の上書きは前の中身を
//     `files/.versions/` に残しますが、ここは残しません。元の写真は、夜の控え（14日・12か月）と社外の控え
//     （VPS・HP405——消さない）にあります。
//   - 目録（meta.json）に、縮める前の大きさ・指紋・画素を `shrunk_from` で残します。中身の指紋で同じ写真かを
//     確かめる道具（ワンノートの添付を足す道具など）が、縮めた写真を元の写真と見分けられるように。
//   - 縮めても小さくならない写真（もとから小さく作ってある）は、そのままにします。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// PhotoShrinkSettings は設定 `photo_shrink` です。
type PhotoShrinkSettings struct {
	// LongSide は縮めたあとの長い辺の画素です。これより長い写真だけを縮めます。
	LongSide int `json:"long_side"`
	// Quality は縮めた写真の JPEG の品質です（1〜100）。
	Quality int `json:"quality"`
}

func validatePhotoShrink(path string, c *PhotoShrinkSettings) error {
	if c == nil {
		return nil
	}
	// 800 px より小さくすると、紙や図面を写した写真の字が読めなくなる。
	if c.LongSide < 800 || c.LongSide > maxImageSide {
		return fmt.Errorf("%s: photo_shrink の long_side は 800 から %d のあいだで書いてください", path, maxImageSide)
	}
	if c.Quality < 50 || c.Quality > 95 {
		return fmt.Errorf("%s: photo_shrink の quality は 50 から 95 のあいだで書いてください", path)
	}
	return nil
}

// PhotoShrinkConfig は設定 `photo_shrink` を返します（無ければ nil＝縮めない）。
func PhotoShrinkConfig() *PhotoShrinkSettings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.PhotoShrink
}

// stripJPEGQuality は、縮めずにメタデータだけ落とすときの品質です（再符号化は一度きりなので高めに取る）。
const stripJPEGQuality = 90

// reencodeJPEG は JPEG を復号し、EXIF の向きを画素へ反映して、長い辺が longSide を超えていれば縮めてから
// 符号化し直します（longSide が 0 なら縮めない）。復号→再符号化なので、EXIF・XMP・ICC を含む全メタデータが落ちます。
// before・after は向きを直したあとの画素の大きさ、resized は縮めたかです。縮めたときの品質は quality、
// 縮めないときは stripJPEGQuality。
func reencodeJPEG(content []byte, longSide, quality int) (out []byte, before, after image.Point, resized bool, err error) {
	// 先に大きさだけ読む——数百バイトのファイルが 65535×65535 を名乗ると、復号が何 GB も取ってサーバーが落ちる
	// （メールの添付は自動で取り込むので、外のだれでも送れる・2026-10-22 の見直し）。
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, before, after, false, fmt.Errorf("JPEGとして読めません: %v", err)
	}
	if err := checkImageSize(cfg.Width, cfg.Height); err != nil {
		return nil, before, after, false, err
	}
	orientation := jpegOrientation(content)
	img, err := jpeg.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, before, after, false, fmt.Errorf("JPEGとして読めません: %v", err)
	}
	img = applyOrientation(img, orientation)
	before = img.Bounds().Size()
	q := stripJPEGQuality
	if longSide > 0 && max(before.X, before.Y) > longSide {
		img = downscaleArea(img, longSide)
		resized, q = true, quality
	}
	after = img.Bounds().Size()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, before, after, false, fmt.Errorf("JPEGを書き出せません: %v", err)
	}
	return buf.Bytes(), before, after, resized, nil
}

// downscaleArea は、長い辺が longSide になるように画像を縮めます（面積平均・標準ライブラリだけ）。
// 縮めるだけで、大きくはしません。
func downscaleArea(src image.Image, longSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= longSide && h <= longSide {
		return src
	}
	nw, nh := longSide, max(1, h*longSide/w)
	if h > w {
		nw, nh = max(1, w*longSide/h), longSide
	}
	rgba, ok := src.(*image.RGBA)
	if !ok || rgba.Rect.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, (y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, (x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint32
			for yy := y0; yy < y1; yy++ {
				row := rgba.Pix[yy*rgba.Stride+x0*4 : yy*rgba.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					r, g, bl, n = r+uint32(row[i]), g+uint32(row[i+1]), bl+uint32(row[i+2]), n+1
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}
	return dst
}

// isPhotoName は、縮める対象の名前（JPEG）かを返します。
func isPhotoName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return true
	}
	return false
}

// 縮めなかった理由です。
var (
	errPhotoShrinkOff  = errors.New("写真を小さくする設定（photo_shrink）がありません")
	errNotPhoto        = errors.New("JPEG の写真ではありません（小さくできるのは JPEG だけです）")
	errPhotoSmall      = errors.New("もう小さい写真です")
	errPhotoNotSmaller = errors.New("縮めても小さくなりませんでした（そのままにしました）")
	errPhotoChanged    = errors.New("縮めているあいだに、ほかで書き換えられました（そのままにしました）")
)

// photoShrinkResult は1枚を縮めた結果です。
type photoShrinkResult struct {
	Before int64       `json:"before"` // バイト
	After  int64       `json:"after"`
	From   image.Point `json:"-"`
	To     image.Point `json:"-"`
}

// shrinkPhotoFile は、ページ pageID の添付 stored（JPEG）を設定の大きさまで縮めて上書きします。
// 書き換えてよいか（canWriteAttachment）は呼ぶ側ではなく、ここで確かめます。
func shrinkPhotoFile(user *auth.User, pageID, stored string) (photoShrinkResult, error) {
	var res photoShrinkResult
	conf := PhotoShrinkConfig()
	if conf == nil {
		return res, errPhotoShrinkOff
	}
	if !isPhotoName(stored) || filepath.Base(stored) != stored || strings.HasPrefix(stored, ".") || IsAttachmentMetaFile(stored) {
		return res, errNotPhoto
	}
	if err := canWriteAttachment(user, pageID); err != nil {
		return res, err
	}
	path := filepath.Join(page.AttachmentDir(pageID), stored)
	current, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	// 縮める（重い）のはロックの外で。差し替える直前に、読んだときから変わっていないかを確かめる。
	out, before, after, resized, err := reencodeJPEG(current, conf.LongSide, conf.Quality)
	if err != nil {
		return res, err
	}
	if !resized {
		return res, errPhotoSmall
	}
	if len(out) >= len(current) {
		return res, errPhotoNotSmaller
	}
	sum := sha256.Sum256(current)
	origSHA := hex.EncodeToString(sum[:])
	localEditMu.Lock() // 「📝 ローカル編集」の上書きと重ならないように（同じ添付を1つずつ）。
	defer localEditMu.Unlock()
	now, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	if nowSum := sha256.Sum256(now); hex.EncodeToString(nowSum[:]) != origSHA {
		return res, errPhotoChanged
	}
	if err := page.WriteFileAtomic(path, out, 0o644); err != nil {
		return res, err
	}
	fresh := NewAttachmentMeta(stored, user.Username, "", out)
	if err := UpdateAttachmentMeta(pageID, stored, func(m *AttachmentMeta) {
		if m.Name == "" {
			m.Name = stored
		}
		// 届いた事実（名前・保存日時・保存した人・由来）はそのまま。中身が変わったぶんだけ進める。
		if m.ShrunkFrom == nil {
			size, sha := m.Size, m.SHA256
			if sha == "" || sha != origSHA {
				// 目録が無い・目録より後に書き換わっていた——いま縮める前の中身を元とする。
				size, sha = int64(len(current)), origSHA
			}
			m.ShrunkFrom = &ShrunkPhoto{Size: size, SHA256: sha, Width: before.X, Height: before.Y,
				At: time.Now().In(time.Local).Format(time.RFC3339), By: user.Username}
		}
		m.Size, m.SHA256 = fresh.Size, fresh.SHA256
	}); err != nil {
		auth.Audit(user.Username, "attach.meta-failed", pageID+"/"+stored+": "+err.Error())
	}
	return photoShrinkResult{Before: int64(len(current)), After: int64(len(out)), From: before, To: after}, nil
}

// photoShrinkMessage は縮めなかった理由を、画面に出す文と HTTP の状態にします。
func photoShrinkMessage(err error) (int, string) {
	switch {
	case errors.Is(err, os.ErrPermission):
		return http.StatusForbidden, "このページの写真を書き換える権限がありません"
	case errors.Is(err, errReadOnlyArea):
		return http.StatusForbidden, "この写真は小さくできません（届いたものをそのまま残す範囲です——設定 attachment_readonly）"
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound, "写真が見つかりません"
	case errors.Is(err, errPhotoShrinkOff), errors.Is(err, errNotPhoto), errors.Is(err, errPhotoSmall),
		errors.Is(err, errPhotoNotSmaller), errors.Is(err, errPhotoChanged):
		return http.StatusConflict, err.Error()
	}
	return http.StatusBadRequest, "写真を小さくできませんでした: " + err.Error()
}

// PhotoShrinkAPIHandler は POST /api/photo-shrink です（写真ごとの「⋯」の「🗜 写真を小さくする」）。
// 入力: {ref}（`ページ番号-添付ID`）。読めない添付は「見つかりません」（読めないと無いを区別しない）。
func PhotoShrinkAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	fr, ok := ParseFileRef(req.Ref)
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ref は「ページ番号-添付ID」の形です")
		return
	}
	stored, _, found := AttachmentOfRef(user, fr)
	if !found {
		JSONFail(w, http.StatusNotFound, "写真が見つかりません")
		return
	}
	pageID, _ := page.NormalizeID(fr.PageID)
	res, err := shrinkPhotoFile(user, pageID, stored)
	if err != nil {
		code, msg := photoShrinkMessage(err)
		JSONFail(w, code, msg)
		return
	}
	auth.Audit(user.Username, "attach.shrink", fmt.Sprintf("%s/%s %d→%d バイト（%d×%d → %d×%d）",
		pageID, stored, res.Before, res.After, res.From.X, res.From.Y, res.To.X, res.To.Y))
	WriteJSON(w, map[string]any{
		"success": true,
		"before":  res.Before,
		"after":   res.After,
		"from":    fmt.Sprintf("%d×%d", res.From.X, res.From.Y),
		"to":      fmt.Sprintf("%d×%d", res.To.X, res.To.Y),
	})
}
