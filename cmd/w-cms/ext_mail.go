//go:build !minimal && !nomail

package main

// ─────────────────────────────────────────────────────────────────────────
// メール送受信（IMAP／SMTP）——コンパイル時に選ぶ拡張（2026-09-03）
//
//	go build ./cmd/w-cms                  … メール入り（既定）
//	go build -tags nomail ./cmd/w-cms     … メールだけ外す
//	go build -tags minimal ./cmd/w-cms    … 素の w-cms（業務セットごと外れる）
//
// **メールを外しても他の拡張はビルドできます。** 使う側はコアの口
// （internal/cms/mail.go の CurrentMailer）に尋ねるだけで、この実装を直接
// import していないためです——ユーザーの問い「メール関係はメールプラグインに
// なって、それを使うプラグインは明示する感じでしょうか」に対する答えが、
// この「口はコア・中身はプラグイン」の形です。
//
// 入っているかどうかは起動ログに出ます（有効／設定待ちも含めて）。
// ─────────────────────────────────────────────────────────────────────────

import _ "w-cms/ext/mail"

func init() {
	loadedExtensions = append(loadedExtensions, "mail（メール送受信・IMAP／SMTP）")
}
