package main

import (
	"os"
	"path/filepath"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// effectiveSettingsLine は起動時に出す「いま何が効いているか」の1行です（2026-10-01・【要求】運用 §3「起動時に有効な設定を
// 1行ログへ出すこと——環境変数方式は『いま何が効いているか』が見えにくいため。ただし秘密の値は出しません（キーの有無だけ）」）。
//
// ⚠ **秘密は有無だけ**——`GEMINI_API_KEY`・メールの `WCMS_MAIL_CLIENT_ID` の値は書かない（ログは人の目にも
// ファイルにも残る）。
func effectiveSettingsLine(cert, key string) string {
	onOff := func(b bool) string {
		if b {
			return "あり"
		}
		return "なし"
	}
	has := func(name string) string { return onOff(os.Getenv(name) != "") }
	// 待ち受け（WCMS_ADDR）とデータの場所（WCMS_DATA_DIR）も言います（2026-10-14——1台に2つ同居させたとき、
	// どちらがどこで動いているかを1行で見分けられるように）。
	tls := cert != "" && key != ""
	listen := listenURL(listenAddr(tls), tls) + "（TLS " + onOff(tls) + "）"
	exts := []string{}
	for _, e := range cms.Extensions() {
		exts = append(exts, e.ID)
	}
	ext := "なし"
	if len(exts) > 0 {
		ext = strings.Join(exts, ",")
	}
	return "有効な設定: 待ち受け " + listen +
		"・WebDAV " + onOff(cms.DavEnabled()) +
		"・Secure Cookie " + onOff(auth.SecureCookies()) +
		"・データ " + filepath.ToSlash(database.DataDir()) + "/・設定 " + cms.SettingsPath +
		"・Gemini のキー " + has("GEMINI_API_KEY") +
		"・メールのアプリ登録 " + has("WCMS_MAIL_CLIENT_ID") +
		"・拡張 " + ext
}
