package cms

import (
	"bytes"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"w-cms/internal/cms/page"
)

// 添付1件あたりの上限は設定 max_upload_mib（既定32MiB・cms.MaxUploadBytes）。
// 上限が無いとリクエストボディを丸ごとメモリへ読み込んでしまい、認証済みの
// 利用者がメモリを枯渇させられます。

// allowedAttachmentExts は添付として保存を許す拡張子です。
//
// かつて添付は正本（<id>.html・<id>.meta.json）と同じフォルダに置かれ、拡張子の
// 許可リストだけが「サイドカーを上書きして権限を書き換える」「.html/.svg を同一
// オリジンで配信させる」を防いでいた。2026-08-31 に添付を files/ サブフォルダへ
// 分けてからは、その穴は**構造で**塞がっている（page/storage.go AttachmentsDirName）。
// 拡張子の絞り込みは以後、安全の門ではなく運用の方針——この口は PDF 専用。
// 配信側（setAttachmentHeaders）にも多層防御があり、未知の種別は解釈させない。
var allowedAttachmentExts = map[string]bool{".pdf": true}

// attachmentFileName は受け取ったファイル名を、ページのディレクトリ内へ安全に置ける
// 名前へ正規化します（PDF の口はこちら）。使えない名前なら理由つきのエラーを返します。
func attachmentFileName(pageID, raw string) (string, error) {
	return SafeAttachmentName(pageID, raw, allowedAttachmentExts,
		"PDFファイル（.pdf）のみアップロードできます")
}

// SafeAttachmentName はファイル名の正規化と検査の本体です。許可する拡張子の集合を
// 受け取るので、PDF の口（allowedAttachmentExts）と画像の口（allowedImageExts）で
// 共有できます。**名前の守りは1箇所**にしておかないと、口を増やすたびに
// 「サイドカーを上書きできる穴」が復活します。
func SafeAttachmentName(pageID, raw string, allowed map[string]bool, extError string) (string, error) {
	// パス要素を落とす。filepath.Base は実行中のOSの区切りしか見ないため、
	// Linux上での "..\\..\\evil.pdf" のような名前に備えて両方の区切りで切る。
	name := raw
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)

	if name == "" || name == "." || name == ".." {
		return "", errors.New("ファイル名が不正です")
	}
	if strings.HasPrefix(name, ".") {
		return "", errors.New("ドットで始まるファイル名は使用できません")
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return "", errors.New("ファイル名に制御文字は使用できません")
		}
	}
	if !allowed[strings.ToLower(filepath.Ext(name))] {
		return "", errors.New(extError)
	}
	// 本文と属性サイドカーは添付として上書きさせない。拡張子の許可リストを将来
	// 広げたときにも効くよう、ここで名指しで守る。
	if strings.EqualFold(name, pageID+".html") || strings.EqualFold(name, pageID+".meta.json") {
		return "", errors.New("この名前のファイルは使用できません")
	}
	return name, nil
}

// UploadPDFHandler はドラッグ＆ドロップされたPDFを該当ページIDのフォルダに保存します
func UploadPDFHandler(w http.ResponseWriter, r *http.Request) {
	// 入口は3本共通（upload_common.go）。**先に引き受ける口があれば回します**
	// （汎用の口と同じ扱い・upload_intercept.go）——通信箱の取り込み係に担当が居るのは
	// `.eml` だけなので、**PDF はここを素通りして普通の添付**になります（2026-09-05。
	// ユーザー:「通信箱のPDF、DXF取り込みはやめましょう。メモに添付するようにしましょう」）。
	// 解析（parse-pdf）は永続状態を変えない（結果はDOMへ足すだけで、保存は
	// /api/save がロック検証する）ので、そちらは編集ロックを通しません。
	up, ok := openUpload(w, r, "pdf_file", true, attachmentFileName)
	if !ok {
		return
	}

	// 拡張子は名乗りにすぎないので、中身がPDFであることも確認する
	// （.pdf という名前のHTMLを置かれると配信側の判定を欺ける）。
	if !bytes.HasPrefix(up.content, []byte("%PDF-")) {
		http.Error(w, "PDFファイルではありません（先頭が %PDF- ではありません）", http.StatusBadRequest)
		return
	}

	// 保存の作法は1箇所（attachment_save.go）。生成名・上書きの監査まで含む。
	attachID, fileName, saveErr := SaveAttachment(up.pageID, up.username, up.fileName, up.content)
	if saveErr != nil {
		JSONFail(w, http.StatusInternalServerError, "Failed to save PDF")
		return
	}

	WriteJSON(w, map[string]any{
		"success":   true,
		"file_name": fileName,
		"src":       fileName,
		"id":        attachID,
		// 配信アドレス（/<ページID>/<生成名>）。クライアントはこれをリンクへ使う
		// （自前でパスを組むと置き場の知識が二重になる）。
		"href": page.AttachmentURLFor(up.pageID, fileName),
	})
}

// PDF解析の口（ParsePDFHandler・ParsedItem）は 2026-09-16 に `ext/subcon` へ
// 移しました（ext/subcon/parse_pdf.go）。ユーザー:「PDF解析は業務に密着せざるを
// 得ないので、ext/subcon ではないでしょうか？」——プロンプトが「発注書または
// 見積書」と業務を語る口が、コアに残っていた最後の1本でした。
// ここに残るのは**添付としてPDFを置く**口だけです（業務を知りません）。
