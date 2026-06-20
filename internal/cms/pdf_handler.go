package cms

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// UploadPDFHandler はドラッグ＆ドロップされたPDFを該当ページIDのフォルダに保存します
func UploadPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pageID := r.FormValue("page_id")
	if pageID == "" {
		http.Error(w, "page_id is required", http.StatusBadRequest)
		return
	}
	// PDFの追加はページ内容の変更なので write 権限を要求する
	if !RequirePageWrite(w, r, pageID) {
		return
	}

	r.ParseMultipartForm(32 << 20)
	file, header, err := r.FormFile("pdf_file")
	if err != nil {
		http.Error(w, "File upload error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return
	}

	pageDir := GetPageDir(pageID)
	os.MkdirAll(pageDir, 0755)

	// 安全なファイル名を作成
	fileName := filepath.Base(header.Filename)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid request body"})
		return
	}
	// PDF解析→明細挿入はページ内容の変更につながるため write 権限を要求する
	if !RequirePageWrite(w, r, req.PageID) {
		return
	}

	pageDir := GetPageDir(req.PageID)
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
