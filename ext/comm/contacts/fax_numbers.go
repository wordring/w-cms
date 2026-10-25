package contacts

import (
	"strconv"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
)

// FaxNumbersOfOrg は、連絡帳の組織 name（題）とその下の人の `FAX番号` を、FAX を送るときの候補にします（2026-10-25 夜）。
// 組織の番号を先に、人の番号をあとに並べます。組織が見つからなければ空。業務のページから送るとき、拡張が相手の名前を渡す
// （ext/toho/fax_numbers.go）。
func FaxNumbersOfOrg(user *auth.User, name string) []comm.FaxCandidate {
	id, ok := PartnerByTitle(user, name)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return nil
	}
	out := comm.FaxNumbersOfPage(user, n)
	for _, p := range PersonsOf(user, id) {
		if pn, err := strconv.Atoi(p.ID); err == nil {
			out = append(out, comm.FaxNumbersOfPage(user, pn)...)
		}
	}
	return out
}
