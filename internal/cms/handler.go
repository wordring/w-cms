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
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// escapeAttr はHTML属性値（ダブルクォート囲み）に安全に埋め込めるよう、
// 特殊文字をエスケープします。標準 html.EscapeString と同等ですが、本ファイルでは
// ローカル変数 html がパッケージ名を隠すため、最小限の置換器を用意しています。
var attrEscaper = strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&#34;", `'`, "&#39;")

func escapeAttr(s string) string {
	return attrEscaper.Replace(s)
}

// PageMeta は一覧表示用の簡素化されたメタデータ構造体です。
type PageMeta struct {
	ID       string
	Title    string
	FilePath string
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
		// 新規ページ：作成者を所有者としてサイドカーを作成してから同期する
		id = GenerateNextID(database.DB)
		if u := auth.CurrentUser(r); u != nil {
			EnsureSidecar(id, u.Username, u.PrimaryGroup)
		}
	} else {
		// 既存ページ：write権限を要求する
		if !RequirePageWrite(w, r, id) {
			return
		}
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

	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "save", id)
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
	// ページ本文の取得は read 権限を要求する
	if !RequirePageRead(w, r, id) {
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

	// アップロード者を所有者とする権限サイドカーを作成（SyncIndexより前）
	if u := auth.CurrentUser(r); u != nil {
		EnsureSidecar(newID, u.Username, u.PrimaryGroup)
	}

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
	// 一覧表示には親ページの read 権限を要求する（Unixの「ディレクトリの読み取り」に相当）
	if !RequirePageRead(w, r, parentID) {
		return
	}
	user := auth.CurrentUser(r)

	rows, err := database.DB.Query("SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC", parentIDInt)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 各子ページのうち、閲覧者が read 権限を持つものだけを返す
	pages := make([]PageMeta, 0)
	for rows.Next() {
		var p PageMeta
		var idInt int
		if err := rows.Scan(&idInt, &p.Title); err == nil {
			if GetPerms(idInt).CanRead(user) {
				p.ID = fmt.Sprintf("%0*d", IDLength, idInt)
				pages = append(pages, p)
			}
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
		// 子ページの作成には親ページの write 権限を要求する
		if !RequirePageWrite(w, r, parentIDStr) {
			return
		}
	} else {
		// 親なし（トップレベル）ページの作成は admin のみ
		if !RequireAdmin(w, r) {
			return
		}
	}
	creator := auth.CurrentUser(r)

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

	// 3. デフォルトHTMLを構築。
	//    ページ属性は文書先頭の <m-page-info> に集約する。作成日時・作成者は
	//    サーバーがここで1回だけ刻む（以後の改竄は保存APIが復元する）。
	//    親ページIDは <m-page-info> に内包する <m-tag name="親ページID"> で表す。
	var htmlBuilder strings.Builder
	createdAt := time.Now().UTC().Format(time.RFC3339)
	createdBy := ""
	if creator != nil {
		createdBy = creator.Username
	}
	fmt.Fprintf(&htmlBuilder,
		"<m-page-info created-at=\"%s\" created-by=\"%s\">\n", createdAt, escapeAttr(createdBy))
	if parentID.Valid {
		parentStr := fmt.Sprintf("%0*d", IDLength, parentID.Int64)
		fmt.Fprintf(&htmlBuilder,
			"  <m-tag name=\"親ページID\" value=\"%s\"></m-tag>\n", parentStr)
	}
	htmlBuilder.WriteString("</m-page-info>\n")
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

	// 4-2. 権限サイドカーを作成（作成者が所有者。SyncIndexより前に作る）
	if creator != nil {
		EnsureSidecar(newID, creator.Username, creator.PrimaryGroup)
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
	// 全再構築は admin のみ
	if !RequireAdmin(w, r) {
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
			// トップページは全員が閲覧できるよう other に read を付与（owner rw / other r）。
			// 書き込みは admin（owner）のみ。
			WriteSidecar("000000", PagePerms{Owner: defaultOwner, Mode: "302"})
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
