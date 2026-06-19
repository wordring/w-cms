package cms

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"w-cms/internal/database"
)

// PageMeta は一覧表示用の簡素化されたメタデータ構造体です。
type PageMeta struct {
	ID       string
	Title    string
	FilePath string
}

// RequiredMaterialResponse は部材手配の進捗状況を返却するためのJSON構造体です。
type RequiredMaterialResponse struct {
	MaterialName  string `json:"material_name"`
	SupplierName  string `json:"supplier_name"`
	Cost          int    `json:"cost"`
	TotalRequired int    `json:"total_required"`
	Ordered       int    `json:"ordered"`
	Remaining     int    `json:"remaining"`
}

// RequiredMaterialsAPIHandler は指定されたpage_id(受注ページ)に紐づく部材の要手配数・発注済数を集計して返却します。
func RequiredMaterialsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pageID := r.URL.Query().Get("page_id")
	if pageID == "" {
		http.Error(w, "Missing page_id parameter", http.StatusBadRequest)
		return
	}

	pageIDInt, err := strconv.Atoi(pageID)
	if err != nil {
		http.Error(w, "Invalid page_id format", http.StatusBadRequest)
		return
	}

	// 1. そのページ内の受注 client_orders の明細を取得する
	rows, err := database.DB.Query(`
		SELECT item_id, quantity 
		FROM client_order_items 
		WHERE order_no IN (
			SELECT order_no FROM client_orders WHERE page_id = ?
		)
	`, pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type orderItem struct {
		ItemID   string
		Quantity int
	}
	var clientItems []orderItem
	for rows.Next() {
		var item orderItem
		if err := rows.Scan(&item.ItemID, &item.Quantity); err == nil {
			clientItems = append(clientItems, item)
		}
	}

	// 2. 各受注部品に対し、必要な部材の定義 part_materials を取得し、総必要数を集計する
	materialsMap := make(map[string]*RequiredMaterialResponse)

	for _, item := range clientItems {
		matRows, err := database.DB.Query(`
			SELECT material_name, cost, supplier_name, quantity 
			FROM part_materials 
			WHERE part_id = ?
		`, item.ItemID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for matRows.Next() {
			var matName, supplierName string
			var cost, unitQty int
			if err := matRows.Scan(&matName, &cost, &supplierName, &unitQty); err == nil {
				totalReq := unitQty * item.Quantity
				if existing, ok := materialsMap[matName]; ok {
					existing.TotalRequired += totalReq
				} else {
					materialsMap[matName] = &RequiredMaterialResponse{
						MaterialName:  matName,
						SupplierName:  supplierName,
						Cost:          cost,
						TotalRequired: totalReq,
						Ordered:       0,
					}
				}
			}
		}
		matRows.Close()
	}

	// 3. 同じ page_id に紐づく弊社の発注実績 our_orders の明細を取得し、発注済数を集計する
	ourRows, err := database.DB.Query(`
		SELECT ooi.item_name, ooi.quantity, oo.supplier_name 
		FROM our_order_items ooi
		JOIN our_orders oo ON ooi.order_no = oo.order_no
		WHERE oo.page_id = ?
	`, pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer ourRows.Close()

	for ourRows.Next() {
		var itemName, supplierName string
		var quantity int
		if err := ourRows.Scan(&itemName, &quantity, &supplierName); err == nil {
			if existing, ok := materialsMap[itemName]; ok {
				existing.Ordered += quantity
			} else {
				materialsMap[itemName] = &RequiredMaterialResponse{
					MaterialName:  itemName,
					SupplierName:  supplierName,
					Cost:          0,
					TotalRequired: 0,
					Ordered:       quantity,
				}
			}
		}
	}

	// 4. 残要注文数を算出し、スライスに変換
	list := make([]RequiredMaterialResponse, 0)
	for _, m := range materialsMap {
		m.Remaining = m.TotalRequired - m.Ordered
		if m.Remaining < 0 {
			m.Remaining = 0
		}
		list = append(list, *m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// SaveRequest はオートセーブで送られてくるJSON構造体です。
type SaveRequest struct {
	PageID string `json:"page_id"`
	HTML   string `json:"html"`
}

// SaveAPIHandler はエディタからの自動保存（JSON）を受け取り、HTMLファイルとDB同期を上書き保存します。
func SaveAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	id := req.PageID
	if id == "" {
		id = GenerateNextID(database.DB)
	}

	pageDir := GetPageDir(id)
	os.MkdirAll(pageDir, 0755)

	htmlPath := filepath.Join(pageDir, id+".html")
	if err := os.WriteFile(htmlPath, []byte(req.HTML), 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	if err := SyncIndex(id, req.HTML); err != nil {
		log.Printf("SyncIndex failed for page %s: %v\n", id, err)
		http.Error(w, "Failed to sync database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"page_id": id,
	})
}

// LoadAPIHandler は指定されたpage_idのHTMLファイルを読み込んで返却します。
func LoadAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}

	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "Invalid id format", http.StatusBadRequest)
		return
	}

	var filePath string
	err = database.DB.QueryRow("SELECT file_path FROM pages WHERE id = ?", idInt).Scan(&filePath)
	if err != nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// UploadHandler はブラウザからのファイルアップロードリクエストを受け取り、保存と同期を行います。
func UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	r.ParseMultipartForm(32 << 20)
	file, _, err := r.FormFile("html_page")
	if err != nil {
		http.Error(w, "File upload error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	content, _ := io.ReadAll(file)

	newID := GenerateNextID(database.DB)
	pageDir := GetPageDir(newID)

	os.MkdirAll(pageDir, 0755)

	htmlPath := filepath.Join(pageDir, newID+".html")
	os.WriteFile(htmlPath, content, 0644)

	SyncIndex(newID, string(content))

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ChildPagesAPIHandler は指定された親ページIDを持つ子ページの一覧を返します。
func ChildPagesAPIHandler(w http.ResponseWriter, r *http.Request) {
	parentID := r.URL.Query().Get("parent_id")
	if parentID == "" {
		http.Error(w, "Missing parent_id", http.StatusBadRequest)
		return
	}

	parentIDInt, err := strconv.Atoi(parentID)
	if err != nil {
		http.Error(w, "Invalid parent_id format", http.StatusBadRequest)
		return
	}

	rows, err := database.DB.Query("SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC", parentIDInt)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var pages []PageMeta
	for rows.Next() {
		var p PageMeta
		var idInt int
		if err := rows.Scan(&idInt, &p.Title); err == nil {
			p.ID = fmt.Sprintf("%0*d", IDLength, idInt)
			pages = append(pages, p)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pages)
}

// NewPageAPIHandler はサーバー側で新しいページを作成し、そのページへリダイレクトします。
func NewPageAPIHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 親ページIDの取得
	parentIDStr := r.URL.Query().Get("parent")
	var parentID sql.NullInt64
	if parentIDStr != "" {
		pid, err := strconv.Atoi(parentIDStr)
		if err != nil {
			http.Error(w, "Invalid parent ID", http.StatusBadRequest)
			return
		}
		parentID = sql.NullInt64{Int64: int64(pid), Valid: true}
	}

	// 2. DBにページレコードを挿入（IDはSQLiteが自動採番）
	result, err := database.DB.Exec(
		`INSERT INTO pages (title, parent_id, file_path) VALUES (?, ?, '')`,
		"新しいページ", parentID,
	)
	if err != nil {
		http.Error(w, "Failed to create page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	newIDInt, _ := result.LastInsertId()
	newID := fmt.Sprintf("%0*d", IDLength, newIDInt)

	// 3. デフォルトHTMLを構築（親ページIDタグを含む）
	var htmlBuilder strings.Builder
	if parentID.Valid {
		parentStr := fmt.Sprintf("%0*d", IDLength, parentID.Int64)
		fmt.Fprintf(&htmlBuilder,
			"<m-tag name=\"親ページID\" value=\"%s\"></m-tag>\n", parentStr)
	}
	htmlBuilder.WriteString("<h1>新しいページ</h1>\n")
	htmlBuilder.WriteString("<p>ここから編集を始めてください。</p>\n")
	htmlBuilder.WriteString("<h2>子ページ一覧</h2>\n")
	htmlBuilder.WriteString("<m-child-list></m-child-list>")
	html := htmlBuilder.String()

	// 4. HTMLファイルを物理保存
	pageDir := GetPageDir(newID)
	os.MkdirAll(pageDir, 0755)
	htmlPath := filepath.Join(pageDir, newID+".html")
	if err := os.WriteFile(htmlPath, []byte(html), 0644); err != nil {
		http.Error(w, "Failed to write file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 5. DB同期（タグなどのインデックス更新）
	if err := SyncIndex(newID, html); err != nil {
		log.Printf("SyncIndex failed for new page %s: %v\n", newID, err)
	}

	// 6. 新しいページへリダイレクト
	http.Redirect(w, r, "/"+newID+"?edit=true", http.StatusFound)
}

// RebuildDBAPIHandler は、HTMLファイルからデータベースを完全に再構築します。
func RebuildDBAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := RebuildDatabase()
	if err != nil {
		http.Error(w, "Rebuild error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// RootHandler はWiki型のルーティングを担当します。
func RootHandler(w http.ResponseWriter, r *http.Request) {
	// `/assets/` などの静的ファイルは既に mux で処理されている前提
	id := r.URL.Path[1:] // 先頭の `/` を取り除く
	if id == "" || id == "index.html" {
		id = "000000"
	}

	// 初回起動時の 000000 ページ自動生成
	if id == "000000" {
		var exists bool
		database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = 0)").Scan(&exists)
		if !exists {
			defaultHTML := `<h1>w-cms Wiki トップページ</h1>
<p>ここはすべての起点となるトップページです。</p>
<p>右上のスイッチで「編集モード」に切り替えると、Notionのようにブロックベースで編集できます。</p>
<h2>子ページ一覧</h2>
<m-child-list></m-child-list>`
			
			pageDir := GetPageDir("000000")
			os.MkdirAll(pageDir, 0755)
			htmlPath := filepath.Join(pageDir, "000000.html")
			os.WriteFile(htmlPath, []byte(defaultHTML), 0644)
			SyncIndex("000000", defaultHTML)
		}
	}

	// id が英数字ハイフンのみか簡易チェック
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			http.NotFound(w, r)
			return
		}
	}

	http.ServeFile(w, r, "assets/index.html")
}
