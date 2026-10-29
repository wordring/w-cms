package comm

// FAX を機械から受ける口（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.4）。
//
// 呼ぶのは FAX サーバー（同じ Ubuntu の機械の HylaFAX+）の受信のスクリプト（deploy/fax/）です。機械専用の入口（/api/machine/——
// 鍵で通す・internal/auth/machine.go）の下にあります。
//
//	POST /api/machine/fax（multipart）file=受けた FAX（PDF か画像）, from=送ってきた番号（あれば）, at=受けた時刻（RFC 3339）
//	  … チャネル FAX・向き受信・対応：未処理の記録を通信箱に作り、FAX を添付に置いて本文に表示する（ファイル表示の印）。
//	    送ってきた番号は、連絡帳などの `FAX番号` のタグと数字だけで比べて相手を引く。{page_id, url, title} を返す。
//
// 判定（🤖 解析）は、記録の箱の「🤖 解析（添付）」で人が押します（メールの添付と同じ・2026-10-21 夜から画像も読む）。

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// FaxNumberTag は FAX 番号のタグです（連絡帳の相手のページと、FAX の記録に書く）。
const FaxNumberTag = "FAX番号"

// FaxJobTag は、FAX サーバーが受けた FAX ごとに付ける番号のタグです（HylaFAX の commID）。
// 同じ番号で送り直されたら、記録を作り直さずに続きから（2026-10-22 の見直し——それまでは送り直しのたびに記録が増えた）。
const FaxJobTag = "FAX受信番号"

// faxJobOf は、送られてきた受信番号を英数字だけに絞ります（40字まで）。
func faxJobOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') && b.Len() < 40 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// faxKinds は FAX として受けるファイルの種類です（FAX サーバーの側で PDF にして渡す決まり。画像も受ける）。
// ⚠ TIFF は受けません——ブラウザも Gemini も読めないので、FAX サーバーの側で PDF にしてから渡します（deploy/fax/）。
var faxKinds = map[string]bool{".pdf": true, ".png": true, ".jpg": true, ".jpeg": true}

// faxOwner は、番号 number を `FAX番号` のタグに持つページ（通信記録は除く）を引きます（phoneOwner と同じ形）。
func faxOwner(user *auth.User, number string) (int, string, bool) {
	want := digitsOf(number)
	if len(want) < 6 {
		return 0, "", false
	}
	rows, err := database.DB.Query(`SELECT page_id, value FROM page_tags WHERE name = ?`, FaxNumberTag)
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
			continue
		}
		return pid, cms.PageTitleByID(pid), true
	}
	return 0, "", false
}

// MachineFaxAPIHandler は POST /api/machine/fax です（機械専用の入口・鍵で通す）。
func MachineFaxAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !cms.ParseUploadForm(w, r) {
		return
	}
	// 人のアップロードと同じ守り（名前の検査・PDF は先頭の印・画像は種別とメタデータの除去）。
	name, content, ok := cms.UploadedFile(w, r, "file", faxKinds, "FAX は PDF か画像で渡してください")
	if !ok {
		return
	}
	at := parseCallTime(r.FormValue("at"))
	from := strings.TrimSpace(r.FormValue("from"))
	job := faxJobOf(r.FormValue("job"))
	if job != "" {
		unlock := lockIntakeRef(FaxJobTag + "=" + job)
		defer unlock()
	}

	boxID, ok := MailBoxPageID()
	if !ok {
		cms.JSONFail(w, http.StatusConflict, ErrNoMailBox.Error())
		return
	}
	if !page.RequirePageWrite(w, r, boxID) {
		return
	}
	tmpl, err := cms.PageTemplateBody(MemoTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "記録を作れません: "+err.Error())
		return
	}
	counterpart, who := "", ""
	if id, title, found := faxOwner(user, from); found {
		counterpart, who = page.FormatID(id), title
	}
	label := who
	if label == "" {
		label = digitsOf(from)
	}
	if label == "" {
		label = "番号なし"
	}
	title := ChannelFax + " " + label + " " + at.In(time.Local).Format("2006-01-02 15:04")
	// 同じ受信番号の記録があれば、それを使う（FAX がもう表示されていれば、何もしない）。
	pageID, dup := ExistingIntakePage(FaxJobTag, job)
	if dup {
		if cur, _ := cms.ReadPageBody(pageID); strings.Contains(cur, `data-type="file-view"`) {
			auth.Audit(user.Username, "intake.duplicate", pageID+" ("+FaxJobTag+"="+job+")")
			cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "url": "/" + pageID, "title": cms.PageTitleByID(atoiPage(pageID)), "duplicate": true})
			return
		}
	} else {
		body := memoBodyHTML(tmpl, ChannelFax, title, DirectionIn, "", counterpart, at)
		if job != "" {
			d := cms.EditBody(body)
			d.SetTag(FaxJobTag, job)
			body = d.HTML()
		}
		if pageID, err = CreateRecordPage(boxID, user.Username, at, body); err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "記録を作れません: "+err.Error())
			return
		}
	}
	// FAX を添付に置き、本文に表示する。番号もタグに。
	attachID, _, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "fax:"+digitsOf(from), content)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "FAX を置けません: "+err.Error())
		return
	}
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		d := cms.EditBody(cur)
		if n := digitsOf(from); n != "" {
			d.SetTag(FaxNumberTag, n)
		}
		return strings.TrimRight(d.HTML(), "\n") + "\n" + `<section data-type="file-view" data-ref="` + pageID + `-` + attachID + `"></section>`
	}); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "記録に FAX を表示できません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "intake.fax", pageID+" ("+digitsOf(from)+")")
	NotifyArrival(Arrival{PageID: pageID, Title: title, Channel: ChannelFax})
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "url": "/" + pageID, "title": title})
}
