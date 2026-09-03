package cms

// ─────────────────────────────────────────────────────────────────────────
// 既存ページの末尾へブロックを書き足す（2026-09-03）
//
// 機械が作った結果を**本文へ材料化する**ための口です。PDFの読み取り
// （種類・OCRテキスト）のように、ページを作るほどではないが本文に残したい結果を、
// **保存APIと同じ作法**で足します——サニタイズ・更新日時・版・索引の順序を
// 外さないための1箇所。
//
// **ロックは呼ぶ側が取ります。** ここは「書く手順」だけを持ち、「書いてよいか」の
// 判断（他の人が編集中でないか）は呼ぶ側の責任です——取り込み（作った直後の
// ページ・誰も見ていない）と解析（既存ページ・誰かが開いているかもしれない）で
// 必要な確認が違うため。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"strings"

	"w-cms/internal/cms/page"
)

// InsertAfterH1 はページ本文の**見出しの直後**へ fragmentHTML を差し込みます
// （h1 が無ければ先頭）。
//
// 改定図面のために要ります——ユーザー:「既存ページの図面の項目の先頭に配置しては
// どうでしょう？既存の図面は古いとわかるように赤枠で囲み、ユーザーの判断で消します。
// （古い図面は旧版に残っています）」（2026-09-03）。**新しいものが上**という
// 並びそのものが「どれが最新か」を表すので、状態を別に持たずに済みます。
func InsertAfterH1(pageID, author, fragmentHTML string) error {
	return rewritePageBody(pageID, author, func(current string) string {
		if i := strings.Index(current, "</h1>"); i >= 0 {
			at := i + len("</h1>")
			return current[:at] + fragmentHTML + current[at:]
		}
		return fragmentHTML + current
	})
}

// AppendToPageBody はページ本文の末尾へ fragmentHTML を足します。
// fragment は**呼ぶ側がエスケープ済み**であること（サニタイザは安全性の網で、
// エスケープの肩代わりはしません）。
func AppendToPageBody(pageID, author, fragmentHTML string) error {
	return rewritePageBody(pageID, author, func(current string) string {
		return current + fragmentHTML
	})
}

// rewritePageBody は本文の読み書きの**作法**（サニタイズ・更新日時・版・索引の順序）を
// 1箇所に持ちます。どこを書き換えるかだけが rewrite で変わります。
func rewritePageBody(pageID, author string, rewrite func(current string) string) error {
	htmlPath := filepath.Join(page.GetPageDir(pageID), pageID+".html")
	current, err := os.ReadFile(htmlPath)
	if err != nil {
		return err
	}

	// 保存経路と同じ順序（handler_save.go）——サニタイズ → 更新日時 → 書き込み →
	// 版 → 索引。版を残すので、機械が足したものは人がリバートで取り消せる。
	safeHTML := Sanitize(rewrite(string(current)))
	if _, err := page.BumpUpdatedAt(pageID); err != nil {
		return err
	}
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	if err := RecordVersion(pageID, author, safeHTML, false); err != nil {
		return err
	}
	return SyncIndex(pageID, safeHTML)
}
