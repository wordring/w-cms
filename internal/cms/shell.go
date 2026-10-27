package cms

import (
	"html"
	"os"
	"strings"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────
// ページシェルの合成
//
// assets/index.html（エディタの殻）へ、サーバー側で本文とタイトルを埋め込んだ
// 完成HTMLを返すための組み立てです。従来はフロントが /api/load を叩いて
// #editor-content へ流し込んでいましたが、初期表示のチラつき・JS無効時に本文が
// 読めない・404/401を正しく返せないといった問題があったため合成方式にしました。
//
// テンプレートエンジン（html/template）は使いません。当初の理由は index.html に
// 巨大なインラインJSがあり contextual escaping で壊れるリスクがあったことですが、
// それは 2026-08-06 に外部化済みです（いま残るインラインは <head> の FOUC 防止
// スクリプト1本だけ）。それでも文字列置換のままなのは、差し込む本文が
// **サニタイズ済みのHTML**であり、html/template では template.HTML への
// キャストが必要になって contextual escaping の恩恵が無いためです。
// 代わりに専用のプレースホルダを文字列置換します。
// ─────────────────────────────────────────────────────────────────────────

const (
	// shellPath は殻となるHTMLファイルです。
	shellPath = "assets/index.html"

	// contentPlaceholder は本文を差し込む位置の目印（#editor-content の中身）。
	contentPlaceholder = "<!--WCMS_CONTENT-->"

	// breadcrumbPlaceholder はパンくず（本文の上の帯）を差し込む位置の目印。
	breadcrumbPlaceholder = "<!--WCMS_BREADCRUMB-->"

	// titlePlaceholder は差し替える既定のタイトル要素です。
	// index.html を単体で開いても壊れないよう、実在するタイトルをそのまま目印にします。
	titlePlaceholder = "<title>w-cms エディタ</title>"

	// siteNamePlaceholder は画面の左上のサイトの名前です（2026-10-27）。
	siteNamePlaceholder = `<h1 class="title">w-cms Wiki Editor</h1>`
)

// siteName は、環境変数 WCMS_SITE_NAME に書いたサイトの名前です（2026-10-27・空ならいままでどおり）。
// 画面の左上の名前と、タブの題の末尾（「ページの題 - 名前」）に使います。動いている w-cms ごとの値なので、
// Git の設定（config/settings.json）ではなく環境変数に置きます——本番と試験用で名前を変えられるように。
//
// 利用者:「本番サーバーのw-cmsページのタイトルをw-cms Wiki Editorから板金部に変えます」
func siteName() string {
	return strings.TrimSpace(os.Getenv("WCMS_SITE_NAME"))
}

// shellFile は殻HTMLの読み込み口です。mtime を見て変化時だけ読み直すため、
// 開発中に殻を編集しても再起動なしで反映されます。編集用と公開用の2枚
// （shellCache・publicShellCache）で同じ機構が要るので、型として1つに持ちます。
type shellFile struct {
	path    string
	mu      sync.Mutex
	body    string
	modTime int64
}

// load は殻のHTMLを返します（mtimeキャッシュ付き）。
func (s *shellFile) load() (string, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return "", err
	}
	mod := info.ModTime().UnixNano()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.body != "" && s.modTime == mod {
		return s.body, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return "", err
	}
	s.body = string(data)
	s.modTime = mod
	return s.body, nil
}

// shellCache は編集用の殻です。
var shellCache = &shellFile{path: shellPath}

// RenderPageShell は本文HTML（サニタイズ済みであること）とページタイトルを殻へ埋め込み、
// ブラウザへ返す完成HTMLを組み立てます。
//
// **この殻が届くのは認証済みの利用者だけ**です（2026-08-26。匿名の訪問者へは
// 公開専用ビュー＝shell_public.go が返る）。前日に入れた「匿名なら body へ
// `class="anonymous"` を刻む」応急処置は、この分岐が入って到達不能になったので
// 外しました——本筋（クローム自体を配信しない）が入ったため。
// JS 側の `body.anonymous` 付与は残してあります: 編集中にセッションが切れると
// /api/me が未認証を返すので、その場合の見せ方はまだ要るからです。
// breadcrumbHTML は先祖の道（[RenderBreadcrumb] が組む・空なら帯は CSS の :empty で
// 畳まれる）。本文と同じく、渡す側が既にエスケープ済みであること。
func RenderPageShell(bodyHTML, breadcrumbHTML, title string) (string, error) {
	shell, err := shellCache.load()
	if err != nil {
		return "", err
	}

	out := strings.Replace(shell, contentPlaceholder, bodyHTML, 1)
	out = strings.Replace(out, breadcrumbPlaceholder, breadcrumbHTML, 1)
	out = strings.Replace(out, extScriptsPlaceholder, extScriptTags(), 1) // 拡張の画面の JS（ext_scripts.go）
	out = strings.Replace(out, extStylesPlaceholder, extStyleTags(), 1)   // 拡張の画面の CSS（同じ）

	suffix := "w-cms"
	if name := siteName(); name != "" {
		suffix = name
		out = strings.Replace(out, siteNamePlaceholder, `<h1 class="title">`+html.EscapeString(name)+`</h1>`, 1)
	}
	if t := strings.TrimSpace(title); t != "" {
		out = strings.Replace(out, titlePlaceholder,
			"<title>"+html.EscapeString(t)+" - "+html.EscapeString(suffix)+"</title>", 1)
	}
	// 画面のファイルの住所に版の印を添える（asset_version.go・殻は no-store なので、いつも今の印を指す）。
	return StampAssetURLs(out), nil
}
