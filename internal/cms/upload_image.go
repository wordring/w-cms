package cms

// ─────────────────────────────────────────────────────────────────────────
// 画像添付のアップロード（要件定義書 §2.6）
//
// PDF の口（UploadPDFHandler）と同じ守りを掛けたうえで、画像固有の3点を足します:
//
//   - **中身で種別を決める**（SniffImageKind）。名乗りと中身が食い違うファイルは
//     拒否する——`.png` という名前のHTMLを置かれると配信側の判定を欺けるため。
//   - **EXIF を落とす**（StripImageMetadata）。カメラ写真のGPSが公開サイトへ載る
//     事故を、公開時の選別ではなく**この経路の一律処理**で防ぐ（フェイルクローズ）。
//   - **HEIC は理由つきで拒否**する。iOS のカメラ写真がこの形式で届くことがあり、
//     「なぜ入らないのか」が分からないと利用者が詰まる（サーバー側変換は要件外）。
//
// 本文は返した `src`（`/<ページID>/<生成ID>.<拡張子>`——page.AttachmentURLFor の
// きれいなURL）を `<img src>` に入れます。**絶対パス**なのは、ページのアドレス
// （`/000012`）からの相対名がページの隣ではなくサイトのルートを指してしまうためです。
// サニタイザは埋め込みの絶対パスを **きれいなURL と旧形の `/data/` 配下**に限って
// 許可しており（htmldoc/sanitize.go の safeEmbedURL）、この形はその許可範囲です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"w-cms/internal/cms/page"
)

// UploadImageHandler はドロップ／ファイル選択／カメラ撮影で届いた画像を、
// 該当ページのフォルダへ保存します（POST /api/upload-image）。
func UploadImageHandler(w http.ResponseWriter, r *http.Request) {
	// 入口は3本共通（upload_common.go）。画像は受け口（取り込み）へ回しません。
	up, ok := openUpload(w, r, "image_file", false, imageAttachmentName)
	if !ok {
		return
	}

	kind, err := checkImageContent(up.fileName, up.content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// EXIF などのメタデータをここで落とす（保存する正本が無害化済みになる）。
	content, err := StripImageMetadata(kind, up.content)
	if err != nil {
		http.Error(w, "画像を処理できませんでした: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 保存の作法は1箇所（attachment_save.go）。生成名・上書きの監査まで含む。
	attachID, fileName, saveErr := SaveAttachment(up.pageID, up.username, up.fileName, content)
	if saveErr != nil {
		JSONFail(w, http.StatusInternalServerError, "画像を保存できませんでした")
		return
	}

	WriteJSON(w, map[string]any{
		"success":   true,
		"file_name": fileName,
		"kind":      kind,
		// 本文の <img src> へそのまま入れる絶対パス。
		"src": page.AttachmentURLFor(up.pageID, fileName),
		"id":  attachID,
	})
}

// imageAttachmentName は画像の口の名前の検査です。
//
// 扱えないと分かっている画像形式は、名前の段階で**理由を具体的に**返します。
// 「許可リスト外です」だけだと、iOS のカメラ写真（HEIC）が入らない人が
// 次に何をすればよいか分からない（要件 §2.6）。
func imageAttachmentName(pageID, raw string) (string, error) {
	if msg := unsupportedImageMessage(raw); msg != "" {
		return "", errors.New(msg)
	}
	return SafeAttachmentName(pageID, raw, allowedImageExts,
		"画像ファイル（png / jpeg / webp / gif / svg）のみアップロードできます")
}

// unsupportedImageMessage は「画像ではあるが扱えない形式」の拒否理由を返します
// （扱える形式なら空文字）。中身を読む前に名前で判るぶんだけを担当し、名乗りを
// 偽ったファイルは checkImageContent が中身から捕まえます。
func unsupportedImageMessage(rawName string) string {
	switch strings.ToLower(filepath.Ext(rawName)) {
	case ".heic", ".heif":
		return "HEIC形式の画像は扱えません。端末の設定で「互換性優先（JPEG）」にして撮り直すか、" +
			"JPEG / PNG に変換してからアップロードしてください"
	case ".avif":
		return "AVIF形式の画像は扱えません。JPEG / PNG / WebP に変換してください"
	case ".tif", ".tiff":
		return "TIFF形式の画像は扱えません。JPEG / PNG に変換してください"
	case ".bmp":
		return "BMP形式の画像は扱えません。JPEG / PNG に変換してください"
	}
	return ""
}

// checkImageContent は中身から種別を決め、名乗り（拡張子）と一致するかを確かめます。
// 許可外の形式は、利用者が理由を読んで次の手が打てる文言で拒否します。
func checkImageContent(fileName string, content []byte) (string, error) {
	kind := SniffImageKind(content)
	switch kind {
	case "heic":
		return "", fmt.Errorf(
			"HEIC形式の画像は扱えません。端末の設定で「互換性優先（JPEG）」にして撮り直すか、" +
				"JPEG / PNG に変換してからアップロードしてください")
	case "avif":
		return "", fmt.Errorf("AVIF形式の画像は扱えません。JPEG / PNG / WebP に変換してください")
	case "":
		return "", fmt.Errorf("画像ファイルではありません（png / jpeg / webp / gif / svg のいずれかを指定してください）")
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	// 拡張子は名乗りにすぎない。中身と食い違うものは、配信側の判定を欺く道具に
	// なりうるので受け付けない（`.png` という名前のSVGなど）。
	if want := extKinds[ext]; want != kind {
		return "", fmt.Errorf("拡張子（%s）と中身（%s）が一致しません", ext, kind)
	}
	if kind == "svg" {
		if err := ValidateSVG(content); err != nil {
			return "", err
		}
	}
	return kind, nil
}
