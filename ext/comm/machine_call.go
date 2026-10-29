package comm

// 電話の着信を機械から受ける口（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.3）。
//
// > 2026-10-21 利用者:「出た着信だけ記録にします。セールスなど不要なものだった場合、人間が消します。電話中に記録するメモとも
// > いえるので、電話に出たら素早くメモを作って、しかもユーザーがアクセスする手段を提供して欲しいです。」
//
// 呼ぶのは電話の道具（cmd/w-cms-phone——アロハ ND6 の電文を読む）です。機械専用の入口（/api/machine/——鍵で通す・
// internal/auth/machine.go）の下にあります。
//
//	POST /api/machine/call {event:"answered", number, at}  … 出た着信。チャネル電話・向き受信・対応：未処理の記録を作り、
//	                                                        {page_id, url, title} を返す（道具はその URL を開ける）
//	POST /api/machine/call {event:"hangup", page_id, at}    … 受話器を置いた。その記録に「通話終了」の時刻を書き足す
//	POST /api/machine/call {event:"missed", number, at, ended} … 出る前に鳴りやんだ（2026-10-28）。ページは作らず、出ていない着信の
//	                                                        履歴に残す（missed_call.go・人が通信箱で「記録」を押したら作る）
//
// 番号の代わりに ND6 が送る印（P＝非通知・O＝表示圏外・C＝公衆電話・S＝サービス競合・E＝受信の誤り）は、言葉にして題に入れる。
// 番号は、連絡帳などの `電話番号` のタグと数字だけで比べて、相手（ページ参照）を引く（phoneOwner）。

import (
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// CallEndTag は通話の終わった時刻のタグです（受話器を置いたとき・電話の道具が書く）。
const CallEndTag = "通話終了"

// callerMarks は、番号の代わりに届く印の言葉です（アロハ ND6 の機能仕様書の 9-2）。
var callerMarks = map[string]string{"P": "非通知", "O": "表示圏外", "C": "公衆電話", "S": "サービス競合", "E": "番号を受けられませんでした"}

// digitsOf は数字だけを残します（全角の数字も半角へ）。
//
// 国の番号で始まる番号（「+81 3 1234 5678」——FAX の機械はよくこう名乗る）は、国内の形（03…）にする
// （連絡帳の番号と合わせるため・2026-10-22 の見直し）。「+81 (0)3…」の括弧の 0 も1つにまとめる。
func digitsOf(s string) string {
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "+") || strings.HasPrefix(t, "＋") {
		if d := plainDigits(t); strings.HasPrefix(d, "81") && len(d) > 3 {
			return "0" + strings.TrimPrefix(d[2:], "0")
		}
	}
	return plainDigits(s)
}

// plainDigits は数字だけを残します（全角の数字は半角に）。
func plainDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '０' && r <= '９' {
			r = '0' + (r - '０')
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// phoneOwner は、番号 number を `電話番号` のタグに持つページ（通信記録は除く）を引きます（numberOwner）。
func phoneOwner(user *auth.User, number string) (id int, title string, ok bool) {
	return numberOwner(user, PhoneTag, number)
}

// numberOwner は、番号 number（数字が6けた以上）をタグ tag に持つページ（通信記録は除く）を引きます。見えるもののうちページ番号の
// 小さいもの（連絡帳の先にできたページ）。無ければ ok=false。電話と FAX の写し（phoneOwner・faxOwner）を寄せた（2026-10-29）。
func numberOwner(user *auth.User, tag, number string) (id int, title string, ok bool) {
	want := digitsOf(number)
	if len(want) < 6 {
		return 0, "", false
	}
	rows, err := database.DB.Query(`SELECT page_id, value FROM page_tags WHERE name = ?`, tag)
	if err != nil {
		return 0, "", false
	}
	var ids []int
	for rows.Next() {
		var pid int
		var v string
		if rows.Scan(&pid, &v) == nil && digitsOf(v) == want {
			ids = append(ids, pid)
		}
	}
	rows.Close()
	sort.Ints(ids)
	for _, pid := range ids {
		if !page.CanView(user, pid) || strings.TrimSpace(cms.PageTagValue(database.DB, pid, ChannelTag)) != "" {
			continue // 通信記録（前の電話の記録）は相手ではない
		}
		return pid, cms.PageTitleByID(pid), true
	}
	return 0, "", false
}

// callTitle は、出た着信の記録の題です（`電話 相手 10:15` か `電話 0312345678 10:15`、印なら `電話 非通知 10:15`）。
func callTitle(number, who string, at time.Time) string {
	label := strings.TrimSpace(who)
	if label == "" {
		if m, ok := callerMarks[strings.TrimSpace(number)]; ok {
			label = m
		} else if d := digitsOf(number); d != "" {
			label = d
		} else {
			label = "番号なし"
		}
	}
	return ChannelPhone + " " + strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, label) + " " + at.In(time.Local).Format("2006-01-02 15:04")
}

