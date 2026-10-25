package comm

// FAX を送る口（2026-10-25 夜・deploy/fax/README.md）。
//
// 利用者:「とりあえず実装してみて、間に合わなかったらまた明日の夜やりましょう」——複合機を FAX の線から外したので、
// FAX を送る手段を w-cms に持たせる。送るのは、同じ機械の HylaFAX（sendfax）です。
//
//	POST /api/fax/send   {page_id, file, number} … ページに添付した PDF を、その番号へ送る仕事を HylaFAX に頼む。{job} を返す。
//	GET  /api/fax/status?job=N                  … その仕事の様子（HylaFAX の faxstat の1行）。{state, line} を返す。
//
// 画面は、PDF のファイル表示の「⋯」の「📠 FAX で送る」（ext/comm/assets/comm.js・window.wcms.onFileMenu）。
//
// ⚠ まだ足りないもの: 送った FAX を通信箱の記録（向き送信・チャネル FAX）にすること・部材発注書などの「送る」から送ること・
//
//	送れたかを後から知らせること。いまは監査（fax.send）と、画面での様子の確かめだけ。
import (
	"context"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// sendfaxPath・faxstatPath は HylaFAX の道具の場所です（Ubuntu の hylafax-client）。試験で差し替える。
var (
	sendfaxPath = "/usr/bin/sendfax"
	faxstatPath = "/usr/bin/faxstat"
)

// faxSendNumber は、打たれた FAX の番号を数字だけにします（ハイフン・空白・括弧を落とす）。使えない番号なら空を返す。
// ⚠ 数字だけに絞るのは、sendfax の引数に人の書いた文字をそのまま渡さないため。
func faxSendNumber(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '０' && r <= '９':
			b.WriteRune('0' + (r - '０'))
		case r == '-' || r == 'ー' || r == '－' || r == ' ' || r == '　' || r == '(' || r == ')' || r == '（' || r == '）':
		default:
			return ""
		}
	}
	n := b.String()
	if len(n) < 6 || len(n) > 15 {
		return ""
	}
	return n
}

var sendfaxJobRe = regexp.MustCompile(`request id is (\d+)`)

type faxSendRequest struct {
	PageID string `json:"page_id"`
	File   string `json:"file"`
	Number string `json:"number"`
}

// FaxSendAPIHandler は POST /api/fax/send です。
func FaxSendAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req faxSendRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	// 社外へ出す操作なので、そのページに書ける人だけ（読むだけの人は送れない）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	fileName, err := cms.SafeAttachmentName(pageID, req.File, map[string]bool{".pdf": true}, "FAX で送れるのは PDF だけです")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	path, found := page.AttachmentPath(pageID, fileName)
	if !found {
		cms.JSONFail(w, http.StatusNotFound, "添付が見つかりません")
		return
	}
	number := faxSendNumber(req.Number)
	if number == "" {
		cms.JSONFail(w, http.StatusBadRequest, "FAX の番号が読めません（数字6〜15桁。ハイフンは入れてもよい）")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	// -n: 送り状を付けない。-N: 済んだ知らせのメールを送らない（この機械にはメールの道具が無い）。
	out, err := exec.CommandContext(ctx, sendfaxPath, "-n", "-N", "-d", number, path).CombinedOutput()
	m := sendfaxJobRe.FindStringSubmatch(string(out))
	if err != nil || m == nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, "FAX の道具に頼めませんでした: "+firstFaxLine(string(out), err))
		return
	}
	auth.Audit(user.Username, "fax.send", pageID+"/"+fileName+" -> "+number+" (job "+m[1]+")")
	cms.WriteJSON(w, map[string]any{"success": true, "job": m[1], "number": number})
}

// FaxStatusAPIHandler は GET /api/fax/status?job=N です。state は「送っている・待っている」（queued）・「送れた」（done）・
// 「送れなかった」（failed）・「分からない」（unknown）のどれか。line は HylaFAX の faxstat の1行（理由の文が入る）。
func FaxStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := cms.GateJSONGet(w, r); !ok {
		return
	}
	job := r.URL.Query().Get("job")
	if job == "" || strings.Trim(job, "0123456789") != "" || len(job) > 9 {
		cms.JSONFail(w, http.StatusBadRequest, "job が読めません")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	// -s: 送る待ちと送っている最中・-d: 済んだもの（送れた・送れなかった）。
	out, _ := exec.CommandContext(ctx, faxstatPath, "-s", "-d").CombinedOutput()
	state, line := faxJobState(string(out), job)
	cms.WriteJSON(w, map[string]any{"success": true, "state": state, "line": line})
}

// faxJobState は faxstat の出力から、仕事 job の行と様子を読みます。
// 行の形は「JID Pri S Owner Number Pages Dials TTS Status」で、S が D（送れた）・F（送れなかった）・それ以外は途中。
func faxJobState(out, job string) (string, string) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[0] != job {
			continue
		}
		switch f[2] {
		case "D":
			return "done", strings.TrimSpace(l)
		case "F":
			return "failed", strings.TrimSpace(l)
		default:
			return "queued", strings.TrimSpace(l)
		}
	}
	return "unknown", ""
}

// firstFaxLine は道具の出力の最初の1行（無ければ誤りの文）を返します。
func firstFaxLine(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		}
		return s
	}
	if err != nil {
		return err.Error()
	}
	return "応答がありません"
}
