package cms

// ─────────────────────────────────────────────────────────────────────────
// 汎用の添付アップロード（2026-08-31 ユーザー決定3件を実装）
//
//   - 「.eml の扱いは添付から始めましょう」
//   - 「サイズ上限32MiBは設定で変えられるように」（settings.go の max_upload_mib）
//   - 「添付はドラッグアンドドロップに耐えられた方が良い」（本文へのドロップが入口）
//
// 受ける拡張子は設定 `attachment_extensions`（既定＝ワンノート実データの15種
// −.pdf の14種。【考察】ワンノート移行.md §3-4）。**中身は検査しません**——
// CAD・Office のマジックナンバー検証は現実的でなく、安全の本体は配信側にある:
// DataFileHandler が未知の種別を `Content-Disposition: attachment`＋nosniff で
// 返すので、ブラウザは解釈しない（SVG で確立した「入口は網・配信が本体」の一般化）。
//
// **画像と .pdf はこの口では受けません**——専用の口（中身検査・EXIF除去つき）を
// 迂回させないため。エディタ側も種類で振り分ける。
//
// 保存先は files/ サブフォルダ（正本と同居しない——構造で塞ぐ。storage.go）。
// 名前の検査は SafeAttachmentName の1箇所を全アップロード口が共有する。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// UploadFileHandler は POST /api/upload-file（汎用の添付）です。
func UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	pageID := r.FormValue("page_id")
	if pageID == "" {
		http.Error(w, "page_id is required", http.StatusBadRequest)
		return
	}
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}

	// **先に引き受ける口があれば回す**（upload_intercept.go・2026-09-15）。
	// いまは通信箱への到着を取り込み係へ回す口だけ（intake.go）——コアは通信箱を
	// 名指ししない。引き受けなければ、通常の添付として下の経路へ流れる。
	if interceptUpload(w, r, pageID, "file") {
		return
	}

	// 添付は同名を無条件で上書きし、リビジョンも無い——本文編集と同じ編集ロックで直列化。
	if !editlock.RequireEditLock(w, r, pageID) {
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "ファイルを受け取れませんでした（サイズ上限は "+
			strconv.FormatInt(limit>>20, 10)+"MiB です）", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	// 専用の口があるものは迂回させない（画像＝マジックナンバー検証・EXIF除去、
	// PDF＝%PDF- 検証）。**通信箱宛てだけは例外**で、上の取り込み分岐が
	// 種類ごとの検査を通したうえで引き受ける（1つの口で全部受ける・2026-09-03）。
	if ext == ".pdf" || allowedImageExts[ext] {
		http.Error(w, "この種類は専用のアップロード口を使ってください（画像・PDF）", http.StatusBadRequest)
		return
	}
	fileName, err := SafeAttachmentName(pageID, header.Filename, GenericAttachmentExts(),
		"この拡張子は添付として受け付けていません（許可リストは config/settings.json の attachment_extensions）")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return
	}

	// 保存の作法は1箇所（attachment_save.go）。生成名・上書きの監査まで含む。
	username := ""
	if u := auth.CurrentUser(r); u != nil {
		username = u.Username
	}
	attachID, fileName, saveErr := SaveAttachment(pageID, username, fileName, content)
	if saveErr != nil {
		JSONFail(w, http.StatusInternalServerError, "Failed to save file")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":   true,
		"file_name": fileName,
		"id":        attachID, // リンクブロックの data-id に使う（ファイル名と一致）
		"href":      page.AttachmentURLFor(pageID, fileName),
	})
}

// GuardUploadContent は、1つの口で何でも受ける経路のために、**専用の口が持っていた守り**を
// 種類ごとに当てます。画像は EXIF を落とした中身を返すので、**戻り値のほうを保存すること**。
//
// （2026-09-15 に `checkIntakeContent` から改名して公開。使い手の通信箱は「何かが届いた」の
// 1つの口で全部を受けます（メール・PDF・画像・図面…）。守りそのものは通信の語彙ではなく
// 画像とPDFの検査なので、コアに残しています。）
//
// そのぶん、**専用の口が持っていた守りをここで引き受けます**——
// PDF はマジックナンバー、画像は種別の一致とメタデータ除去。
// それ以外（DXF・Office・ZIP 等）は中身を検査しません——CAD/Office の検証は
// 現実的でなく、安全の本体は配信側（未知の種別は `attachment`＋nosniff）にあります。
func GuardUploadContent(fileName string, content []byte) ([]byte, error) {
	switch ext := strings.ToLower(filepath.Ext(fileName)); {
	case ext == ".pdf":
		if !bytes.HasPrefix(content, []byte("%PDF-")) {
			return nil, errors.New("PDFファイルではありません（先頭が %PDF- ではありません）")
		}
		return content, nil
	case allowedImageExts[ext]:
		kind, err := checkImageContent(fileName, content)
		if err != nil {
			return nil, err
		}
		// カメラ写真のGPSが載ったまま保存されないよう、正本を無害化してから渡す。
		return StripImageMetadata(kind, content)
	}
	return content, nil
}
