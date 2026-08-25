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

	// titlePlaceholder は差し替える既定のタイトル要素です。
	// index.html を単体で開いても壊れないよう、実在するタイトルをそのまま目印にします。
	titlePlaceholder = "<title>w-cms エディタ</title>"

	// bodyPlaceholder は匿名の印を付ける位置の目印です。
	// タイトルと同じく「実在する記述をそのまま目印にする」流儀で、殻を単体で
	// 開いても壊れません。
	bodyPlaceholder = "<body>"
)

// shellCache は殻の内容をメモリに保持します。mtime を見て変化時だけ読み直すため、
// 開発中に index.html を編集しても再起動なしで反映されます。
var shellCache struct {
	sync.Mutex
	body    string
	modTime int64
}

// loadShell は殻のHTMLを返します（mtimeキャッシュ付き）。
func loadShell() (string, error) {
	info, err := os.Stat(shellPath)
	if err != nil {
		return "", err
	}
	mod := info.ModTime().UnixNano()

	shellCache.Lock()
	defer shellCache.Unlock()

	if shellCache.body != "" && shellCache.modTime == mod {
		return shellCache.body, nil
	}
	data, err := os.ReadFile(shellPath)
	if err != nil {
		return "", err
	}
	shellCache.body = string(data)
	shellCache.modTime = mod
	return shellCache.body, nil
}

// RenderPageShell は本文HTML（サニタイズ済みであること）とページタイトルを殻へ埋め込み、
// ブラウザへ返す完成HTMLを組み立てます。
//
// anonymous が真なら body へ `class="anonymous"` を刻みます。編集スイッチ・ログアウト
// ボタン・「保存されるHTML」欄を隠すのは CSS（`body.anonymous`）の仕事ですが、印を
// JS（/api/me の応答後）で付けていたころは**最初の描画に間に合わず**、それらが一瞬
// 映って消えていました（中身は空なので情報は漏れないが、見た目が壊れる）。
// 印はサーバーが知っている情報なので、ここで刻むのが正しい置き場です。
// なお JS 側も従来どおり印を付けます（冪等。通信で状態が変わる場合に備える）。
func RenderPageShell(bodyHTML, title string, anonymous bool) (string, error) {
	shell, err := loadShell()
	if err != nil {
		return "", err
	}

	out := strings.Replace(shell, contentPlaceholder, bodyHTML, 1)

	if t := strings.TrimSpace(title); t != "" {
		out = strings.Replace(out, titlePlaceholder,
			"<title>"+html.EscapeString(t)+" - w-cms</title>", 1)
	}
	if anonymous {
		out = strings.Replace(out, bodyPlaceholder, `<body class="anonymous">`, 1)
	}
	return out, nil
}
