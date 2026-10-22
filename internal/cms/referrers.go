package cms

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/cms/page"
)

// ─────────────────────────────────────────────────────────────────────────
// このページを指しているページ（2026-10-23・【考察】消すと壊れるページと表 §5 の案A の芯）
//
//	2026-10-22 利用者:「消すと壊れるページや表を探して文書にしてください。できる限りそのようなページや表を無くしたいです」
//
// 参照の型（設定の語彙で `type: ref` の語）のタグと、同じ語を見出しにした表のセルは、索引（page_tags・vocab_index）に入っています。
// 本文のリンク・画像など・ファイル表示は page_links（page_links.go）に入っています。
// そこから、ページ id を指している**ほかの**ページを引きます。消す前に「消すと何が壊れるか」を見るためのものです。
//
//   - 参照の値は、中の6桁の番号を1つずつ見ます（「001234」「001234-abcd」「001234、001235」「/001234」のどれも当たる）。
//   - 自分自身と、テンプレートの下のページは数えません。
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

// refHit は「ページ PageID が、How の形で指している」の1件です。
type refHit struct {
	PageID int
	How    string // 「タグ「受信元」」「表「案件明細表」の「案件」」「リンク」「画像など」「ファイル表示」
}

// refHitsTo は、ページ id を指しているほかのページを、指し方つきで返します（参照のタグ・表のセル・本文のリンク・ファイル表示）。
// 同じページの同じ指し方は1件にまとめます。テンプレートの下のページは除きます。
func refHitsTo(db ReadOnlyDB, id string) ([]refHit, error) {
	self, ok := page.NormalizeID(id)
	if !ok {
		return nil, nil
	}
	target, _ := strconv.Atoi(self)
	seen := map[refHit]bool{}
	var hits []refHit
	add := func(h refHit) {
		if h.PageID != target && !seen[h] {
			seen[h] = true
			hits = append(hits, h)
		}
	}
	// ① 参照の型のタグと表のセル。行を読み切ってから見る（読みながら別の問い合わせをしない）。
	type raw struct {
		pid               int
		dataType, name, v string
	}
	var found []raw
	rows, err := db.Query(`SELECT page_id, ?, name, value FROM page_tags WHERE value LIKE ?
		UNION ALL SELECT page_id, data_type, field, value FROM vocab_index WHERE value LIKE ?`, TagsDataType, "%"+self+"%", "%"+self+"%")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.pid, &r.dataType, &r.name, &r.v); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range found {
		if InferColumnType(strings.TrimSpace(r.name)) != ColRef || !refValueNames(r.v, self) {
			continue
		}
		how := "タグ「" + r.name + "」"
		if r.dataType != TagsDataType {
			how = "表「" + vocabDisplayName(r.dataType) + "」の「" + r.name + "」"
		}
		add(refHit{PageID: r.pid, How: how})
	}
	// ② 本文のリンク・画像など・ファイル表示。
	var links []refHit
	lrows, err := db.Query(`SELECT page_id, kind FROM page_links WHERE target = ?`, target)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		var h refHit
		if err := lrows.Scan(&h.PageID, &h.How); err != nil {
			lrows.Close()
			return nil, err
		}
		links = append(links, h)
	}
	err = lrows.Err()
	lrows.Close()
	if err != nil {
		return nil, err
	}
	for _, h := range links {
		add(h)
	}
	out := hits[:0]
	for _, h := range hits {
		if !IsTemplateArea(page.FormatID(h.PageID)) {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PageID < out[j].PageID })
	return out, nil
}

// vocabDisplayName は形式の表示名（無ければ形式の名前）です。
func vocabDisplayName(dataType string) string {
	if def, ok := VocabDefByType(dataType); ok && strings.TrimSpace(def.DisplayName) != "" {
		return def.DisplayName
	}
	return dataType
}

// PagesReferringTo は、ページ id を参照の型のタグ・表のセル・本文のリンク・ファイル表示で指しているほかのページの番号を、小さい順に返します。
func PagesReferringTo(db ReadOnlyDB, id string) ([]int, error) {
	hits, err := refHitsTo(db, id)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, h := range hits {
		if len(out) == 0 || out[len(out)-1] != h.PageID {
			out = append(out, h.PageID)
		}
	}
	return out, nil
}
