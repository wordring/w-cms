package contacts

// ─────────────────────────────────────────────────────────────────────────
// 社名の表記ゆれを、入口で揃えるための**候補**（2026-09-20）
//
// ユーザー:「見たままと裏の動作が一致してほしいので、できればページを作る時に
// 表記ゆれを無くしたい。株式会社や有限会社、（株）などを無くした社名が連絡帳に
// あれば、それを提案するような形で、編集者の承認を得てはどうでしょう？
// ちょうどメールの整理のように」
// 「受注、加工製品、連絡帳を同じマナーで提案の欄を作ってはどうでしょう？」
//
// **3箇所が共有する1つの口**です——受注の整理・加工製品の整理・連絡帳への登録。
// 置き場が連絡帳なのは、**連絡帳が組織の持ち主**だから。下請け（`ext/subcon`）は
// 既にこのパッケージを import しています（拡張どうしの import は 2026-09-15 に解禁）。
//
// ── ⚠ これは提案であって、判定ではありません ──────────────────────
//
// 法人格を落とすと `株式会社あさひ` と `有限会社あさひ` が同じ鍵になります。
// **実在しうる別会社**なので、機械が黙って1つにしてはいけません:
//
//	「**名寄せを機械にやらせません**。題の完全一致が階層の同一性という根っこは
//	 動かしません——揺れを機械が吸収すると、**別の顧客が1つに潰れます**。
//	 候補を出すところまでが機械の仕事」（[docs/【考察】アドレス帳の作り直し.md] §7）
//
// だから返すのは候補で、**決めるのは人**です。呼ぶ側は欄に入れて見せ、人が承認します。
//
// ── アドレスの鎖のほうが強いこと ─────────────────────────────────
//
// 相手が**アドレスから引ける**なら、そちらが先です（`PartnerTitleForAddress`）——
// 鎖は `受信元 → 通信記録 → 差出人アドレス → 組織 → その題` で**全部が完全一致**、
// 推測が1つも入りません。ここは**その鎖が切れたとき**の次の手です:
// 新しい客先の1通目・社内からの転送・FAXだけの相手・人が手で打ったとき。
//
// **どちらを先に引くかは呼ぶ側が決めます**（`PagesByTag` と `PagesByTagLoose` を
// 呼ぶ側が選ぶのと同じ流儀）——加工製品は鎖が第一、連絡帳は最初から名前しか
// ありません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// OrgSuggestion は「この名前は、連絡帳のこの組織では？」という候補1件です。
type OrgSuggestion struct {
	PageID string `json:"page_id"`
	Title  string `json:"title"` // **連絡帳にある実物の題**（畳んだ形ではない）
	// Exact は題が読んだ名前とそのまま一致したかです。**真なら直すものがありません**
	// ——呼ぶ側は欄を光らせずに済みます。
	Exact bool `json:"exact"`
}

// SuggestOrg は読んだ社名に対して、連絡帳の組織の候補を返します。
//
// 探し方は2段で、**どちらも連絡帳にある実物の題を返します**:
//
//  1. **題の完全一致**（軽い正規化だけ）——直すものが無い場合。`Exact` が真。
//  2. **法人格を落とした一致**——`株式会社南北…` と `南北…` を結ぶ。
//
// 見つからなければ ok=false——**新しい客先は連絡帳に居ないのが正常**で、
// そのときは人が打ちます（推測で埋めない）。
//
// ⚠ **候補が2つ以上なら ok=false** です。`株式会社あさひ` と `有限会社あさひ` が
// どちらも在るとき、どちらかを推すと**間違ったほうを人がそのまま押します**。
// 迷うなら黙るほうが安全で、これは `PartnerByTitle` が「2枚あったら引かない」
// のと同じ判断です。
func SuggestOrg(user *auth.User, read string) (OrgSuggestion, bool) {
	read = strings.TrimSpace(read)
	if read == "" {
		return OrgSuggestion{}, false
	}
	orgs := existingPartners(user)
	if len(orgs) == 0 {
		return OrgSuggestion{}, false
	}

	// ① 題がそのまま一致するか（軽い正規化だけ——`㈱` の開きや全角空白は吸収する）。
	want := cms.NormalizeText(read)
	for _, o := range orgs {
		if cms.NormalizeText(o.Title) == want {
			return OrgSuggestion{PageID: o.ID, Title: o.Title, Exact: o.Title == read}, true
		}
	}

	// ② 法人格を落として一致するか。**1件に絞れたときだけ**返します。
	var hits []PartnerRef
	for _, o := range orgs {
		if cms.SameCompany(o.Title, read) {
			hits = append(hits, o)
		}
	}
	if len(hits) != 1 {
		return OrgSuggestion{}, false // 0件（まだ居ない）か、2件以上（決められない）
	}
	return OrgSuggestion{PageID: hits[0].ID, Title: hits[0].Title}, true
}

// SuggestOrgTitle は候補の**題だけ**を返します（呼ぶ側が欄へ入れるための短い形）。
// 見つからなければ読んだ名前をそのまま返す——**欄を空にしません**。
func SuggestOrgTitle(user *auth.User, read string) string {
	if s, ok := SuggestOrg(user, read); ok {
		return s.Title
	}
	return read
}
