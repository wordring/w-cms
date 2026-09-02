package cms

import (
	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"

	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"
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

	// 添付は files/ サブフォルダへ（正本と同居させない——構造で塞ぐ。storage.go）。
	attachDir := page.AttachmentDir(pageID)
	os.MkdirAll(attachDir, 0755)

	// 保存名はサーバーが生成する（元の名前はURLに出さない。表示は本文のリンク文字が担う）。
	// 生成IDはリンクブロックの data-id と一致させる（storage.go の3役）。
	attachID := page.GeneratedAttachmentID(pageID, strings.ToLower(filepath.Ext(fileName)))
	fileName = attachID + strings.ToLower(filepath.Ext(fileName))
	savePath := filepath.Join(attachDir, fileName)

	// 上書きかどうかは書く前にしか分からない。添付はリビジョンもゴミ箱も無く
	// 上書きが復元できないので、「増えた」のか「消えた」のかを記録で区別する
	// （要件定義書 §2.3）。
	overwrote := false
	if _, err := os.Stat(savePath); err == nil {
		overwrote = true
	}

	if err := page.WriteFileAtomic(savePath, content, 0644); err != nil {
		http.Error(w, "Failed to save PDF", http.StatusInternalServerError)
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
		"src":       fileName,
		"id":        attachID,
		// 配信アドレス（/<ページID>/<生成名>）。クライアントはこれをリンクへ使う
		// （自前でパスを組むと置き場の知識が二重になる）。
		"href": page.AttachmentURLFor(pageID, fileName),
	})
}

// ParsedItem はPDFから抽出された品目データです
type ParsedItem struct {
	ItemName string `json:"item_name"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// ParsePDFHandler は保存されたPDFをGemini APIに渡し、JSONとして明細を抽出します
func ParsePDFHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		PageID   string `json:"page_id"`
		FileName string `json:"file_name"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	// パスに使う前にゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	normID, ok := page.NormalizeID(req.PageID)
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	req.PageID = normID
	// PDF解析→明細挿入はページ内容の変更につながるため write 権限を要求する
	if !page.RequirePageWrite(w, r, req.PageID) {
		return
	}

	// 送る前に名前を検証する。ここが filepath.Base だけだったころ、ページ
	// ディレクトリ内の任意のファイル——**本文 <id>.html と権限サイドカー
	// <id>.meta.json を含む**——を「PDFとして」外部（Gemini）へ送れた。
	// 置く側（UploadPDFHandler）と同じ関門を通し、拡張子の許可リストと
	// 本文・サイドカーの名指し拒否をそのまま効かせる。
	fileName, err := attachmentFileName(req.PageID, req.FileName)
	if err != nil {
		JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	pdfPath, found := page.AttachmentPath(req.PageID, fileName)
	if !found {
		JSONFail(w, http.StatusNotFound, "PDF file not found on server")
		return
	}
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		JSONFail(w, 0, "PDFファイルの読み込みに失敗しました")
		return
	}

	prompt := `このPDFは発注書または見積書です。
記載されているすべての部品明細（品名、単価、数量）を抽出し、以下の形式のJSON配列のみを出力してください。
キーは必ず "item_name", "price", "quantity" にしてください。
単価はカンマを除いた数値文字列にしてください。
マークダウンのコードブロック修飾 (例: ` + "```json" + ` ) は付けず、純粋なJSON配列から出力してください。

[
  {"item_name": "部品A", "price": "1000", "quantity": "2"}
]`

	respText, err := GeminiGenerate(prompt, genai.Blob{MIMEType: "application/pdf", Data: pdfBytes})
	if err != nil {
		if errors.Is(err, ErrNoGeminiKey) {
			// APIキーがない場合はフロント側に分かりやすいエラーメッセージを返す
			JSONFail(w, 0, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。\nターミナルで設定してから起動してください。\n\n例(Windows): \nset GEMINI_API_KEY=AIzaSy...\ngo run ./cmd/w-cms/")
			return
		}
		log.Printf("[Gemini API Error] %v", err)
		JSONFail(w, 0, "Gemini APIの呼び出しに失敗しました: "+err.Error())
		return
	}

	var items []ParsedItem
	err = json.Unmarshal([]byte(StripJSONFence(respText)), &items)
	if err != nil {
		// パース失敗時はエラーではなく空配列を返す（フロント側でダミー追加ロジックが走るため）
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"items":   []ParsedItem{},
			"raw":     respText,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"items":   items,
		"raw":     respText,
	})
}
