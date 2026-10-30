package cms

// ─────────────────────────────────────────────────────────────────────────
// 画像の簡易編集——回転・切り抜き・明るくする・元に戻す（2026-10-30）
//
// 利用者:「画像をクリックすると拡大表示画面になりますが、ここに部分の拡大表示機能と簡易編集機能を付けられないでしょうか」
// 「簡易編集機能は、90度ずつの回転、切り抜き、画像を明るくするが欲しいです」
// 「拡大表示は保存が必要なく、編集機能は無条件に保存が必要です。したがって編集時のアンドゥも必要かと思われます。」
//
// 画面は拡大表示（assets/app.js の「画像の拡大表示」）です。部分の拡大は画面だけで済み、ここには来ません。
//
//	GET  /api/photo-edit?ref=<ページ番号-添付ID> … 編集できるか・戻せるか・いまの版の印
//	POST /api/photo-edit {ref, v, op, …}          … 1回の操作を保存する（押すたびに保存）
//
// ── 上書きして、前の中身を控える ──
//
// 操作のたびに、同じ添付（同じ住所）を上書きし、前の中身を `files/.versions/` に残します（attachment_write.go の決まり）。
// 本文は書き換えないので、この画像を指す全部のページで新しい画像になります。「元に戻す」は、いちばん新しい控えを
// そのまま戻して控えを1つ消します（何度でも。戻した中身は、編集する前と1バイトも違いません）。
//
// ── 守り ──
//
//   - 書き換えてよい添付だけです（canWriteAttachment。ページへの書き込み権限と、読み取り専用の範囲 `attachment_readonly` の外。
//     写真を小さくするのと同じ決まりで、届いた添付は届いた事実の証拠なので編集しません）。
//   - JPEG と PNG だけです。標準ライブラリに WebP を書き出す道具が無く、GIF は動きが消えるためです。
//   - 画面が見ていた版の印（`v`）といまの版が違えば断ります。見ていない絵に操作を重ねないためです。
//   - 復号の大きさの検査と同時に動かす数の上限は、写真を小さくするのと同じです（checkImageSize・jpegDecodeSlots）。
//
// 操作は、書き出すたびに JPEG を作り直します。品質は photoEditJPEGQuality です（何度か押しても目立たないように高めに取る）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

const (
	photoEditJPEGQuality = 92
	// photoEditGamma は「☀ 明るく」1回ぶんのガンマです。暗い所を持ち上げ、もともと白い所は白のままにします（掛け算だと白が飛ぶ）。
	photoEditGamma = 1.25
	// photoEditMinSide は切り抜いたあとの辺の下限（画素）です。指が滑って点のような絵にならないように。
	photoEditMinSide = 16
)

// 編集しなかった理由です。
var (
	errPhotoEditKind  = errors.New("編集できるのは JPEG と PNG の画像だけです")
	errPhotoEditOp    = errors.New("知らない操作です")
	errPhotoEditCrop  = errors.New("切り抜く範囲が小さすぎるか、画像の外です")
	errPhotoEditNone  = errors.New("これより前には戻せません")
	errPhotoEditStale = errors.New("ほかの人か別の画面が、この画像を先に書き換えました。表示を新しくしたので、見てからもう一度押してください")
)

// isEditablePhotoName は編集できる形式の名前かを返します。
func isEditablePhotoName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// photoEditOp は1回の操作です。切り抜きの範囲は、いまの絵に対する割合（0〜1）です——画面の大きさに依りません。
type photoEditOp struct {
	Op string  `json:"op"` // "rotate-right"・"rotate-left"・"crop"・"brighten"・"undo"
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	W  float64 `json:"w"`
	H  float64 `json:"h"`
}

// toNRGBA は絵を、透明を保ったまま1画素4バイトの形にします。
func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// rotateNRGBA は絵を90度回します（right なら時計回り）。
func rotateNRGBA(src *image.NRGBA, right bool) *image.NRGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		from := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			nx, ny := h-1-y, x
			if !right {
				nx, ny = y, w-1-x
			}
			copy(dst.Pix[ny*dst.Stride+nx*4:ny*dst.Stride+nx*4+4], from[x*4:x*4+4])
		}
	}
	return dst
}

