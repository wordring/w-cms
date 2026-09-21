package subcon

// ─────────────────────────────────────────────────────────────────────────
// 集計が共有する小さな引き方（2026-09-22 に寄せた）
//
// 09-21 の集計4本（手配状況・未手配・参考単価・材料の検索）と 09-03 の
// `RequiredMaterials` が、**閲覧者の可視判定を覚える7行**を5回写していました。
// 手配状況と未手配は「受注明細の1行から加工製品ページを引く」9行も2回。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// viewCheck は閲覧者の可視判定を、**ページごとに1度だけ**引く形で返します。
//
// 集計は同じページを何度も当たるので（品番ごと・行ごと）、素の `page.CanView` を
// 毎回呼ぶとサイドカーと索引を何度も読みます。
//
// ⚠ **読めないページは黙って落とす**（見せ分け・C案）のは呼ぶ側の判断で、ここは
// 覚えるだけです。⚠ **試験はファイルDBで**——`page.CanView` を通るので、`:memory:` では
// 静かに全部 false になります（引き継ぎの罠）。
func viewCheck(user *auth.User) func(int) bool {
	seen := map[int]bool{}
	return func(id int) bool {
		if v, ok := seen[id]; ok {
			return v
		}
		v := page.CanView(user, id)
		seen[id] = v
		return v
	}
}

// productOfOrderRow は受注明細の1行から、その加工製品ページを引きます。
//
// ⚠ **結び方は2通りあります**——**`弊社品番`（ページ参照・2026-09-20）**と、
// **`品番` から番号のタグを逆引きする道**（`productByCode`）。⚠ **古いページは
// 後者しか持ちません**ので、片方だけ見ると**移行前のページが丸ごと出なくなります**。
// 逆引きは2枚以上に当たれば引きません（決めるのは人）。
func productOfOrderRow(db cms.ReadOnlyDB, r cms.VocabRow) (int, bool) {
	if id, ok := page.NormalizeID(strings.TrimSpace(r.Values["our-item-id"])); ok && id != "" {
		return pageNum(id), true
	}
	return productByCode(db, strings.TrimSpace(r.Values["item-id"]))
}
