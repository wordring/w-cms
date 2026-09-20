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
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"w-cms/internal/cms/page"
)

// UploadFileHandler は POST /api/upload-file（汎用の添付）です。
func UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	// 入口は3本共通（upload_common.go）。**先に引き受ける口があれば回します**
	// （upload_intercept.go・2026-09-15）。いまは通信箱への到着を取り込み係へ回す口だけ
	// （intake.go）——コアは通信箱を名指ししない。引き受けなければ通常の添付になります。
	up, ok := openUpload(w, r, "file", true, genericAttachmentName)
	if !ok {
		return
	}

	// 保存の作法は1箇所（attachment_save.go）。生成名・上書きの監査まで含む。
	attachID, fileName, saveErr := SaveAttachment(up.pageID, up.username, up.fileName, up.content)
	if saveErr != nil {
		JSONFail(w, http.StatusInternalServerError, "Failed to save file")
		return
	}

	WriteJSON(w, map[string]any{
		"success":   true,
		"file_name": fileName,
		"id":        attachID, // リンクブロックの data-id に使う（ファイル名と一致）
		"href":      page.AttachmentURLFor(up.pageID, fileName),
	})
}

// genericAttachmentName は汎用の口の名前の検査です。
//
// 専用の口があるものは迂回させない（画像＝マジックナンバー検証・EXIF除去、
// PDF＝%PDF- 検証）。**通信箱宛てだけは例外**で、取り込みの受け口が
// 種類ごとの検査を通したうえで引き受ける（1つの口で全部受ける・2026-09-03）。
func genericAttachmentName(pageID, raw string) (string, error) {
	ext := strings.ToLower(filepath.Ext(raw))
	if ext == ".pdf" || allowedImageExts[ext] {
		return "", errors.New("この種類は専用のアップロード口を使ってください（画像・PDF）")
	}
	return SafeAttachmentName(pageID, raw, GenericAttachmentExts(),
		"この拡張子は添付として受け付けていません（許可リストは config/settings.json の attachment_extensions）")
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