// cropNRGBA は割合の範囲を切り抜きます。範囲は絵の中に丸めます（画面の丸めで端が少しはみ出ても断らない）。
func cropNRGBA(src *image.NRGBA, op photoEditOp) (*image.NRGBA, error) {
	if math.IsNaN(op.X+op.Y+op.W+op.H) || op.W <= 0 || op.H <= 0 || op.X >= 1 || op.Y >= 1 || op.X+op.W <= 0 || op.Y+op.H <= 0 {
		return nil, errPhotoEditCrop
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	x0, y0 := max(0, int(math.Round(op.X*float64(w)))), max(0, int(math.Round(op.Y*float64(h))))
	x1, y1 := min(w, int(math.Round((op.X+op.W)*float64(w)))), min(h, int(math.Round((op.Y+op.H)*float64(h))))
	if x1-x0 < photoEditMinSide || y1-y0 < photoEditMinSide {
		return nil, errPhotoEditCrop
	}
	dst := image.NewNRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	for y := y0; y < y1; y++ {
		copy(dst.Pix[(y-y0)*dst.Stride:(y-y0+1)*dst.Stride], src.Pix[y*src.Stride+x0*4:y*src.Stride+x1*4])
	}
	return dst, nil
}

// brightenNRGBA は絵を1段明るくします（透明の度合いは変えません）。
func brightenNRGBA(img *image.NRGBA) {
	var lut [256]uint8
	for i := range lut {
		lut[i] = uint8(math.Round(255 * math.Pow(float64(i)/255, 1/photoEditGamma)))
	}
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = lut[img.Pix[i]], lut[img.Pix[i+1]], lut[img.Pix[i+2]]
	}
}

// applyPhotoEdit は中身を復号し、操作を当てて書き出します。書き出す形式は元の形式です（名前＝住所を変えないため）。
func applyPhotoEdit(content []byte, op photoEditOp) (out []byte, size image.Point, err error) {
	kind := SniffImageKind(content)
	var cfg image.Config
	switch kind {
	case "jpeg":
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(content))
	case "png":
		cfg, err = png.DecodeConfig(bytes.NewReader(content))
	default:
		return nil, size, errPhotoEditKind
	}
	if err != nil {
		return nil, size, fmt.Errorf("画像として読めません: %v", err)
	}
	// 復号の前に大きさを確かめ、同時に動かす数を絞る（reencodeJPEG と同じ守り）。
	if err := checkImageSize(cfg.Width, cfg.Height); err != nil {
		return nil, size, err
	}
	jpegDecodeSlots <- struct{}{}
	defer func() { <-jpegDecodeSlots }()
	var src image.Image
	if kind == "jpeg" {
		if src, err = jpeg.Decode(bytes.NewReader(content)); err == nil {
			src = applyOrientation(src, jpegOrientation(content)) // 保存の入口で向きは画素に入っているが、古い写真のために
		}
	} else {
		src, err = png.Decode(bytes.NewReader(content))
	}
	if err != nil {
		return nil, size, fmt.Errorf("画像として読めません: %v", err)
	}
	img := toNRGBA(src)
	switch op.Op {
	case "rotate-right":
		img = rotateNRGBA(img, true)
	case "rotate-left":
		img = rotateNRGBA(img, false)
	case "crop":
		if img, err = cropNRGBA(img, op); err != nil {
			return nil, size, err
		}
	case "brighten":
		brightenNRGBA(img)
	default:
		return nil, size, errPhotoEditOp
	}
	var buf bytes.Buffer
	if kind == "jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: photoEditJPEGQuality})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return nil, size, fmt.Errorf("画像を書き出せません: %v", err)
	}
	return buf.Bytes(), img.Rect.Size(), nil
}

// photoEditTarget は編集してよいかを確かめて、添付の場所を返します。
func photoEditTarget(user *auth.User, pageID, stored string) (string, error) {
	if !isEditablePhotoName(stored) || filepath.Base(stored) != stored || strings.HasPrefix(stored, ".") || IsAttachmentMetaFile(stored) {
		return "", errPhotoEditKind
	}
	if err := canWriteAttachment(user, pageID); err != nil {
		return "", err
	}
	path, ok := page.AttachmentPath(pageID, stored)
	if !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}

