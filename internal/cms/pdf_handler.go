package cms

import (
	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"

	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
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
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// フォームを読む前に本文サイズを制限する（FormValue が内部でパースするため）。
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes())

	pageID := r.FormValue("page_id")
	if pageID == "" {
		http.Error(w, "page_id is required", http.StatusBadRequest)
		return
	}
	// 保存先のパスに使う前にゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	// PDFの追加はページ内容の変更なので write 権限を要求する
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// **先に引き受ける口があれば回す**（汎用の口と同じ扱い・upload_intercept.go）。
	// 通信箱の取り込み係に担当が居るのは `.eml` だけなので、**PDF はここを素通りして
	// 普通の添付**になります（2026-09-05。ユーザー:「通信箱のPDF、DXF取り込みはやめましょう。
	// メモに添付するようにしましょう」）。
	if interceptUpload(w, r, pageID, "pdf_file") {
		return
	}

	// 添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）。
	// 本文編集と同じ編集ロックで直列化する（editlock/handler.go の宣言どおり）。
	// 解析（parse-pdf）は永続状態を変えない（結果はDOMへ足すだけで、保存は
	// /api/save がロック検証する）ので、そちらは通さない。
	if !editlock.RequireEditLock(w, r, pageID) {
		return
	}

	file, header, err := r.FormFile("pdf_file")
	if err != nil {
		http.Error(w, "ファイルを受け取れませんでした（サイズ上限は "+
			strconv.FormatInt(MaxUploadBytes()>>20, 10)+"MiB です）", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 保存する名前を先に確定させる（種類が許可されないなら読み込むまでもない）。
	fileName, err := attachmentFileName(pageID, header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return
	}

	// 拡張子は名乗りにすぎないので、中身がPDFであることも確認する
	// （.pdf という名前のHTMLを置かれると配信側の判定を欺ける）。
	if !bytes.HasPrefix(content, []byte("%PDF-")) {
		http.Error(w, "PDFファイルではありません（先頭が %PDF- ではありません）", http.StatusBadRequest)
		return
	}

	// 保存の作法は1箇所（attachment_save.go）。生成名・上書きの監査まで含む。
	username := ""
	if u := auth.CurrentUser(r); u != nil {
		username = u.Username
	}
	attachID, fileName, saveErr := SaveAttachment(pageID, username, fileName, content)
	if saveErr != nil {
		JSONFail(w, http.StatusInternalServerError, "Failed to save PDF")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"file_name": fileName,
		"src":       fileName,
		"id":        attachID,
		// 配信アドレス（/<ページID>/<生成名>）。クライアントはこれをリンクへ使う
		// （自前でパスを組むと置き場の知識が二重になる）。
		"href": page.AttachmentURLFor(pageID, fileName),
	})
}

// PDF解析の口（ParsePDFHandler・ParsedItem）は 2026-09-16 に `ext/subcon` へ
// 移しました（ext/subcon/parse_pdf.go）。ユーザー:「PDF解析は業務に密着せざるを
// 得ないので、ext/subcon ではないでしょうか？」——プロンプトが「発注書または
// 見積書」と業務を語る口が、コアに残っていた最後の1本でした。
// ここに残るのは**添付としてPDFを置く**口だけです（業務を知りません）。
