package comm

// FAX を送る口（2026-10-25 夜・deploy/fax/README.md）。
//
// 利用者:「とりあえず実装してみて、間に合わなかったらまた明日の夜やりましょう」——複合機を FAX の線から外したので、
// FAX を送る手段を w-cms に持たせる。送るのは、同じ機械の HylaFAX（sendfax）です。
//
//	POST /api/fax/send   {page_id, file, number} … ページに添付した PDF を、その番号へ送る仕事を HylaFAX に頼む。
//	                                              送った FAX を通信箱の記録にして {job, record} を返す。
//	GET  /api/fax/status?job=N                  … その仕事の様子（HylaFAX の faxstat の1行）。{state, line} を返す。
//
// 画面は、PDF のファイル表示の「⋯」の「📠 FAX で送る」（ext/comm/assets/comm.js・window.wcms.onFileMenu）。
//
// 送った FAX の記録（2026-10-25 夜）: 通信箱に、向き送信・チャネル FAX・対応 済の記録を作り、送った PDF を写して表示する。
// 親ページID は送った元のページ。送れたかはサーバーが見張り（watchFaxJob）、記録の「FAX送信の様子」を送れた・送れなかったに
// 書き替える。送れなかったら対応を未処理に戻し、通知（スマホと画面）で知らせる。サーバーを起こし直しても、送っている途中の
// 記録の見張りは続きから（init の裏の仕事）。
//
// ⚠ まだ足りないもの: 部材発注書などの「送る」から送ること。
import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// FaxSendJobTag は、w-cms から送った FAX の、HylaFAX の仕事の番号です（送った FAX の記録に書く）。
const FaxSendJobTag = "FAX送信番号"

// FaxSendStateTag は、送った FAX の様子です（送っている・送れた・送れなかった：理由）。
const FaxSendStateTag = "FAX送信の様子"

const (
	faxStateSending = "送っている"
	faxStateDone    = "送れた"
	faxStateFailed  = "送れなかった"
)

// faxWatchInterval は、送った FAX の様子を見に行く間隔です。faxWatchFor は見張りをやめるまでの長さ
// （話し中のかけ直しを含めても、HylaFAX は数時間で諦める）。試験で縮める。
var (
	faxWatchInterval = 20 * time.Second
	faxWatchFor      = 12 * time.Hour
)

// faxWatching は、いま見張っている仕事の番号です（同じ仕事を2つの見張りで見ない）。
var faxWatching sync.Map

func init() {
	// サーバーを起こし直しても、送っている途中の記録の見張りを続ける。
	cms.RegisterBackgroundTask("送った FAX の見張り", resumeFaxWatches)
}

// sendfaxPath・faxstatPath は HylaFAX の道具の場所です（Ubuntu の hylafax-client）。試験で差し替える。
var (
	sendfaxPath = "/usr/bin/sendfax"
	faxstatPath = "/usr/bin/faxstat"
)

// runSendfax は HylaFAX に送る仕事を頼みます（-n: 送り状を付けない・-N: 済んだ知らせのメールを送らない——この機械には
// メールの道具が無い）。readFaxStat は HylaFAX の仕事の一覧（-s: 待ちと送っている最中・-d: 済んだもの）を読みます。
// どちらも、HylaFAX の無い機械の試験で差し替える。
var (
	runSendfax = func(ctx context.Context, number, path string) ([]byte, error) {
		return exec.CommandContext(ctx, sendfaxPath, "-n", "-N", "-d", number, path).CombinedOutput()
	}
	readFaxStat = func(ctx context.Context) string {
		out, _ := exec.CommandContext(ctx, faxstatPath, "-s", "-d").CombinedOutput()
		return string(out)
	}
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
	out, err := runSendfax(ctx, number, path)
	m := sendfaxJobRe.FindStringSubmatch(string(out))
	if err != nil || m == nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, "FAX の道具に頼めませんでした: "+firstFaxLine(string(out), err))
		return
	}
	auth.Audit(user.Username, "fax.send", pageID+"/"+fileName+" -> "+number+" (job "+m[1]+")")
	resp := map[string]any{"success": true, "job": m[1], "number": number}
	// 送った FAX を記録にする。記録を作れなくても、FAX はもう頼んであるので、成功として返し、わけを添える。
	if recID, err := recordSentFax(user, pageID, fileName, path, number, m[1]); err != nil {
		resp["record_error"] = err.Error()
	} else {
		resp["record"] = recID
		go watchFaxJob(m[1], recID)
	}
	cms.WriteJSON(w, resp)
}

