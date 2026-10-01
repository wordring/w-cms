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
//
// **照合はまず品番と品名で、合わなければ品番だけで**（2026-10-01 利用者:「検索や結びの照合は、最初に品番と品名で
// 行うべきです。一致しない場合に、ほかの方法を試せば良いと思います」）——同じ品番のページが2枚あっても、品名で
// 1枚に決まれば結べる（同じ図番の左右品・一式の図面を名乗っていたページなど）。加工製品ページの品名は `品名` タグ
// （改定で品名が変わると2つ持つ）と、図面の `図面名称`。⚠ 題は見ない（目印を足すので判断の基準にならない）。
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
//
// name（受注の品名）があれば、**先に品番と品名の両方が合うページ**に絞ります——1枚でも当たればそれを返し、
// 1枚も当たらなければ品番だけで当たったページを返します（上の「照合はまず品番と品名で」）。
func productPagesForCustomer(db cms.ReadOnlyDB, customer, code, name string) []int {
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
	if key := itemNameKey(name); key != "" && len(out) > 0 {
		var named []int
		for _, id := range out {
			if productNameKeys(db, id)[key] {
				named = append(named, id)
			}
		}
		if len(named) > 0 {
			return named
		}
	}
	return out
}

// itemNameKey は品名を比べる形にします（空白・ハイフン類・長音・全角半角・大小を畳む）。
func itemNameKey(name string) string {
	return cms.NormalizeCode(strings.TrimSpace(name))
}

// productNameKeys は加工製品ページの品名（`品名` タグと図面の `図面名称`）を比べる形で返します。
func productNameKeys(db cms.ReadOnlyDB, id int) map[string]bool {
	out := map[string]bool{}
	tags, err := cms.TagsOfPage(db, id)
	if err != nil {
		return out
	}
	for _, tag := range []string{ItemNameTag, DrawingNameTag} {
		for _, v := range tags[tag] {
			if k := itemNameKey(v); k != "" {
				out[k] = true
			}
		}
	}
	return out
}
