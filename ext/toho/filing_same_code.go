package toho

// ─────────────────────────────────────────────────────────────────────────
// 整理の「新規」で、同じ客先に同じ品番の加工製品ページが既にあれば確かめる（2026-10-19）
//
// 2026-10-01 利用者:「とすると、二つの加工製品ページが同じ品番を持つのはマズいので、加工製品ページを作る前にチェックし、
// 編集者に選択させる必要があります」「つまり、会社が違えば同じ品番を許します。同じ会社内の同一品番は警告し、人間が対応します」
//
// 2026-10-19 に利用者が選んだ形（【要求】東邦拡張 §12・【考察】同じ品番の加工製品ページ）:
//
//   - **確かめるのは整理の「新規」のとき**——🤖解析は今までどおり整理を待つページを作る（解析の時点では客先を読み違えることがある）。
//   - 同じ客先（製造台帳の社名）に、同じ品番（設定 `product_code_tags`＝図面番号・品番・部品番号）のページが既にあれば、
//     ダイアログで「別の品物として作る」か「やめる」かを聞く。やめたら、整理の欄で「図面追加」「図面改定」「重複（取り込まない）」を選び直す。
//   - 比べる相手は、整理を済ませたページ（製造台帳の下）だけ。旧版（図面改定で子へ移ったページ）と、自分は除く。
//   - 「作る」を選んで通したことは監査に残す（あとで「なぜ同じ品番が2枚あるのか」を調べる手掛かり）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// sameCodeProducts は、加工製品ページ id と同じ客先 customer に、同じ品番を持つ整理済みの加工製品ページを返します
// （読めないページ・旧版・自分は除く）。codes は当たった番号（人に見せる）。
func sameCodeProducts(db cms.ReadOnlyDB, user *auth.User, id int, customer string) (hits []int, codes []string) {
	tags, err := cms.TagsOfPage(db, id)
	if err != nil {
		return nil, nil
	}
	seen := map[int]bool{}
	seenCode := map[string]bool{}
	for _, name := range ProductCodeTags() {
		for _, v := range tags[name] {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			for _, hit := range productPagesForCustomer(db, customer, v, "") {
				if hit == id || seen[hit] || !isFiledProduct(db, hit) || pageKindOf(hit) == PageKindProductOld {
					continue
				}
				if user != nil && !page.CanView(user, hit) {
					continue
				}
				seen[hit] = true
				hits = append(hits, hit)
				if !seenCode[v] {
					seenCode[v] = true
					codes = append(codes, v)
				}
			}
		}
	}
	sort.Ints(hits)
	return hits, codes
}

// sameCodeMessage は確かめのダイアログの文です。
func sameCodeMessage(customer string, hits []int, codes []string) string {
	var list []string
	for _, h := range firstNInts(hits, 5) {
		list = append(list, "/"+page.FormatID(h)+" "+cms.PageTitleByID(h))
	}
	more := ""
	if len(hits) > 5 {
		more = " ほか " + strconv.Itoa(len(hits)-5) + " 枚"
	}
	return "「" + customer + "」に、同じ品番（" + strings.Join(codes, "・") + "）の加工製品ページが既にあります: " +
		strings.Join(list, "、") + more + "。" +
		"同じ品物なら「やめる」を押して、整理の欄で「図面追加」「図面改定」「重複（取り込まない）」を選び直してください。" +
		"別の品物なら「別の品物として作る」を押してください"
}

func firstNInts(xs []int, n int) []int {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}
