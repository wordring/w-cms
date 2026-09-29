package toho

// ─────────────────────────────────────────────────────────────────────────
// 品番は記号の列から（2026-09-29）
//
// 利用者:「品番が装置名称になっています。これは、顧客が部品番号の項目にそう書いてきたからなのですが、
// 品番を図面番号のような記号列の項目とGeminiに言い含めることは出来ますか？」。
//
// どの列を品番にするかは Gemini が選びます（0c 決着・`item_no_source`）。見出しが「部品番号」の列に
// 客先が**装置の呼び名**を書いてくると、見出しの言葉を信じた Gemini がそれを品番にしていました——
// 図面番号の列には記号が並んでいたのに。プロンプトに「品番は記号の列」と書き足したうえで、
// **Gemini の答えを Go の側でも確かめます**（機械の読みを同じ機械に確かめさせない・引き継ぎの罠）:
//
//   - 選んだ列の値が**日本語の名前**（ひらがな・カタカナ・漢字を含む）ばかりで、
//   - 読んだままの表（source_table）に、同じ行で**記号**（英数字と `-` などで、数字を含む）が並ぶ
//     別の列があれば、そちらへ替える（図番・図面番号の見出しを優先）。
//
// 替えたときは `item_no_source` もその列の見出しにする——「解析は、先方の『…』を弊社の『品番』に
// 入れました」の一文が本当のことを言うように。替えられなければ何もしない（人が直す）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"strings"
	"unicode"

	"w-cms/internal/cms"
)

// looksLikeName は値が日本語の名前を含むか（品番らしくない）です。
func looksLikeName(v string) bool {
	for _, r := range v {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// codeRe は記号の値（図番・品番の形）です——英数字で始まり、英数字と `-` `_` `.` `/` だけ。
var codeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9\-_./]*$`)

// looksLikeCode は値が品番らしい記号かです。⚠ 数だけの値（数量・単価）は記号と見なしません——
// 英字か `-` を含むものだけ（`100-01-00`・`K120-3`・`A100-B01-02`）。
func looksLikeCode(v string) bool {
	v = strings.TrimSpace(cms.NormalizeNameForIngest(v))
	if v == "" || !codeRe.MatchString(v) || !strings.ContainsAny(v, "0123456789") {
		return false
	}
	return strings.ContainsAny(v, "-") || strings.IndexFunc(v, func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
	}) >= 0
}

// fixItemNoColumn は、品番に名前の列が選ばれていたら記号の列へ替えます（替えた数を返す）。
func fixItemNoColumn(j *orderJudgment) int {
	if j == nil || len(j.Items) == 0 {
		return 0
	}
	// 1. 品番が名前になっている行があるか（空は数えない）。全部が名前でなければ触らない。
	named := 0
	for _, it := range j.Items {
		if strings.TrimSpace(it.ItemNo) == "" {
			continue
		}
		if !looksLikeName(it.ItemNo) {
			return 0
		}
		named++
	}
	if named == 0 {
		return 0
	}
	t := j.SourceTable
	if len(t.Headers) == 0 || len(t.Rows) == 0 {
		return 0
	}
	// 2. 読んだままの表で、品番の値がどの列にあるか（選んだ列）。
	norm := func(s string) string { return strings.TrimSpace(cms.NormalizeNameForIngest(s)) }
	chosen := -1
	for ci := range t.Headers {
		hits := 0
		for _, row := range t.Rows {
			if ci >= len(row) {
				continue
			}
			for _, it := range j.Items {
				if it.ItemNo != "" && norm(row[ci]) == norm(it.ItemNo) {
					hits++
					break
				}
			}
		}
		if hits > 0 {
			chosen = ci
			break
		}
	}
	if chosen < 0 {
		return 0
	}
	// 3. 替える先: 品番の値がある行で、値が全部記号の列（図番・図面番号の見出しを優先）。
	best, bestScore := -1, 0
	for ci, h := range t.Headers {
		if ci == chosen {
			continue
		}
		ok, n := true, 0
		for _, row := range t.Rows {
			if chosen >= len(row) || norm(row[chosen]) == "" {
				continue // 品番の無い行（小計の行など）は見ない
			}
			if ci >= len(row) || !looksLikeCode(row[ci]) {
				ok = false
				break
			}
			n++
		}
		if !ok || n == 0 {
			continue
		}
		score := 1
		if hh := norm(h); strings.Contains(hh, "図番") || strings.Contains(hh, "図面番号") {
			score = 2
		}
		if score > bestScore {
			best, bestScore = ci, score
		}
	}
	if best < 0 {
		return 0
	}
	// 4. 行ごとに置き換える。⚠ **選んだ列の値だけでは行を当てられません**——客先が全部の行に
	// 同じ装置名を書いてくると（実例がそれ）、どの明細も1行目の図番になります。品名・数量が
	// 同じ行に在るかで点を付け、**1つの行は1度しか使いません**（同点なら上の行から）。
	has := func(row []string, v string) bool {
		if v = norm(v); v == "" {
			return false
		}
		for _, c := range row {
			if norm(c) == v {
				return true
			}
		}
		return false
	}
	used := make([]bool, len(t.Rows))
	changed := 0
	for i := range j.Items {
		it := &j.Items[i]
		if strings.TrimSpace(it.ItemNo) == "" {
			continue
		}
		pick, pickScore := -1, -1
		for ri, row := range t.Rows {
			if used[ri] || chosen >= len(row) || best >= len(row) || norm(row[chosen]) != norm(it.ItemNo) {
				continue
			}
			score := 0
			if has(row, it.ItemName) {
				score += 2
			}
			if has(row, it.Quantity) {
				score++
			}
			if score > pickScore {
				pick, pickScore = ri, score
			}
		}
		if pick < 0 {
			continue
		}
		used[pick] = true
		it.ItemNo = strings.TrimSpace(t.Rows[pick][best])
		it.ItemNoSource = t.Headers[best]
		changed++
	}
	return changed
}
