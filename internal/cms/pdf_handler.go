package cms

import (
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// maxUploadBytes は添付1件あたりの上限（32MiB）です。
// 上限が無いとリクエストボディを丸ごとメモリへ読み込んでしまい、認証済みの
// 利用者がメモリを枯渇させられます。
const maxUploadBytes = 32 << 20

// allowedAttachmentExts は添付として保存を許す拡張子です。
//
// **拡張子を絞ることが要です。** ページのディレクトリ（data/master/xx/<id>/）には
// 本文 <id>.html と属性サイドカー <id>.meta.json が同居しているため、任意の名前・
// 種類で書き込めると次の2つが成立してしまいます:
//
//   - **権限昇格**: <id>.meta.json を上書きして owner / mode / public を書き換える。
//     サイドカーは権限の正本であり、次の同期で page_perms へ反映される。write 権限
//     しか持たない利用者が、本来 admin だけの chown や所有者だけの公開操作を行えてしまう。
//   - **保存型XSS**: .html や .svg を置き、/data/master/... 経由で**同一オリジンの
//     HTMLとして**配信させる。本文の保存時サニタイズ（docs/本文サニタイズ設計.md）を
//     完全に迂回でき、中間版CSPの script-src 'unsafe-inline' はインラインを許すため
//     スクリプトが動く。
//
// 配信側（page.DataFileHandler）にも多層防御を入れてあるが、そもそも置かせないのが第一。
var allowedAttachmentExts = map[string]bool{".pdf": true}

// attachmentFileName は受け取ったファイル名を、ページのディレクトリ内へ安全に置ける
// 名前へ正規化します。使えない名前なら理由つきのエラーを返します。
func attachmentFileName(pageID, raw string) (string, error) {
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
	if !allowedAttachmentExts[strings.ToLower(filepath.Ext(name))] {
		return "", errors.New("PDFファイル（.pdf）のみアップロードできます")
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

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
		http.Error(w, "ファイルを受け取れませんでした（サイズ上限は32MiBです）", http.StatusBadRequest)
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

	pageDir := page.GetPageDir(pageID)
	os.MkdirAll(pageDir, 0755)

	savePath := filepath.Join(pageDir, fileName)

	if err := os.WriteFile(savePath, content, 0644); err != nil {
		http.Error(w, "Failed to save PDF", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"file_name": fileName,
		"src":       fileName,
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
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Method not allowed"})
		return
	}

	var req struct {
		PageID   string `json:"page_id"`
		FileName string `json:"file_name"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	// パスに使う前にゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	normID, ok := page.NormalizeID(req.PageID)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "ページIDが不正です"})
		return
	}
	req.PageID = normID
	// PDF解析→明細挿入はページ内容の変更につながるため write 権限を要求する
	if !page.RequirePageWrite(w, r, req.PageID) {
		return
	}

	pageDir := page.GetPageDir(req.PageID)
	pdfPath := filepath.Join(pageDir, filepath.Base(req.FileName))

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "PDF file not found on server"})
		return
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		// APIキーがない場合はフロント側に分かりやすいエラーメッセージを返す
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "サーバーに GEMINI_API_KEY 環境変数が設定されていません。\nターミナルで設定してから起動してください。\n\n例(Windows): \nset GEMINI_API_KEY=AIzaSy...\ngo run ./cmd/w-cms/",
		})
		return
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Geminiクライアントの作成に失敗しました: " + err.Error()})
		return
	}
	defer client.Close()

	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "PDFファイルの読み込みに失敗しました"})
		return
	}

	model := client.GenerativeModel("gemini-3.5-flash")

	prompt := genai.Text(`このPDFは発注書または見積書です。
記載されているすべての部品明細（品名、単価、数量）を抽出し、以下の形式のJSON配列のみを出力してください。
キーは必ず "item_name", "price", "quantity" にしてください。
単価はカンマを除いた数値文字列にしてください。
マークダウンのコードブロック修飾 (例: ` + "```json" + ` ) は付けず、純粋なJSON配列から出力してください。

[
  {"item_name": "部品A", "price": "1000", "quantity": "2"}
]`)

	pdfBlob := genai.Blob{
		MIMEType: "application/pdf",
		Data:     pdfBytes,
	}

	resp, err := model.GenerateContent(ctx, pdfBlob, prompt)
	if err != nil {
		log.Printf("[Gemini API Error] %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Gemini APIの呼び出しに失敗しました: " + err.Error(),
		})
		return
	}

	var respText string
	if len(resp.Candidates) > 0 {
		for _, part := range resp.Candidates[0].Content.Parts {
			if t, ok := part.(genai.Text); ok {
				respText += string(t)
			}
		}
	}

	cleanJson := strings.TrimSpace(respText)
	if strings.HasPrefix(cleanJson, "```json") {
		cleanJson = strings.TrimPrefix(cleanJson, "```json")
		cleanJson = strings.TrimSuffix(cleanJson, "```")
		cleanJson = strings.TrimSpace(cleanJson)
	}

	var items []ParsedItem
	err = json.Unmarshal([]byte(cleanJson), &items)
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
