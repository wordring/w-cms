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
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

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

// pageInfoOpenRe は <m-page-info ...> の開始タグ全体（属性部分をキャプチャ）にマッチします。
var pageInfoOpenRe = regexp.MustCompile(`(?is)<m-page-info\b([^>]*)>`)

// pageInfoAttrRe は name="value" 形式の属性（ダブルクォート）にマッチします。
var pageInfoAttrRe = regexp.MustCompile(`([a-zA-Z][\w-]*)\s*=\s*"([^"]*)"`)

// setPageInfoAttrs は保存対象HTML中の <m-page-info> 開始タグについて、
// サーバー権限属性（created-at / created-by / updated-at）を与えられたサーバー値で
// 強制的に設定して返します。クライアント由来の同名属性は破棄します（改竄防止）。
// created-at / created-by は空文字なら属性を出力しません（作成情報が無い既存ページ用）。
// updated-at は常に設定します。<m-page-info> が無ければ文書先頭に新設します。
func setPageInfoAttrs(htmlContent, createdAt, createdBy, updatedAt string) string {
	build := func(existing string) string {
		var attrs []string
		// 既存属性のうち、サーバー権限フィールド以外はそのまま保持する。
		for _, m := range pageInfoAttrRe.FindAllStringSubmatch(existing, -1) {
			switch strings.ToLower(m[1]) {
			case "created-at", "created-by", "updated-at":
				// サーバー値で置き換えるため破棄
			default:
				attrs = append(attrs, fmt.Sprintf(`%s="%s"`, m[1], m[2]))
			}
		}
		if createdAt != "" {
			attrs = append(attrs, fmt.Sprintf(`created-at="%s"`, escapeAttr(createdAt)))
		}
		if createdBy != "" {
			attrs = append(attrs, fmt.Sprintf(`created-by="%s"`, escapeAttr(createdBy)))
		}
		attrs = append(attrs, fmt.Sprintf(`updated-at="%s"`, escapeAttr(updatedAt)))
		return "<m-page-info " + strings.Join(attrs, " ") + ">"
	}

	if loc := pageInfoOpenRe.FindStringSubmatchIndex(htmlContent); loc != nil {
		existing := htmlContent[loc[2]:loc[3]]
		return htmlContent[:loc[0]] + build(existing) + htmlContent[loc[1]:]
	}
	// <m-page-info> が無い既存・旧HTMLには先頭に新設する。
	return build("") + "</m-page-info>\n" + htmlContent
}

