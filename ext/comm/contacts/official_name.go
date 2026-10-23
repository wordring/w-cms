package contacts

// 組織の正式名称（2026-10-23）。
//
// 連絡帳の組織のページの題は、ふだん呼ぶ名前（法人格を落とした形など）です。相手に出す紙には正式な名前を刷りたいので、
// 組織のページにタグ「正式名称」を書いておき、紙を作るときに引きます（書かれていなければ、題のまま）。
//
// > 2026-10-23 利用者:「見積書には正式名を入れたいです。正式名称をどこかに書いておいて入れられるでしょうか？
// > 書くとしたら連絡帳になると思います」

import (
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// OfficialNameTag は組織のページの、正式な名前のタグです（型は設定の語彙で text）。
const OfficialNameTag = "正式名称"

// OfficialName は、連絡帳の組織 name（題）の正式名称を返します。組織が見つからない・2つある・正式名称が空なら、name のまま。
func OfficialName(user *auth.User, name string) string {
	name = strings.TrimSpace(name)
	id, ok := PartnerByTitle(user, name)
	if !ok {
		return name
	}
	n, err := strconv.Atoi(id) // PartnerByTitle は6桁のページ番号を返す
	if err != nil {
		return name
	}
	tags, err := cms.TagsOfPage(database.DB, n)
	if err != nil {
		return name
	}
	if v := strings.TrimSpace(cms.FirstTag(tags, OfficialNameTag)); v != "" {
		return v
	}
	return name
}
