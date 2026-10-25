package toho

import (
	"strings"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// faxPartyTags は、業務のページで相手の会社を書くタグです。FAX を送るとき、この名前の連絡帳の組織の `FAX番号` を候補に出す
// （2026-10-25 夜・ext/comm/fax_numbers.go）。部材発注書・部材見積依頼書は仕入先、案件ページ・納品書は客先・発注元、見積書は見積先。
var faxPartyTags = []string{SupplierTag, ClientNameTag, OrderClientTag, EstimateClientTag}

func init() {
	comm.RegisterFaxNumberSource(func(user *auth.User, pageID int) []comm.FaxCandidate {
		tags, err := cms.TagsOfPage(database.DB, pageID)
		if err != nil {
			return nil
		}
		var out []comm.FaxCandidate
		seen := map[string]bool{}
		for _, t := range faxPartyTags {
			name := strings.TrimSpace(cms.FirstTag(tags, t))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, contacts.FaxNumbersOfOrg(user, name)...)
		}
		return out
	})
}
