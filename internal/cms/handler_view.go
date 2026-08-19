package cms

// ページの表示に関わるハンドラ。画面（サーバー合成した完成HTML）と、
// 編集モードの載せ替えに使う本文API を扱います。

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

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
	// ページ本文の取得は read 権限を要求する（匿名でも実効公開なら閲覧可）。
	if !page.RequirePageReadOrPublic(w, r, id) {
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

	// 本文はHTMLだが、エディタは fetch().text() で受けて自前で DOMParser にかけるため、
	// text/html で返す必要がない。text/plain ＋ nosniff で返すことで、この URL を
	// 直接ブラウザで開いてもHTMLとして実行されない（多層防御）。保存時サニタイズ導入前の
	// 既存ファイルなど、万一スクリプトを含む本文が残っていても直接ナビゲーションでの
	// 実行を封じられる。詳細は docs/本文サニタイズ設計.md。
	// 計算ビューのマーカーには、編集モードの載せ替えでも表示が出るよう中身
	// （vocab-chrome）を埋めて返す。シリアライザが保存時に落とすので正本には混ざらない。
	body := RenderComputedViews(r, idInt, string(content))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(body))
}

// RootHandler はWiki型のルーティングを担当します。
func RootHandler(w http.ResponseWriter, r *http.Request) {
	// `/assets/` などの静的ファイルは既に mux で処理されている前提
	id := r.URL.Path[1:] // 先頭の `/` を取り除く

	// トップページの正規URLは /000000 の1つに統一する（同一文書が複数の名前を
	// 持たないように）。`/` や `/index.html` はエイリアスとして /000000 へ
	// リダイレクトする。
	if id == "" || id == "index.html" {
		target := "/000000"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	// 初回起動時の 000000 ページ自動生成
	if id == "000000" {
		var exists bool
		database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = 0)").Scan(&exists)
		if !exists {
			defaultHTML := `<h1>w-cms Wiki トップページ</h1>
<p>ここはすべての起点となるトップページです。</p>
<p>右上のスイッチで「編集モード」に切り替えると、Notionのようにブロックベースで編集できます。子ページは左のサイドパネルから辿れます。</p>`

			pageDir := page.GetPageDir("000000")
			os.MkdirAll(pageDir, 0755)
			htmlPath := filepath.Join(pageDir, "000000.html")
			os.WriteFile(htmlPath, []byte(defaultHTML), 0644)
			// トップページは全員が閲覧できるよう other に read を付与（owner rw / other r）。
			// 書き込みは admin（owner）のみ。
			if err := page.WriteSidecar("000000", page.PageMeta{Owner: page.DefaultOwner, Mode: "302"}); err != nil {
				log.Printf("トップページのサイドカー作成に失敗しました: %v", err)
			}
			if err := SyncIndex("000000", defaultHTML); err != nil {
				log.Printf("トップページの同期に失敗しました: %v", err)
			}
		}
	}

	// id が英数字ハイフンのみか簡易チェック
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			http.NotFound(w, r)
			return
		}
	}

	// ページの実体（本文・タイトル）を取り出し、認可のうえで殻へ埋め込んで返す。
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var filePath, title string
	err = database.DB.QueryRow(
		"SELECT file_path, COALESCE(title, '') FROM pages WHERE id = ?", pageID,
	).Scan(&filePath, &title)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 画面の認可。API（401を返す page.RequirePageReadOrPublic）とは扱いを変え、匿名は
	// ログイン画面へ誘導する（RequireAuth の「APIは401・画面は/login」に合わせる）。
	if !requirePageViewable(w, r, pageID) {
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 保存経路を通っていない本文（既存データ・バックアップ復元・手動配置）に備え、
	// 描画時にもサニタイズする（docs/本文サニタイズ設計.md の二層目）。
	// サニタイズ後に計算ビュー（子ページ一覧・手配集計）の中身をサーバーが埋める
	// （view_render.go。埋めた中身は class を持つためサニタイズより後に行う）。
	body := RenderComputedViews(r, pageID, Sanitize(string(content)))
	shellHTML, err := RenderPageShell(body, title)
	if err != nil {
		http.Error(w, "ページの生成に失敗しました", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 認可結果に依存する内容なのでキャッシュさせない。
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(shellHTML))
}

// requirePageViewable は画面表示のための read 認可を行います。
// 認証済みで権限が無ければ403、匿名で実効公開でなければ /login へリダイレクトします。
func requirePageViewable(w http.ResponseWriter, r *http.Request, pageID int) bool {
	if u := auth.CurrentUser(r); u != nil {
		if !page.GetPerms(pageID).CanRead(u) {
			http.Error(w, "このページを閲覧する権限がありません", http.StatusForbidden)
			return false
		}
		return true
	}
	if page.EffectivePublic(pageID) {
		return true
	}
	http.Redirect(w, r, "/login", http.StatusFound)
	return false
}