// PageSummary は一覧表示用の簡素化されたメタデータ構造体です。
type PageSummary struct {
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

	// サーバー権限フィールド（作成日時・作成者・更新日時）の権威値を決める。
	// クライアントが送ってきたHTML内の値は信用せず、後でサーバー値で上書きする（改竄防止）。
	id := req.PageID
	var createdAt, createdBy string
	if id == "" {
		// 新規ページ：作成者を所有者としてサイドカーを作成してから同期する。
		// 作成日時＝今、作成者＝現在のユーザー（サーバーが1回だけ刻む）。
		id = GenerateNextID(database.DB)
		if u := auth.CurrentUser(r); u != nil {
			EnsureSidecar(id, u.Username, u.PrimaryGroup)
			createdBy = u.Username
		}
		createdAt = time.Now().UTC().Format(time.RFC3339)
	} else {
		// 既存ページ：write権限を要求する
		if !RequirePageWrite(w, r, id) {
			return
		}
		// 作成日時・作成者はDB（＝作成時にサーバーが刻んだ値）を正とし、復元する。
		if idInt, err := strconv.Atoi(id); err == nil {
			var ca, cb sql.NullString
			database.DB.QueryRow("SELECT created_at, created_by FROM pages WHERE id = ?", idInt).Scan(&ca, &cb)
			createdAt, createdBy = ca.String, cb.String
		}
	}
	// 更新日時は保存のたびにサーバーが「今」を刻む。
	updatedAt := time.Now().UTC().Format(time.RFC3339)

	// 親ページIDが変更される場合のみ、変更先の妥当性を検証する（実在・循環・write権限）。
	// 親が変わらない通常の保存では検証しない（親にwrite権限が無くても自分のページは保存できる）。
	idInt, _ := strconv.Atoi(id)
	newParent := parseParentID(req.HTML)
	var oldParent sql.NullInt64
	database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", idInt).Scan(&oldParent)
	if parentChanged(oldParent, newParent) {
		if msg, code := validateParentChange(auth.CurrentUser(r), idInt, newParent); code != 0 {
			http.Error(w, msg, code)
			return
		}
	}

	// 受け取ったHTMLの <m-page-info> をサーバー権威値で上書きしてから永続化する。
	htmlOut := setPageInfoAttrs(req.HTML, createdAt, createdBy, updatedAt)

	pageDir := GetPageDir(id)
	os.MkdirAll(pageDir, 0755)

	htmlPath := filepath.Join(pageDir, id+".html")
	if err := os.WriteFile(htmlPath, []byte(htmlOut), 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	if err := SyncIndex(id, htmlOut); err != nil {
		log.Printf("SyncIndex failed for page %s: %v\n", id, err)
		http.Error(w, "Failed to sync database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "save", id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"page_id":    id,
		"updated_at": updatedAt,
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
	pages := make([]PageSummary, 0)
	for rows.Next() {
		var p PageSummary
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

// parseParentID は保存対象HTMLから親ページID（<m-tag name="親ページID"> の値）を取り出します。
// 無ければ空文字を返します。
func parseParentID(htmlContent string) string {
	root, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return ""
	}
	return ParseCore(root).ParentID
}

// parentChanged は、DB上の現在の親（old）と新しい親文字列（newStr）が異なるかを返します。
// 親が変わらない通常の保存では検証をスキップするための判定です。
func parentChanged(old sql.NullInt64, newStr string) bool {
	newNorm := -1 // 親なし
	if newStr != "" {
		if v, err := strconv.Atoi(newStr); err == nil {
			newNorm = v
		} else {
			newNorm = -2 // 数値でない不正値。必ず検証を走らせて弾く
		}
	}
	oldNorm := -1
	if old.Valid {
		oldNorm = int(old.Int64)
	}
	return newNorm != oldNorm
}

// parentCreatesCycle は、ページ childID の親を newParentID にすると木に循環が生じるかを返します。
// newParentID から parent_id チェーンを上にたどり、childID に到達すれば循環です。
// 既存データの破損による無限ループに備え、探索回数に上限を設けます。
func parentCreatesCycle(childID, newParentID int) bool {
	cur := newParentID
	for i := 0; i < 10000; i++ {
		if cur == childID {
			return true
		}
		var parent sql.NullInt64
		if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", cur).Scan(&parent); err != nil || !parent.Valid {
			return false
		}
		cur = int(parent.Int64)
	}
	return false
}

// validateParentChange は、ページ childID の親を newParentStr に変更する操作の妥当性を検証します。
// 呼び出し側は「親が実際に変わるとき」だけ呼ぶこと（不変の保存では検証しない）。
// 妥当なら ("", 0)、不正なら (ユーザー向けメッセージ, HTTPステータス) を返します。
//
// ルール（子ページ作成 NewPageAPIHandler と同じポリシー）:
//   - 親を空（トップレベル）にするには admin 権限が必要
//   - 親IDは数値かつ実在するページであること
//   - 自分自身や自分の子孫を親に指定できない（循環防止）
//   - 新しい親ページへの write 権限が必要
func validateParentChange(user *auth.User, childID int, newParentStr string) (string, int) {
	if user == nil {
		return "認証が必要です", http.StatusUnauthorized
	}
	if newParentStr == "" {
		if !user.IsAdmin {
			return "親なし（トップレベル）に変更するには管理者権限が必要です", http.StatusForbidden
		}
		return "", 0
	}
	parentID, err := strconv.Atoi(newParentStr)
	if err != nil {
		return "親ページIDが不正です", http.StatusBadRequest
	}
	var exists bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = ?)", parentID).Scan(&exists)
	if !exists {
		return "指定された親ページが存在しません", http.StatusBadRequest
	}
	if parentID == childID {
		return "自分自身を親ページに指定できません", http.StatusBadRequest
	}
	if parentCreatesCycle(childID, parentID) {
		return "自分の子孫ページを親に指定できません（循環します）", http.StatusBadRequest
	}
	if !GetPerms(parentID).CanWrite(user) {
		return "新しい親ページへの書き込み権限がありません", http.StatusForbidden
	}
	return "", 0
}

// ValidateParentAPIHandler は、編集中ページの親ページ変更が妥当かを返します（クライアントの即時検証用）。
// 権威的な検証は保存API側でも行われます。対象ページのwrite権限を前提とします。
func ValidateParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !RequirePageWrite(w, r, id) {
		return
	}
	childID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	newParent := strings.TrimSpace(r.URL.Query().Get("parent"))

	w.Header().Set("Content-Type", "application/json")
	if msg, code := validateParentChange(auth.CurrentUser(r), childID, newParent); code != 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
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
			WriteSidecar("000000", PageMeta{Owner: defaultOwner, Mode: "302"})
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