// attachmentVersionNow はいまの版の印を返します。
func attachmentVersionNow(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return page.AttachmentVersion(fi), nil
}

// editPhotoFile は操作を1回当てて保存します（前の中身は控える）。v は画面が見ていた版の印です。
func editPhotoFile(user *auth.User, pageID, stored, v string, op photoEditOp) (image.Point, error) {
	var size image.Point
	path, err := photoEditTarget(user, pageID, stored)
	if err != nil {
		return size, err
	}
	if now, err := attachmentVersionNow(path); err != nil {
		return size, err
	} else if now != v {
		return size, errPhotoEditStale
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return size, err
	}
	// 作り直す（重い）のはロックの外で。差し替える直前に、読んだときから変わっていないかを確かめる（photo_shrink.go と同じ）。
	out, size, err := applyPhotoEdit(current, op)
	if err != nil {
		return size, err
	}
	origSHA := sha256Hex(current)
	localEditMu.Lock()
	defer localEditMu.Unlock()
	now, err := os.ReadFile(path)
	if err != nil {
		return size, err
	}
	if sha256Hex(now) != origSHA {
		return size, errPhotoEditStale
	}
	// 控えが書けなければ上書きしない（戻せない上書きにしない）。
	if err := saveAttachmentVersion(pageID, stored); err != nil {
		return size, err
	}
	if err := page.WriteFileAtomic(path, out, 0o644); err != nil {
		return size, err
	}
	fresh := NewAttachmentMeta(stored, user.Username, "", out)
	if err := UpdateAttachmentMeta(pageID, stored, func(m *AttachmentMeta) {
		if m.Name == "" {
			m.Name = stored
		}
		// 届いた事実（名前・保存日時・保存した人・由来）はそのまま。中身が変わったぶんだけ進める。
		if m.EditedFrom == "" {
			m.EditedFrom = origSHA
		}
		m.EditedAt, m.EditedBy = time.Now().In(time.Local).Format(time.RFC3339), user.Username
		m.Size, m.SHA256 = fresh.Size, fresh.SHA256
	}); err != nil {
		auth.Audit(user.Username, "attach.meta-failed", pageID+"/"+stored+": "+err.Error())
	}
	if _, err := page.BumpUpdatedAt(pageID); err != nil {
		auth.Audit(user.Username, "attach.touch-failed", pageID+"/"+stored+": "+err.Error())
	}
	return size, nil
}

// undoPhotoEdit は、いちばん新しい控えを戻して、その控えを消します。
func undoPhotoEdit(user *auth.User, pageID, stored, v string) error {
	path, err := photoEditTarget(user, pageID, stored)
	if err != nil {
		return err
	}
	localEditMu.Lock()
	defer localEditMu.Unlock()
	if now, err := attachmentVersionNow(path); err != nil {
		return err
	} else if now != v {
		return errPhotoEditStale
	}
	versions := attachmentVersions(pageID, stored)
	if len(versions) == 0 {
		return errPhotoEditNone
	}
	versionPath := filepath.Join(page.AttachmentDir(pageID), attachmentVersionsDir, stored, versions[0])
	content, err := os.ReadFile(versionPath)
	if err != nil {
		return err
	}
	if err := page.WriteFileAtomic(path, content, 0o644); err != nil {
		return err
	}
	// 控えを消すのは戻し終えてから（消せなくても、次の「元に戻す」が同じ中身を戻すだけで、失うものは無い）。
	if err := os.Remove(versionPath); err != nil {
		auth.Audit(user.Username, "attach.version-remove-failed", pageID+"/"+stored+": "+err.Error())
	}
	fresh := NewAttachmentMeta(stored, user.Username, "", content)
	if err := UpdateAttachmentMeta(pageID, stored, func(m *AttachmentMeta) {
		if m.Name == "" {
			m.Name = stored
		}
		m.Size, m.SHA256 = fresh.Size, fresh.SHA256
		if m.EditedFrom == fresh.SHA256 { // 編集する前まで戻った
			m.EditedFrom, m.EditedAt, m.EditedBy = "", "", ""
		}
	}); err != nil {
		auth.Audit(user.Username, "attach.meta-failed", pageID+"/"+stored+": "+err.Error())
	}
	if _, err := page.BumpUpdatedAt(pageID); err != nil {
		auth.Audit(user.Username, "attach.touch-failed", pageID+"/"+stored+": "+err.Error())
	}
	return nil
}

