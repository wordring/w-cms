package cms

// ページの表示に関わるハンドラ。画面（サーバー合成した完成HTML）と、
// 編集モードの載せ替えに使う本文API を扱います。

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	// 直接ブラウザで開いてもHTMLとして実行されない（多層防御）。
	//
	// **描画時と同じくサニタイズを通す**（サニタイズ二層目。docs/本文サニタイズ設計.md）。
	// かつては「正本をそのまま返す」方針だったが、この応答は populateEditor が
	// **属性を濾さずDOMへ入れる**ため、保存経路を通っていない本文（手動配置・バックアップ復元・
	// 取り込みAPIが直接書いたページ）に仕込まれた id="w-…" が殻の要素を乗っ取れてしまう
	// （getElementById は文書順で最初を返し、本文の挿入点より後ろに権限UIの入力欄がある）。
	// RootHandler には二層目があるのにここだけ抜けていた（2026-08-20 に塞いだ）。
	//
	// 計算ビューのマーカーには、編集モードの載せ替えでも表示が出るよう中身
	// （vocab-chrome）を埋めて返す。シリアライザが保存時に落とすので正本には混ざらない。
	// **ページ内アンカー（RenderAnchors）はここでは足さない**——合成した id が
	// エディタのDOMへ入ると、シリアライザが本文として保存してしまう（anchor.go の冒頭）。
	body := RenderComputedViews(r, idInt, Sanitize(string(content)))

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
			pageNotFound(w, r)
			return
		}
	}

	// ページの実体（本文・タイトル）を取り出し、認可のうえで殻へ埋め込んで返す。
	pageID, err := strconv.Atoi(id)
	if err != nil {
		pageNotFound(w, r)
		return
	}

	var filePath, title string
	err = database.DB.QueryRow(
		"SELECT file_path, COALESCE(title, '') FROM pages WHERE id = ?", pageID,
	).Scan(&filePath, &title)
	if err != nil {
		pageNotFound(w, r)
		return
	}

	// 画面の認可。API（401を返す page.RequirePageReadOrPublic）とは扱いを変え、匿名は
	// ログイン画面へ誘導する（RequireAuth の「APIは401・画面は/login」に合わせる）。
	if !requirePageViewable(w, r, pageID) {
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		pageNotFound(w, r)
		return
	}

	// 保存経路を通っていない本文（既存データ・バックアップ復元・手動配置）に備え、
	// 描画時にもサニタイズする（docs/本文サニタイズ設計.md の二層目）。
	// サニタイズ後に計算ビュー（子ページ一覧・手配集計）の中身をサーバーが埋める
	// （view_render.go。埋めた中身は class を持つためサニタイズより後に行う）。
	// さらにページ内アンカー（見出し・ブロックの id）を合成する。**この経路だけ**で行う
	// ——エディタが編集モードで読み直す GET /api/load へ入れると、合成した id が
	// シリアライザを通って本文として保存されてしまう（anchor.go の冒頭）。
	body := RenderAnchors(RenderComputedViews(r, pageID, Sanitize(string(content))))
	shellHTML, err := RenderPageShell(body, title, auth.CurrentUser(r) == nil)
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
		// 認証済み×read不可は 403 のまま。「存在は分かるが読めない」という
		// Unix の作法で、社内では相手が誰かも分かっているので隠す意味が薄い。
		if !page.GetPerms(pageID).CanRead(u) {
			http.Error(w, "このページを閲覧する権限がありません", http.StatusForbidden)
			return false
		}
		return true
	}
	if page.EffectivePublic(pageID) {
		return true
	}
	// 匿名×非公開は**不存在と同じ404**にする。権限の無いアドレスを開いた社員には
	// ログイン画面より「ありません」のほうが迷わない、というのが決定
	// （列挙対策が動機ではない。要件定義書 §2.1）。
	//
	// 例外はトップページだけ——ここまで404にすると、サイトを閉じたときに
	// 誰も入口へ辿り着けなくなる。
	if pageID == 0 {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return false
	}
	notFoundWithLogin(w, r)
	return false
}

// notFoundWithLogin は匿名向けの「ありません」画面を返します。
//
// ただの 404 で終わらせないのは、**社員どうしでページのアドレスを共有する使い方**が
// 行き止まりになるからです（404統一と戻り先の復元を同時に入れる、という決定の片割れ）。
// ログインへの入口に戻り先を積んでおけば、ログインしたその足で目的のページへ着きます。
//
// 存在しないページでも同じ画面を返すので、これで存在が分かることはありません。
func notFoundWithLogin(w http.ResponseWriter, r *http.Request) {
	next := url.QueryEscape(r.URL.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	// CSP strict のためインラインの style/script は書けない（/assets/ の外部ファイルのみ）。
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="ja">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>ページがありません - w-cms</title>
<link rel="stylesheet" href="/assets/login.css"></head>
<body class="notfound-page">
<main class="notfound-card">
<h1>ページがありません</h1>
<p>アドレスが違うか、閲覧する権限が無いページです。</p>
<p><a href="/login?next=%s">ログインする</a></p>
</main>
</body>
</html>
`, next)
}

// pageNotFound は画面の「ありません」応答です。
//
// 匿名にはログインへの入口つきの画面を返します——**匿名×非公開もここへ来る**ので、
// 不存在と同じ体裁でなければ「無いのか読めないのか」が応答で分かってしまいます。
// 認証済みには素の404で足ります（読めないページには 403 を返しており、
// 存在を隠していないため）。
func pageNotFound(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentUser(r) == nil {
		notFoundWithLogin(w, r)
		return
	}
	http.NotFound(w, r)
}
