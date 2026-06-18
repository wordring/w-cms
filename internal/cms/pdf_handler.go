package cms

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// UploadPDFHandler はドラッグ＆ドロップされたPDFを該当ページIDのフォルダに保存します
func UploadPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pageID := r.FormValue("page_id")
	if pageID == "" {
		// page_idが未確定の場合は、とりあえず新規発番するか、フロント側で保存してからアップロードさせる
		// 今回は、フロント側でオートセーブして page_id を確定させてからアップロードする設計とします。
		http.Error(w, "page_id is required", http.StatusBadRequest)
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

// ParsedItem はPDFから推測された品目データです
type ParsedItem struct {
	ItemName string `json:"item_name"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// ParsePDFHandler は保存されたPDFを開き、簡易的なテキスト抽出と正規表現解析を行います
func ParsePDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PageID   string `json:"page_id"`
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	pageDir := GetPageDir(req.PageID)
	pdfPath := filepath.Join(pageDir, filepath.Base(req.FileName))

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		http.Error(w, "PDF file not found", http.StatusNotFound)
		return
	}

	// ledongthuc/pdf を使ってテキスト抽出
	text, err := extractPDFText(pdfPath)
	if err != nil {
		// 失敗した場合は空配列を返す
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"items":   []ParsedItem{},
			"raw":     "",
		})
		return
	}

	// 簡易的な解析ロジック（本来はAI等の高度な処理が入る部分）
	items := parseDummyItems(text)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"items":   items,
		"raw":     text, // デバッグ用に生のテキストも返す
	})
}

func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	buf.ReadFrom(b)
	return buf.String(), nil
}

func parseDummyItems(rawText string) []ParsedItem {
	var items []ParsedItem
	lines := strings.Split(rawText, "\n")

	// 簡単な推測ロジック:
	// 金額らしきもの（〇〇円）や数量らしきもの（〇〇個）がある行を品目とみなす
	// ※あくまでプロトタイプ実装です
	priceRegex := regexp.MustCompile(`(\d{1,3}(,\d{3})*|\d+)\s*円`)
	qtyRegex := regexp.MustCompile(`(\d+)\s*[個台本枚]`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		priceMatch := priceRegex.FindStringSubmatch(line)
		qtyMatch := qtyRegex.FindStringSubmatch(line)

		if priceMatch != nil || qtyMatch != nil {
			// 品名推測: 数字や記号を取り除いた文字列の先頭部分
			name := line
			if priceMatch != nil {
				name = strings.Replace(name, priceMatch[0], "", 1)
			}
			if qtyMatch != nil {
				name = strings.Replace(name, qtyMatch[0], "", 1)
			}
			// さらに不要な空白やカンマを削除
			name = strings.TrimSpace(strings.ReplaceAll(name, ",", ""))
			if len(name) > 30 {
				name = name[:30] + "..."
			}
			if name == "" {
				name = "不明な部品"
			}

			price := "0"
			if priceMatch != nil {
				price = strings.ReplaceAll(priceMatch[1], ",", "")
			}

			qty := "1"
			if qtyMatch != nil {
				qty = qtyMatch[1]
			}

			items = append(items, ParsedItem{
				ItemName: name,
				Price:    price,
				Quantity: qty,
			})
		}
	}

	return items
}
