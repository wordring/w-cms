package toho

// ─────────────────────────────────────────────────────────────────────────
// 品番は「客先＋品番」で引く（2026-10-01）
//
// 利用者:「家で、品番はユニークということになったのですが、会社が違えば同じ品番が偶然重なることもあり得ます。
// そこで、検索時には顧客ID（または顧客名）と品番で特定してください」「つまり、会社が違えば同じ品番を許します。
// 同じ会社内の同一品番は警告し、人間が対応します」。
//
// 受注の行から加工製品ページを引くとき（結ぶ・必要部材表・結びの気づき）は、**受注ページの `発注元` と同じ客先の
// 加工製品ページだけ**を候補にする。加工製品ページの客先は:
//
//  1. 置き場——`取引先／社名／加工製品／…` の**社名**（取引先の箱の直下の先祖の題）。整理したページはこれで決まる。
//  2. 置き場の外（通信箱の下で整理を待つ・移行の途中など）は、図面ブロックの `客先` タグ。
//
// 名前は法人格と表記を畳んで比べる（`cms.FoldCompanyName`——連絡帳に寄せた題と、読んだままの社名を同じに扱う）。
// ⚠ **受注に発注元が無ければ引かない**（客先が分からないので品番だけでは特定しない）。
// ⚠ 同じ客先の中で2枚以上に当たったら、どれにも結ばない（決めるのは人——鏡の気づきが ⚠ で知らせる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"w-cms/internal/cms"
)

// foldCustomer は客先の名前を比べる形にします（空なら空）。
func foldCustomer(name string) string {
	return cms.FoldCompanyName(cms.NormalizeNameForIngest(strings.TrimSpace(name)))
}

// productCustomerOf は加工製品ページの客先の名前です（置き場の社名・置き場の外なら `客先` タグ・分からなければ空）。
func productCustomerOf(db cms.ReadOnlyDB, id int) string {
	if boxStr, ok := CustomerBoxPageID(); ok {
		if box, err := strconv.Atoi(boxStr); err == nil {
			cur := id
			for depth := 0; depth < 12 && cur > 0; depth++ {
				var parent int
				if err := db.QueryRow(`SELECT COALESCE(parent_id, 0) FROM pages WHERE id = ?`, cur).Scan(&parent); err != nil {
					break
				}
				if parent == box {
					return cms.PageTitleByID(cur) // 取引先の箱の直下＝社名のページ
				}
				cur = parent
			}
		}
	}
	return cms.PageTagValue(db, id, ClientNameTag)
}

// productPagesForCustomer は、その客先の加工製品ページのうち番号の当たるものを返します（客先が空なら何も返さない）。
func productPagesForCustomer(db cms.ReadOnlyDB, customer, code string) []int {
	want := foldCustomer(customer)
	if want == "" || strings.TrimSpace(code) == "" {
		return nil
	}
	var out []int
	for _, id := range pagesByAnyTag(db, ProductCodeTags(), code) {
		if foldCustomer(productCustomerOf(db, id)) == want {
			out = append(out, id)
		}
	}
	return out
}