// recordSentFax は、送った FAX の記録を通信箱に作り、送った PDF を写して表示します（記録のページ番号を返す）。
func recordSentFax(user *auth.User, srcPage, fileName, path, number, job string) (string, error) {
	boxID, ok := MailBoxPageID()
	if !ok {
		return "", ErrNoMailBox
	}
	tmpl, err := cms.PageTemplateBody(MemoTemplate)
	if err != nil {
		return "", err
	}
	now := time.Now()
	counterpart, label := "", number
	if id, title, found := faxOwner(user, number); found {
		counterpart, label = page.FormatID(id), title
	}
	title := ChannelFax + " " + DirectionOut + " " + label + " " + now.Format("2006-01-02 15:04")
	d := cms.EditBody(memoBodyHTML(tmpl, ChannelFax, title, DirectionOut, "", counterpart, now))
	d.SetTag(HandledTag, HandledDone) // 送った控え（送れなかったら、見張りが未処理に戻す）
	d.SetTag(FaxNumberTag, number)
	d.SetTag(FaxSendJobTag, job)
	d.SetTag(FaxSendStateTag, faxStateSending)
	d.SetTag(ParentPageTag, srcPage)
	recID, err := CreateRecordPage(boxID, user.Username, now, d.HTML())
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return recID, err
	}
	name := fileName
	if m, ok := cms.ReadAttachmentMetas(srcPage)[fileName]; ok && strings.TrimSpace(m.Name) != "" {
		name = m.Name // 届いたときの名前で写す
	}
	attachID, _, err := cms.SaveAttachmentFrom(recID, user.Username, name, "fax-send:"+srcPage+"/"+fileName, content)
	if err != nil {
		return recID, err
	}
	if err := cms.RewriteBody(recID, user.Username, func(cur string) string {
		return strings.TrimRight(cur, "\n") + "\n" + `<section data-type="file-view" data-ref="` + recID + `-` + attachID + `"></section>`
	}); err != nil {
		return recID, err
	}
	auth.Audit(user.Username, "fax.record", recID+" (job "+job+")")
	return recID, nil
}

// watchFaxJob は、仕事 job が済むまで様子を見て、記録 recID の「FAX送信の様子」を書き替えます。
func watchFaxJob(job, recID string) {
	if _, busy := faxWatching.LoadOrStore(job, true); busy {
		return
	}
	defer faxWatching.Delete(job)
	end := time.Now().Add(faxWatchFor)
	for time.Now().Before(end) {
		time.Sleep(faxWatchInterval)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		out := readFaxStat(ctx)
		cancel()
		switch state, line := faxJobState(out, job); state {
		case "done":
			setFaxSendState(recID, faxStateDone, false)
			return
		case "failed":
			setFaxSendState(recID, faxStateFailed+"："+faxJobReason(line), true)
			return
		}
	}
}

// setFaxSendState は、記録の「FAX送信の様子」を書き替えます。failed なら対応を未処理に戻して知らせます。
func setFaxSendState(recID, state string, failed bool) {
	if err := cms.RewriteBody(recID, "FAX", func(cur string) string {
		d := cms.EditBody(cur)
		d.SetTag(FaxSendStateTag, state)
		if failed {
			d.SetTag(HandledTag, HandledPending)
		}
		return d.HTML()
	}); err != nil {
		return
	}
	if failed {
		NotifyArrival(Arrival{PageID: recID, Title: "⚠ FAX を送れませんでした " + cms.PageTitleByID(atoiPage(recID)), Channel: ChannelFax})
	}
}

// resumeFaxWatches は、サーバーが起きたときに、送っている途中の記録の見張りを始め直します。
func resumeFaxWatches() {
	ids, err := cms.PagesByTag(database.DB, FaxSendStateTag, faxStateSending)
	if err != nil {
		return
	}
	for _, id := range ids {
		job := strings.TrimSpace(cms.PageTagValue(database.DB, id, FaxSendJobTag))
		if job == "" {
			continue
		}
		go watchFaxJob(job, page.FormatID(id))
	}
}

// faxJobReason は、faxstat の1行から理由の文（Status の欄）を取り出します。済んだ仕事は TTS の欄が空なので、8つ目からが理由。
func faxJobReason(line string) string {
	f := strings.Fields(line)
	if len(f) <= 7 {
		return "理由は分かりません"
	}
	r := strings.Join(f[7:], " ")
	if _, err := strconv.Atoi(strings.ReplaceAll(f[7], ":", "")); err == nil && len(f) > 8 {
		r = strings.Join(f[8:], " ") // TTS（時刻）が入っていれば飛ばす
	}
	return r
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
	state, line := faxJobState(readFaxStat(ctx), job)
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
