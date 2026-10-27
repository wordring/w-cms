package toho

// ─────────────────────────────────────────────────────────────────────────
// 図面のブロックに「通信記録」を付ける（2026-10-27・comm.RecordRefTag）
//
// 利用者:「では、メール等の通信ページを指すタグを新設しましょう。メール本文を参照したいのが主な理由です」
// 「これは、メールから図面を追加や更新したなら図面につけるべきですが、加工製品ページを作ったなら加工製品ページ冒頭のタグに
// 付くことになると思います」
//
//   - 加工製品ページを作るとき（解析・メールから作る空のページ）は、ページの頭のタグに付ける（analyze_product_page.go・
//     mail_product.go）。
//   - 整理で既にある加工製品ページへ図面として運ぶとき（図面追加・図面改定）は、運ぶ図面のブロックに付ける（revision.go の
//     mergeInputs）。値は、そのブロックの `受信元` のページ番号（無ければ運ぶ前のページの頭の `通信記録`）。
//   - 重複（取り込まない）で既にある図面に `受信元` を書き足すときも、同じブロックに付ける（filing_duplicate.go）。
//   - 同じ値がもうあれば足さない。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"strconv"
	"strings"

	"w-cms/ext/comm"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// recordPageOfSource は、`受信元` の値（`ページID-添付ID` かページID）から通信記録のページ番号を返します（読めなければ空）。
func recordPageOfSource(src string) string {
	p, _, _ := strings.Cut(strings.TrimSpace(src), "-")
	if len(p) != 6 {
		return ""
	}
	if _, err := strconv.Atoi(p); err != nil {
		return ""
	}
	return p
}

// hasRecordTag は、本文 body のどこかに「通信記録：record」があるかです。
func hasRecordTag(body, record string) bool {
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(comm.RecordRefTag) + `</dt>\s*<dd>\s*` + regexp.QuoteMeta(record) + `\s*</dd>`)
	return re.MatchString(body)
}

// recordTagPair は「通信記録：record」のタグの1組です。
func recordTagPair(record string) string {
	return `<dt>` + htmlEscape(comm.RecordRefTag) + `</dt><dd>` + htmlEscape(record) + `</dd>`
}

// withRecordTag は、図面のブロック block の最初のタグの並びへ「通信記録：record」を足します（もうあれば・書けなければそのまま）。
func withRecordTag(block, record string) string {
	if record == "" || hasRecordTag(block, record) {
		return block
	}
	at := cms.EndOfFirstTagList(block)
	if at < 0 {
		return block
	}
	return block[:at] + recordTagPair(record) + block[at:]
}

// recordOfMovingBlock は、整理で運ぶ図面のブロックの通信記録のページ番号です——ブロックの `受信元` から、無ければ
// 運ぶ前のページ srcPageID の頭の `通信記録` から。
func recordOfMovingBlock(block, srcPageID string) string {
	if m := sourceRefRe.FindStringSubmatch(block); m != nil {
		if p := recordPageOfSource(m[1]); p != "" {
			return p
		}
	}
	if id, err := strconv.Atoi(srcPageID); err == nil {
		return recordPageOfSource(cms.PageTagValue(database.DB, id, comm.RecordRefTag))
	}
	return ""
}
