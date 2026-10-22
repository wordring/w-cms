package cms

import (
	"regexp"
	"sort"
	"strings"

	"w-cms/internal/cms/page"
)

// ─────────────────────────────────────────────────────────────────────────
// このページを指しているページ（2026-10-23・【考察】消すと壊れるページと表 §5 の案A の芯）
//
//	2026-10-22 利用者:「消すと壊れるページや表を探して文書にしてください。できる限りそのようなページや表を無くしたいです」
//
// 参照の型（設定の語彙で `type: ref` の語）のタグと、同じ語を見出しにした表のセルは、索引（page_tags・vocab_index）に入っています。
// そこから、ページ id を指している**ほかの**ページを引きます。消す前に「消すと何が壊れるか」を見るためのものです。
//
//   - 値の中の6桁の番号を1つずつ見ます（「001234」「001234-abcd」「001234、001235」「/001234」のどれも当たる）。
//   - 自分自身と、テンプレートの下のページは数えません。
//   - 本文のリンク（<a href>）とファイル表示（data-ref）は索引に無いので、ここでは見ません。
// ─────────────────────────────────────────────────────────────────────────

// refIDRe は参照の値の中のページ番号（6桁）です。
var refIDRe = regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`)

// refValueNames は値が id を含むかです（6桁の番号として）。
func refValueNames(value, id string) bool {
	for _, m := range refIDRe.FindAllStringSubmatch(value, -1) {
		if m[1] == id {
			return true
		}
	}
	return false
}

// PagesReferringTo は、ページ id を参照の型のタグ・表のセルで指しているほかのページの番号を、小さい順に返します。
func PagesReferringTo(db ReadOnlyDB, id string) ([]int, error) {
	self, ok := page.NormalizeID(id)
	if !ok {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, q := range []string{
		`SELECT page_id, name, value FROM page_tags WHERE value LIKE ?`,
		`SELECT page_id, field, value FROM vocab_index WHERE value LIKE ?`,
	} {
		rows, err := db.Query(q, "%"+self+"%")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pid int
			var name, value string
			if err := rows.Scan(&pid, &name, &value); err != nil {
				rows.Close()
				return nil, err
			}
			if page.FormatID(pid) == self || seen[pid] || InferColumnType(strings.TrimSpace(name)) != ColRef || !refValueNames(value, self) {
				continue
			}
			seen[pid] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	var out []int
	for pid := range seen {
		if !IsTemplateArea(page.FormatID(pid)) {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out, nil
}
