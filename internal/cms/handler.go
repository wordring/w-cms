package cms

import (
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"w-cms/internal/database"
)

// PageMeta は一覧表示用の簡素化されたメタデータ構造体です。
type PageMeta struct {
	ID       string
	Title    string
	FilePath string
}

// UploadHandler はブラウザからのファイルアップロードリクエストを受け取り、保存と同期を行います。
func UploadHandler(w http.ResponseWriter, r *http.Request) {
	// POSTリクエスト以外は弾く
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// ファイルをメモリに読み込む
	r.ParseMultipartForm(32 << 20)
	file, _, err := r.FormFile("html_page")
	if err != nil {
		http.Error(w, "File upload error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	content, _ := io.ReadAll(file)

	// 階層化された保存先（物理パス）を取得し、フォルダを作成して保存
	newID := GenerateNextID(database.DB)
	pageDir := GetPageDir(newID)

	os.MkdirAll(pageDir, 0755)

	// 物理ファイル名にページIDを使用する (例: 00001.html)
	htmlPath := filepath.Join(pageDir, newID+".html")
	os.WriteFile(htmlPath, content, 0644)

	// データベースへ同期
	SyncIndex(newID, string(content))

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// IndexHandler はデータベースから記事一覧を取得しブラウザに描画します。
func IndexHandler(w http.ResponseWriter, r *http.Request) {
	// 手順1: pagesテーブルからデータを取得する
	rows, err := database.DB.Query("SELECT id, title, file_path FROM pages ORDER BY id DESC")
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 手順2: 取得したデータを PageMeta のスライスに詰め替える
	var pages []PageMeta
	for rows.Next() {
		var p PageMeta
		if err := rows.Scan(&p.ID, &p.Title, &p.FilePath); err == nil {
			pages = append(pages, p)
		}
	}

	// 手順3: HTMLテンプレート
	tmpl := `
	<!DOCTYPE html>
	<html>
	<head><title>w-cms 統合データベース</title></head>
	<body>
		<h1>w-cms 統合データ登録</h1>
		<form action="/upload" method="post" enctype="multipart/form-data">
			<input type="file" name="html_page" accept=".html" required>
			<button type="submit">アップロード</button>
		</form>
		<hr>
		<h2>登録済みデータ一覧</h2>
		<table border="1">
			<tr><th>ID</th><th>タイトル</th><th>物理ファイルパス</th></tr>
			{{range .}}
			<tr>
				<td>{{.ID}}</td>
				<td>{{.Title}}</td>
				<td><code>{{.FilePath}}</code></td>
			</tr>
			{{end}}
		</table>
	</body>
	</html>
	`
	t, _ := template.New("index").Parse(tmpl)
	t.Execute(w, pages)
}
