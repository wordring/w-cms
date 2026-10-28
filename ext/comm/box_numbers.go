package comm

// 通信箱から電話をかける・FAX を送るときの、相手の番号の候補（2026-10-28）。
//
//	GET /api/comm/numbers?kind=phone|fax … 見る人が読めるページの `電話番号`（phone）か `FAX番号`（fax）を
//	                                       {numbers:[{number, label, page}]} で返す（連絡帳の人・組織など）。
//
// 記録（チャネルのあるページ）の番号は入れない——届いた・かけた記録の番号は、相手のページではないため。
//
// > 2026-10-28 利用者:「FAXの送信と電話のダイヤルを通信フォルダから出来るようにしたいです。」

import (
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// boxNumberMax は候補の数の上限です（画面の候補の一覧が重くならないように）。
const boxNumberMax = 2000

// BoxNumber は通信箱の候補1つです（number は書かれたまま・label は誰の番号か・page はそのページ）。
type BoxNumber struct {
	Number string `json:"number"`
	Label  string `json:"label"`
	Page   string `json:"page"`
}

// boxNumbers は、user が読めるページの tag（電話番号・FAX番号）の値を候補にします（題の順・同じページの同じ番号は1つ）。
func boxNumbers(user *auth.User, tag string) []BoxNumber {
	rows, err := database.DB.Query(`SELECT page_id, value FROM page_tags WHERE name = ?`, tag)
	if err != nil {
		return nil
	}
	type pv struct {
		pid int
		v   string
	}
	var all []pv
	for rows.Next() {
		var x pv
		if rows.Scan(&x.pid, &x.v) == nil && len(digitsOf(x.v)) >= 6 {
			all = append(all, x)
		}
	}
	rows.Close()
	seen := map[string]bool{}
	var out []BoxNumber
	for _, x := range all {
		key := page.FormatID(x.pid) + "/" + digitsOf(x.v)
		if seen[key] || !page.CanView(user, x.pid) || cms.IsTemplateArea(page.FormatID(x.pid)) ||
			strings.TrimSpace(cms.PageTagValue(database.DB, x.pid, ChannelTag)) != "" {
			continue
		}
		seen[key] = true
		out = append(out, BoxNumber{Number: strings.TrimSpace(x.v), Label: cms.PageTitleByID(x.pid), Page: page.FormatID(x.pid)})
		if len(out) >= boxNumberMax {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// BoxNumbersAPIHandler は GET /api/comm/numbers?kind=phone|fax です。
func BoxNumbersAPIHandler(w http.ResponseWriter, r *http.Request) {
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusUnauthorized, "ログインしてください")
		return
	}
	tag := PhoneTag
	if r.URL.Query().Get("kind") == "fax" {
		tag = FaxNumberTag
	}
	cms.WriteJSON(w, map[string]any{"success": true, "numbers": boxNumbers(user, tag)})
}
