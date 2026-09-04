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
	"crypto/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// RewriteBody は本文を書き換えます（作法つき）。拡張から使う口です
// ——改訂履歴へ1行足す、のような「読んで・変えて・書く」を、保存経路と同じ
// 順序（サニタイズ・更新日時・版・索引）で通すため。
func RewriteBody(pageID, author string, rewrite func(current string) string) error {
	return rewritePageBody(pageID, author, rewrite)
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

// SetPageH1 はページの見出し（h1）を書き換えます。**題はページ名**なので、
// 整理の画面で図面名称を直したらページの題も揃える必要があります。
func SetPageH1(pageID, author, title string) error {
	return rewritePageBody(pageID, author, func(current string) string {
		open := strings.Index(current, "<h1>")
		if open < 0 {
			return "<h1>" + title + "</h1>" + current
		}
		close := strings.Index(current[open:], "</h1>")
		if close < 0 {
			return "<h1>" + title + "</h1>" + current
		}
		return current[:open] + "<h1>" + title + "</h1>" + current[open+close+len("</h1>"):]
	})
}

// FirstBlockHTML は本文から最初の <section>…</section> を取り出します
// （無ければ空）。改定図面の合流で「新しい図面ブロックだけ」を運ぶために使います。
func FirstBlockHTML(bodyHTML string) string {
	open := strings.Index(bodyHTML, "<section")
	if open < 0 {
		return ""
	}
	close := strings.Index(bodyHTML[open:], "</section>")
	if close < 0 {
		return ""
	}
	return bodyHTML[open : open+close+len("</section>")]
}

// ReadPageBody はページ本文（保存されている生のHTML）を読みます。
// 表示用の合成（計算ビュー・参照リンク・アンカー）は掛かっていません。
func ReadPageBody(pageID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(page.GetPageDir(pageID), pageID+".html"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// blockIDAttrRe は本文の中の data-id を拾う正規表現です（採番の重複避けに使う）。
var blockIDAttrRe = regexp.MustCompile(`data-id="([0-9a-z]+)"`)

// NewBlockID は bodyHTML の中で未使用のブロックIDを1つ返します。
//
// **ブロックIDは社内コードの後半になります**——参照値 `ページID-ブロックID` は
// 押せばそのブロックへ飛ぶので（ref_render.go）、部品ページの図面ブロックに
// 付ければ「その改定の社内コード」がそのまま出来上がります（2026-09-03 ユーザー:
// 「部品の社内コードは部品ページのページ番号と改定番号を足したものになるのでは？
// 改定番号等は、改定を記す項目のdata-idとなるのではないでしょうか？」）。
//
// 形はエディタの採番（app.js の newBlockId）に合わせた4桁の base36 です
// ——**同じ本文に2種類の採番規則を混ぜない**ため。短さで衝突しうる分は、
// エディタと同じく使用済みとの突き合わせで潰します。
func NewBlockID(bodyHTML string) string {
	used := map[string]bool{}
	for _, m := range blockIDAttrRe.FindAllStringSubmatch(bodyHTML, -1) {
		used[m[1]] = true
	}
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 4)
	for attempt := 0; attempt < 50; attempt++ {
		if _, err := rand.Read(buf); err != nil {
			break
		}
		id := make([]byte, 4)
		for i, b := range buf {
			id[i] = chars[int(b)%len(chars)]
		}
		if !used[string(id)] {
			return string(id)
		}
	}
	// 乱数が尽きる状況は想定していないが、無言で衝突させるよりは長い値を返す。
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