// photoEditMessage は編集しなかった理由を、画面に出す文と HTTP の状態にします。
func photoEditMessage(err error) (int, string) {
	switch {
	case errors.Is(err, os.ErrPermission):
		return http.StatusForbidden, "この画像を編集する権限がありません"
	case errors.Is(err, errReadOnlyArea):
		return http.StatusForbidden, "この画像は編集できません（届いたものをそのまま残す範囲です——設定 attachment_readonly）"
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound, "画像が見つかりません"
	case errors.Is(err, errPhotoEditStale), errors.Is(err, errPhotoEditNone):
		return http.StatusConflict, err.Error()
	case errors.Is(err, errPhotoEditKind), errors.Is(err, errPhotoEditOp), errors.Is(err, errPhotoEditCrop):
		return http.StatusBadRequest, err.Error()
	}
	return http.StatusBadRequest, "画像を編集できませんでした: " + err.Error()
}

// photoEditState は画面に渡す、いまの様子です。
func photoEditState(user *auth.User, pageID, stored string) map[string]any {
	state := map[string]any{"success": true, "can_edit": false, "can_undo": false, "v": ""}
	if p, ok := page.AttachmentPath(pageID, stored); ok {
		if v, err := attachmentVersionNow(p); err == nil {
			state["v"] = v
		}
	}
	if _, err := photoEditTarget(user, pageID, stored); err != nil {
		return state
	}
	state["can_edit"] = true
	state["can_undo"] = len(attachmentVersions(pageID, stored)) > 0
	return state
}

// PhotoEditAPIHandler は /api/photo-edit です（冒頭の説明）。読めない添付は「見つかりません」（読めないと無いを区別しない）。
func PhotoEditAPIHandler(w http.ResponseWriter, r *http.Request) {
	var (
		user *auth.User
		ok   bool
		raw  string
		v    string
		op   photoEditOp
	)
	if r.Method == http.MethodGet {
		if user, ok = GateJSONGet(w, r); !ok {
			return
		}
		raw = r.URL.Query().Get("ref")
	} else {
		if user, ok = GateJSONPost(w, r); !ok {
			return
		}
		var req struct {
			Ref string `json:"ref"`
			V   string `json:"v"`
			photoEditOp
		}
		if !DecodeJSONBody(w, r, &req) {
			return
		}
		raw, v, op = req.Ref, req.V, req.photoEditOp
	}
	fr, ok := ParseFileRef(raw)
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ref は「ページ番号-添付ID」の形です")
		return
	}
	stored, _, found := AttachmentOfRef(user, fr)
	if !found {
		JSONFail(w, http.StatusNotFound, "画像が見つかりません")
		return
	}
	pageID, _ := page.NormalizeID(fr.PageID)
	if r.Method == http.MethodGet {
		WriteJSON(w, photoEditState(user, pageID, stored))
		return
	}
	var err error
	if op.Op == "undo" {
		err = undoPhotoEdit(user, pageID, stored, v)
	} else {
		_, err = editPhotoFile(user, pageID, stored, v, op)
	}
	if err != nil {
		code, msg := photoEditMessage(err)
		// 断ったときも、いまの版の印を返す（先に書き換えられていたら、画面が表示を新しくできるように）。
		w.WriteHeader(code)
		state := photoEditState(user, pageID, stored)
		state["success"], state["message"] = false, msg
		WriteJSON(w, state)
		return
	}
	auth.Audit(user.Username, "attach.edit", pageID+"/"+stored+" "+op.Op)
	WriteJSON(w, photoEditState(user, pageID, stored))
}
