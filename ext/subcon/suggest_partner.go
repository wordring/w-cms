package subcon

// ─────────────────────────────────────────────────────────────────────────
// 「推奨業者」の入力候補（2026-09-21）
//
// ユーザー選択:「**文字列だが、候補を出す**」。`ref`（取引先ページへの参照）なら
// 表記ゆれが構造的に起きませんが、**先に取引先ページが要ります**。文字列＋候補なら
// **在るものは揃い、無いものも打てる**——顧客名のコンボボックスと同じ手です。
//
// ⚠ **揃うのは採ったときだけ**で、手打ちは止められません。それでも意味があるのは、
// **打つより選ぶほうが速い**からです。速ければ揃います。
//
// 候補は **`取引先` の下の社名ページの題**です。⚠ **連絡帳ではありません**
// ——連絡帳は「誰と話したか」の木で、外注先は取引の相手（`取引先`）だからです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PartnerSuggestSource は候補の出どころの名前です（宣言と登録で同じ字を使う）。
const PartnerSuggestSource = "subcon.partners"

func init() { cms.RegisterSuggestSource(PartnerSuggestSource, suggestPartners) }

// suggestPartners は `取引先` の下の社名ページの題を返します。
//
// ⚠ **読めないページは混ぜません**——題そのものが情報です（誰と取引しているか）。
// ⚠ **`取引先` が無ければ空**を返します（壊れではなく「まだ登録していない」）。
func suggestPartners(user *auth.User, q string) []string {
	boxID, ok := cms.TopLevelPageByTitle(CustomerBoxTitle)
	if !ok {
		return nil
	}
	rows, qerr := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title`, boxID)
	if qerr != nil {
		return nil
	}
	// ⚠ **先に読み切ってから絞ります**——行を読みながら `page.CanView` を投げると、
	//    `:memory:` DBでは空の別DBに当たり、絞り込みが静かに全部落ちます。
	type row struct {
		id    int
		title string
	}
	var all []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.title) == nil {
			all = append(all, r)
		}
	}
	rows.Close()

	needle := cms.NormalizeText(strings.TrimSpace(q))
	var out []string
	for _, r := range all {
		if strings.TrimSpace(r.title) == "" || !page.CanView(user, r.id) {
			continue
		}
		if needle != "" && !strings.Contains(cms.NormalizeText(r.title), needle) {
			continue
		}
		out = append(out, r.title)
	}
	return out
}
