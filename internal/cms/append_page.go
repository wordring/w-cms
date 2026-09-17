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
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/cms/page"
)

// InsertAfterH1 は本文HTMLの**見出しの直後**へ fragment を差し込んで返します
// （h1 が無ければ先頭）。**ファイルは読み書きしません**——文字列を返すだけです。
//
// 改定図面のために要ります——ユーザー:「既存ページの図面の項目の先頭に配置しては
// どうでしょう？既存の図面は古いとわかるように赤枠で囲み、ユーザーの判断で消します。
// （古い図面は旧版に残っています）」（2026-09-03）。**新しいものが上**という
// 並びそのものが「どれが最新か」を表すので、状態を別に持たずに済みます。
//
// ⚠ **ページを読み書きする版は置きません**（2026-09-14 に撤去）。呼び手はどちらも
// **1回の書き換えで全部やる**必要があったためです——外して・足して・履歴を直すのを
// 別々に保存すると、途中で失敗したときに図面の無いページが残ります。だから
// `RewriteBody` の中でこれを呼ぶ形にして、保存は1回に畳みます。
//
// ⚠ **`</h1>` を生の文字列で探します。** `<h1 class="…">` のように属性が付いていても
// 終了タグは同じなので当たりますが、本文に `</h1>` が文字として現れると誤ります
// （サニタイズを通った本文では起きません）。直すときはここ1箇所です——
// **2026-09-14 まで同じ7行が3箇所に写されていました**（うち1つは呼び手ゼロ）。
func InsertAfterH1(body, fragment string) string {
	if i := strings.Index(body, "</h1>"); i >= 0 {
		at := i + len("</h1>")
		return body[:at] + fragment + body[at:]
	}
	return fragment + body
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
// 押せばそのブロックへ飛ぶので（ref_render.go）、加工製品ページの図面ブロックに
// 付ければ「その改定の社内コード」がそのまま出来上がります（2026-09-03 ユーザー:
// 「部品の社内コードは加工製品ページのページ番号と改定番号を足したものになるのでは？
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

// ── 可変タグの読み書き（2026-09-15 に intake_eml.go・handler_handled.go から寄せた）──
//
// どちらも通信箱に限らず、アドレス帳（連絡先の追記）・メール（送信の控え）が使います。

// WriteTag は「名前：値」のタグを1対書きます（値が空なら書かない）。
//
// **値は前後の空白を落としてから書きます。** 取り込んだメールのヘッダには
// 余分な空白が普通に混ざっており、そのまま入れると索引の値が空白付きになって
// 逆引き（PagesByTag は生テキストで引く）が外れます。
//
// かつては取り込み係とメール拡張が同名の関数を別々に持ち、**trim の有無だけが
// 違って**いました——どちらの経路で作られたページかで値が変わる、という形の
// 静かな食い違いだったので、コアの1つに寄せました（2026-09-05）。
func WriteTag(b *strings.Builder, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("<dt>" + html.EscapeString(name) + "</dt><dd>" + html.EscapeString(value) + "</dd>")
}

// EndOfFirstTagList は最初の可変タグの並びの終わり（`</dl>` の位置）を返します。
// 見つからなければ -1。
func EndOfFirstTagList(body string) int {
	i := strings.Index(body, `<dl data-type="tags">`)
	if i < 0 {
		return -1
	}
	end := strings.Index(body[i:], "</dl>")
	if end < 0 {
		return -1
	}
	return i + end
}
