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
// 本文は返した `src`（`/data/master/<prefix>/<id>/<名前>`）を `<img src>` に入れます。
// **絶対パス**なのは、ページのアドレス（`/000012`）からの相対名がページの隣ではなく
// サイトのルートを指してしまうためです。サニタイザは埋め込みの絶対パスを
// `/data/` 配下に限って許可しており（htmldoc/sanitize.go の safeEmbedURL）、
// この形はその許可範囲そのものです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// UploadImageHandler はドロップ／ファイル選択／カメラ撮影で届いた画像を、
// 該当ページのフォルダへ保存します（POST /api/upload-image）。
func UploadImageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// フォームを読む前に本文サイズを制限する（FormValue が内部でパースするため）。
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes())

	pageID, ok := page.NormalizeID(r.FormValue("page_id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	// 画像の追加はページ内容の変更なので write 権限を要求する。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// 添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）。
	// 本文編集と同じ編集ロックで直列化する（PDF の口と同じ扱い）。
	if !editlock.RequireEditLock(w, r, pageID) {
		return
	}

	file, header, err := r.FormFile("image_file")
	if err != nil {
		http.Error(w, fmt.Sprintf("ファイルを受け取れませんでした（サイズ上限は %dMiB です）", MaxUploadBytes()>>20), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 扱えないと分かっている画像形式は、名前の段階で**理由を具体的に**返す。
	// 「許可リスト外です」だけだと、iOS のカメラ写真（HEIC）が入らない人が
	// 次に何をすればよいか分からない（要件 §2.6）。
	if msg := unsupportedImageMessage(header.Filename); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	// 保存する名前を先に確定させる（種類が許可されないなら読み込むまでもない）。
	fileName, err := safeAttachmentName(pageID, header.Filename, allowedImageExts,
		"画像ファイル（png / jpeg / webp / gif / svg）のみアップロードできます")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return
	}

	kind, err := checkImageContent(fileName, content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// EXIF などのメタデータをここで落とす（保存する正本が無害化済みになる）。
	content, err = StripImageMetadata(kind, content)
	if err != nil {
		http.Error(w, "画像を処理できませんでした: "+err.Error(), http.StatusBadRequest)
		return
	}

	attachDir := page.AttachmentDir(pageID)
	os.MkdirAll(attachDir, 0755)
	// 保存名はサーバーが生成する（元の名前はURLに出さない。表示は本文のリンク文字が担う）。
	// 生成IDはリンクブロックの data-id と一致させる（storage.go の3役）。
	attachID := page.GeneratedAttachmentID(pageID, strings.ToLower(filepath.Ext(fileName)))
	fileName = attachID + strings.ToLower(filepath.Ext(fileName))
	savePath := filepath.Join(attachDir, fileName)

	// 上書きかどうかは書く前にしか分からない（監査記録で区別するため）。
	overwrote := false
	if _, err := os.Stat(savePath); err == nil {
		overwrote = true
	}

	if err := page.WriteFileAtomic(savePath, content, 0644); err != nil {
		http.Error(w, "画像を保存できませんでした", http.StatusInternalServerError)
		return
	}

	action := "attach"
	if overwrote {
		action = "attach.overwrite"
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, action, pageID+"/"+fileName)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"file_name": fileName,
		"kind":      kind,
		// 本文の <img src> へそのまま入れる絶対パス。
		"src": page.AttachmentURLFor(pageID, fileName),
		"id":  attachID,
	})
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
