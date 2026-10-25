package comm

// FAX を送るときの、送り先の番号の候補（2026-10-25 夜・fax_send.go）。
//
//	GET /api/fax/numbers?page_id=N … そのページから送るときの FAX 番号の候補 {numbers:[{number, label}]} を返す。
//
// 候補の出どころ:
//   - そのページ自身の `FAX番号` のタグ
//   - そのページの `相手`（連絡帳などのページ）の `FAX番号`
//   - 拡張が登録したもの（RegisterFaxNumberSource）——業務のページが相手をどのタグで書くかは拡張が知っている
//     （東邦なら 仕入先・客先 などの名前から連絡帳の組織を引く——ext/toho/fax_numbers.go）
import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// FaxCandidate は FAX 番号の候補1つです（number は数字だけ・label は誰の番号か）。
type FaxCandidate struct {
	Number string `json:"number"`
	Label  string `json:"label"`
}

// FaxNumberSource は、ページ pageID から送るときの候補を返す関数です（読めるものだけ返すこと）。
type FaxNumberSource func(user *auth.User, pageID int) []FaxCandidate

var faxNumberSources struct {
	sync.Mutex
	fns []FaxNumberSource
}

// RegisterFaxNumberSource は候補の出どころを足します（拡張の init() から）。
func RegisterFaxNumberSource(fn FaxNumberSource) {
	faxNumberSources.Lock()
	defer faxNumberSources.Unlock()
	faxNumberSources.fns = append(faxNumberSources.fns, fn)
}

// FaxNumbersOfPage は、ページ pid の `FAX番号` のタグを候補にします（label はページの題）。読めなければ空。
func FaxNumbersOfPage(user *auth.User, pid int) []FaxCandidate {
	if !page.CanView(user, pid) {
		return nil
	}
	tags, err := cms.TagsOfPage(database.DB, pid)
	if err != nil {
		return nil
	}
	var out []FaxCandidate
	for _, v := range tags[FaxNumberTag] {
		if n := digitsOf(v); len(n) >= 6 {
			out = append(out, FaxCandidate{Number: n, Label: cms.PageTitleByID(pid)})
		}
	}
	return out
}

// faxCandidates は、ページ pid から送るときの候補を集めます（同じ番号は1つ）。
func faxCandidates(user *auth.User, pid int) []FaxCandidate {
	var all []FaxCandidate
	all = append(all, FaxNumbersOfPage(user, pid)...)
	if cp, ok := page.NormalizeID(cms.PageTagValue(database.DB, pid, CounterpartTag)); ok {
		if n, err := strconv.Atoi(cp); err == nil {
			all = append(all, FaxNumbersOfPage(user, n)...)
		}
	}
	faxNumberSources.Lock()
	fns := append([]FaxNumberSource(nil), faxNumberSources.fns...)
	faxNumberSources.Unlock()
	for _, fn := range fns {
		all = append(all, fn(user, pid)...)
	}
	seen := map[string]bool{}
	var out []FaxCandidate
	for _, c := range all {
		if c.Number == "" || seen[c.Number] {
			continue
		}
		seen[c.Number] = true
		out = append(out, c)
	}
	return out
}

// FaxNumbersAPIHandler は GET /api/fax/numbers?page_id=N です。
func FaxNumbersAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONGet(w, r)
	if !ok {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, strings.TrimSpace(r.URL.Query().Get("page_id")))
	if !ok {
		return
	}
	if !page.RequirePageRead(w, r, pageID) {
		return
	}
	pid, _ := strconv.Atoi(pageID)
	cands := faxCandidates(user, pid)
	if cands == nil {
		cands = []FaxCandidate{}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "numbers": cands})
}