// parseCallTime は道具が送る時刻（RFC 3339）を読みます。読めなければ今。
func parseCallTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
		return t
	}
	return time.Now()
}

// MachineCallAPIHandler は POST /api/machine/call です（機械専用の入口・鍵で通す）。
func MachineCallAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Event  string `json:"event"`
		Number string `json:"number"`
		At     string `json:"at"`
		PageID string `json:"page_id"`
		Ended  string `json:"ended"` // missed のときの鳴りやんだ時刻
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	at := parseCallTime(req.At)
	switch strings.TrimSpace(req.Event) {
	case "answered":
		if _, ok := mailBoxForWrite(w, r); !ok {
			return
		}
		pageID, title, err := createCallRecord(user, req.Number, at, false, nil)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, err.Error())
			return
		}
		auth.Audit(user.Username, "intake.call", pageID+" ("+digitsOf(req.Number)+")")
		NotifyArrival(Arrival{PageID: pageID, Title: title, Channel: ChannelPhone})
		cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "url": "/" + pageID, "title": title})
	case "hangup":
		id, ok := page.NormalizeID(strings.TrimSpace(req.PageID))
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "page_id が読めません")
			return
		}
		if strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(id), ChannelTag)) != ChannelPhone {
			cms.JSONFail(w, http.StatusBadRequest, "電話の記録ではありません")
			return
		}
		if !page.RequirePageWrite(w, r, id) {
			return
		}
		if err := cms.RewriteBody(id, user.Username, func(cur string) string {
			d := cms.EditBody(cur)
			d.ReplaceTag(CallEndTag, at.In(time.Local).Format(time.RFC3339))
			return d.HTML()
		}); err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "書けません: "+err.Error())
			return
		}
		cms.WriteJSON(w, map[string]any{"success": true, "page_id": id})
	case "missed":
		// ページは作らない（2026-10-28 利用者:「なった記録だけして、編集者がページにするか選択すればよい」）。書き手が通信箱に書けることは見る。
		boxID, ok := mailBoxForWrite(w, r)
		if !ok {
			return
		}
		ended := at
		if strings.TrimSpace(req.Ended) != "" {
			ended = parseCallTime(req.Ended)
		}
		m, err := addMissedCall(req.Number, at, ended)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "履歴に残せません: "+err.Error())
			return
		}
		auth.Audit(user.Username, "intake.call.ring", m.ID)
		cms.NotifyViewsChanged(boxID) // 開いている通信箱を描き直す（2026-10-28 利用者:「再描画でスクロールしたり、開いていたパネルが閉じたりしないなら、描き直してください。」）
		cms.WriteJSON(w, map[string]any{"success": true, "id": m.ID})
	default:
		cms.JSONFail(w, http.StatusBadRequest, "event は answered・hangup・missed のどれかです")
	}
}

// atoiPage は6桁のページ番号を数にします（読めなければ 0）。
func atoiPage(id string) int {
	n := 0
	for _, r := range id {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
