package comm

// FAX を送る口（2026-10-25 夜・deploy/fax/README.md）。
//
// 利用者:「とりあえず実装してみて、間に合わなかったらまた明日の夜やりましょう」——複合機を FAX の線から外したので、
// FAX を送る手段を w-cms に持たせる。送るのは、同じ機械の HylaFAX（sendfax）です。
//
//	POST /api/fax/send   {page_id, file, number} … ページに添付した PDF を、その番号へ送る仕事を HylaFAX に頼む。
//	                                              送った FAX を通信箱の記録にして {job, record} を返す。
//	                     {page_id, purpose, number} … 送る欄の用件（部材発注書など・SendPurpose）の紙を送る。紙は用件の
//	                                              Prepare が返す PDF（メールで添えるものと同じ）。送れたら用件の AfterFaxed。
//	GET  /api/fax/status?job=N                  … その仕事の様子（HylaFAX の faxstat の1行）。{state, line} を返す。
//
// 画面は、PDF のファイル表示の「⋯」の「📠 FAX で送る」（ext/comm/assets/comm.js・window.wcms.onFileMenu）。
//
// 送った FAX の記録（2026-10-25 夜）: 通信箱に、向き送信・チャネル FAX・対応 済の記録を作り、送った PDF を写して表示する。
// 親ページID は送った元のページ。送れたかはサーバーが見張り（watchFaxJob）、記録の「FAX送信の様子」を送れた・送れなかったに
// 書き替える。送れなかったら対応を未処理に戻し、通知（スマホと画面）で知らせる。サーバーを起こし直しても、送っている途中の
// 仕事の見張りは続きから（init の裏の仕事）。見張りが見るのは、サーバーの仕事の控え（fax_jobs.go）だけ。
//
// 用件の紙を送る（2026-10-25 夜）: 利用者:「部材発注書・部材見積依頼書・見積書の「送る」から FAX で送れるようにしてください。
// 送れたときだけ送付日を書いてください。」——記録に用件と送ったファイル（FAX送信の用件・FAX送信のファイル）を書き、見張りが
// 「送れた」を見たときだけ、用件の AfterFaxed（送付日・発注済みの印など）を、送った人として呼ぶ。送れなかったら呼ばない。
// サーバーを起こし直しても、仕事の控えから続きを呼べる。⚠ 2026-10-26 から、用件・元のページ・紙・送った人は控えから取る
// （それまでは記録のタグから読んでいて、タグを書き替えると、書けないページへ記録を作った人として書けた）。
import (
	"context"
	"html"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// FaxSendJobTag は、w-cms から送った FAX の、HylaFAX の仕事の番号です（送った FAX の記録に書く）。
const FaxSendJobTag = "FAX送信番号"

// FaxSendStateTag は、送った FAX の様子です（送っている・送れた・送れなかった：理由）。
const FaxSendStateTag = "FAX送信の様子"

// FaxSendPurposeTag・FaxSendFileTag は、送る欄の用件から送った FAX の、用件の名前と、送ったファイル（元のページの添付の
// 保存名）です。人が読むための写し（送れたときの後始末は、サーバーの仕事の控えを見る——fax_jobs.go）。元のページは 親ページID。
const (
	FaxSendPurposeTag = "FAX送信の用件"
	FaxSendFileTag    = "FAX送信のファイル"
)

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

// costlyFaxNumber は、料金の高くなる番号かです（2026-10-26）。010（国際）・00（事業者を選ぶ番号）・0990（有料の情報）で
// 始まる番号は、管理者だけが送れる——ページに書ける人ならだれでも、どこへでも送れた（モデムの設定は国際の番号も掛ける）。
func costlyFaxNumber(n string) bool {
	return strings.HasPrefix(n, "010") || strings.HasPrefix(n, "00") || strings.HasPrefix(n, "0990")
}

var sendfaxJobRe = regexp.MustCompile(`request id is (\d+)`)

type faxSendRequest struct {
	PageID  string `json:"page_id"`
	File    string `json:"file"`
	Purpose string `json:"purpose"` // 送る欄の用件（file の代わり）
	Number  string `json:"number"`
}

// faxSource は送る紙です（元のページ・送る添付の保存名・ディスクの場所・記録に写すときの名前・用件・用件の後始末に渡す紙）。
// Paper は、送る1本（File）と別の紙を「送った」と記録するとき（資料を綴じた FAX 用を送り、部材発注書の PDF を記録する）。
type faxSource struct {
	PageID, File, Path, Name, Purpose, Paper string
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
	// 送った FAX の記録は通信箱の下に作る——書けないなら送らない（2026-10-26。送ってから記録を作れないと、送った事実が残らない）。
	if _, err := WritableMailBox(user); err != nil {
		cms.JSONFail(w, http.StatusConflict, "FAX の記録を作れないので、送っていません: "+err.Error())
		return
	}
	// 番号は紙を用意する前に確かめる（用件の Prepare は PDF を作ることがある）。
	number := faxSendNumber(req.Number)
	if number == "" {
		cms.JSONFail(w, http.StatusBadRequest, "FAX の番号が読めません（数字6〜15桁。ハイフンは入れてもよい）")
		return
	}
	if costlyFaxNumber(number) && !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "国際・事業者を選ぶ番号・有料の番号（010・00・0990 で始まる番号）へ送れるのは管理者だけです")
		return
	}
	var src faxSource
	if strings.TrimSpace(req.Purpose) != "" {
		if src, ok = purposeFaxSource(w, r, pageID, strings.TrimSpace(req.Purpose)); !ok {
			return
		}
	} else {
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
		src = faxSource{PageID: pageID, File: fileName, Path: path}
	}
	fileName, path := src.File, src.Path
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
	if recID, err := recordSentFax(user, src, number, m[1]); err != nil {
		resp["record_error"] = err.Error()
	} else {
		resp["record"] = recID
		// 見張りと後始末は、サーバーの控えだけを見る（fax_jobs.go——記録のタグは書き替えられる）。
		paper := src.Paper
		if paper == "" {
			paper = src.File
		}
		if err := putFaxJob(faxJob{Job: m[1], Record: recID, User: user.Username, Page: src.PageID,
			Purpose: src.Purpose, File: paper, Number: number, Started: time.Now()}); err != nil {
			resp["record_error"] = "仕事の控えを書けませんでした（送れたかは記録に書かれません）: " + err.Error()
		} else {
			go watchFaxJob(m[1])
		}
	}
	cms.WriteJSON(w, resp)
}

// purposeFaxSource は、送る欄の用件 purpose の紙を用意します（用件の Prepare——メールで添えるものと同じ PDF）。
// ⚠ 断るときは応答を書いて false（Prepare の作法と同じ）。
func purposeFaxSource(w http.ResponseWriter, r *http.Request, pageID, purpose string) (faxSource, bool) {
	p, ok := SendPurposeOf(purpose)
	if !ok || p.AfterFaxed == nil || (p.Prepare == nil && p.FaxPrepare == nil) {
		cms.JSONFail(w, http.StatusBadRequest, "「"+purpose+"」は FAX で送れません")
		return faxSource{}, false
	}
	if p.FaxPrepare != nil {
		paper, send, ok := p.FaxPrepare(w, r, pageID)
		if !ok {
			return faxSource{}, false
		}
		path, found := page.AttachmentPath(pageID, send.File)
		if send.PageID != pageID || !strings.EqualFold(filepath.Ext(send.File), ".pdf") || !found || paper.PageID != pageID {
			cms.JSONFail(w, http.StatusInternalServerError, "FAX で送る紙（PDF）を用意できませんでした")
			return faxSource{}, false
		}
		return faxSource{PageID: pageID, File: send.File, Path: path, Name: send.Name, Purpose: purpose, Paper: paper.File}, true
	}
	atts, ok := p.Prepare(w, r, pageID)
	if !ok {
		return faxSource{}, false
	}
	for _, a := range atts {
		if a.PageID != pageID || !strings.EqualFold(filepath.Ext(a.File), ".pdf") {
			continue
		}
		path, found := page.AttachmentPath(pageID, a.File)
		if !found {
			continue
		}
		return faxSource{PageID: pageID, File: a.File, Path: path, Name: a.Name, Purpose: purpose}, true
	}
	cms.JSONFail(w, http.StatusInternalServerError, "FAX で送る紙（PDF）を用意できませんでした")
	return faxSource{}, false
}

// recordSentFax は、送った FAX の記録を通信箱に作り、送った PDF を写して表示します（記録のページ番号を返す）。
func recordSentFax(user *auth.User, src faxSource, number, job string) (string, error) {
	srcPage, fileName, path := src.PageID, src.File, src.Path
	boxID, err := WritableMailBox(user)
	if err != nil {
		return "", err
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
	if src.Purpose != "" {
		paper := src.Paper
		if paper == "" {
			paper = fileName
		}
		d.SetTag(FaxSendPurposeTag, src.Purpose)
		d.SetTag(FaxSendFileTag, paper)
	}
	recID, err := CreateRecordPage(boxID, user.Username, now, d.HTML())
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return recID, err
	}
	name := fileName
	if strings.TrimSpace(src.Name) != "" {
		name = src.Name // 用件の紙は、メールで添えるときと同じ名前（部材発注書 N.pdf など）
	} else if m, ok := cms.ReadAttachmentMetas(srcPage)[fileName]; ok && strings.TrimSpace(m.Name) != "" {
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

// faxUnknownFor は、HylaFAX の一覧に仕事が見つからないまま待つ長さです。それを過ぎたら「送れたか分からない」として
// 見張りをやめる（済んだ仕事が一覧から消されたあとに起き直したとき、いつまでも見張り続けないため・2026-10-26）。試験で縮める。
var faxUnknownFor = 10 * time.Minute

// watchFaxJob は、仕事 job（サーバーの控え）が済むまで様子を見て、記録の「FAX送信の様子」を書き替えます。
func watchFaxJob(job string) {
	if _, busy := faxWatching.LoadOrStore(job, true); busy {
		return
	}
	defer faxWatching.Delete(job)
	j, ok := sentFaxJobOf(job)
	if !ok || j.Done || j.Record == "" {
		return
	}
	end := time.Now().Add(faxWatchFor)
	var unknownSince time.Time
	// 記録へ書けなかったら（DB が混んでいた など）、見張りを終えずに次の回でもう一度書く（2026-10-26。それまでは書けなくても
	// 終えていて、記録が「送っている」のまま残った）。用件の後始末は1回だけ（afterDone）。
	afterDone, note := false, ""
	finish := func(state string, failed bool) bool {
		if err := setFaxSendState(j.Record, state, failed); err != nil {
			log.Printf("FAX の仕事 %s の様子を記録 %s に書けません（次の回にもう一度）: %v", job, j.Record, err)
			return false
		}
		finishFaxJob(job)
		return true
	}
	for time.Now().Before(end) {
		time.Sleep(faxWatchInterval)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		out := readFaxStat(ctx)
		cancel()
		state, line := faxJobState(out, job)
		if state != "unknown" {
			unknownSince = time.Time{}
		}
		switch state {
		case "done":
			// 送れたときだけ、用件の後始末（送付日など）。済ませてから「送れた」と書く——後始末の途中でサーバーが止まっても、
			// 起き直したときに続きから呼べるように（二度呼ぶことはあっても、呼び落とさない）。
			if !afterDone {
				note, afterDone = afterFaxedNote(j), true
			}
			if finish(faxStateDone+note, false) {
				return
			}
		case "failed":
			if finish(faxStateFailed+"："+faxJobReason(line), true) {
				return
			}
		case "unknown":
			if unknownSince.IsZero() {
				unknownSince = time.Now()
			} else if time.Since(unknownSince) > faxUnknownFor {
				if finish(faxStateFailed+"：送れたか分かりません（FAX の道具の一覧に仕事 "+job+" がありません）", true) {
					return
				}
			}
		}
	}
	// 見張る長さを過ぎた——送れたか分からないまま、人に知らせて終える。
	if !finish(faxStateFailed+"：送れたか分かりません（"+faxWatchFor.String()+" 待っても済みませんでした）", true) {
		finishFaxJob(job) // 書けなくても、見張りは終える（起き直すたびに12時間見張り直さない）
	}
}

// afterFaxedNote は、送る欄の用件から送った FAX（控え j）なら、用件の AfterFaxed を送った人として呼びます。
// うまくいけば空、失敗したら「：⚠ 理由」を返し、対応を未処理に戻して知らせる（送れたことは取り消さない）。
// ⚠ 用件・元のページ・送った紙・送った人は、控えだけから取る（記録のタグは、記録に書ける人ならだれでも書き替えられる）。
func afterFaxedNote(j faxJob) string {
	if j.Purpose == "" {
		return ""
	}
	fail := func(why string) string {
		markFaxPending(j.Record, "⚠ FAX は送れましたが、"+why)
		return "：⚠ " + why
	}
	p, ok := SendPurposeOf(j.Purpose)
	if !ok || p.AfterFaxed == nil {
		return fail("用件「" + j.Purpose + "」の後始末がありません")
	}
	src, _ := page.NormalizeID(j.Page)
	user, err := lookupFaxSender(j.User)
	if src == "" || err != nil || user == nil {
		return fail("送った元のページか、送った人が分かりません")
	}
	if err := p.AfterFaxed(user, src, j.Record, j.File); err != nil {
		return fail(err.Error())
	}
	return ""
}

// lookupFaxSender は利用者を名前で引きます（利用者の DB の無い試験で差し替える）。
var lookupFaxSender = auth.LookupUser

// markFaxPending は、記録の対応を未処理に戻し、title で知らせます。
func markFaxPending(recID, title string) {
	_ = cms.RewriteBody(recID, "FAX", func(cur string) string {
		d := cms.EditBody(cur)
		d.SetTag(HandledTag, HandledPending)
		return d.HTML()
	})
	NotifyArrival(Arrival{PageID: recID, Title: title + " " + cms.PageTitleByID(atoiPage(recID)), Channel: ChannelFax})
}

// setFaxSendState は、記録の「FAX送信の様子」を書き替えます。failed なら対応を未処理に戻して知らせます。
func setFaxSendState(recID, state string, failed bool) error {
	if err := cms.RewriteBody(recID, "FAX", func(cur string) string {
		d := cms.EditBody(cur)
		d.SetTag(FaxSendStateTag, state)
		if failed {
			d.SetTag(HandledTag, HandledPending)
		}
		return d.HTML()
	}); err != nil {
		return err
	}
	if failed {
		NotifyArrival(Arrival{PageID: recID, Title: "⚠ FAX を送れませんでした " + cms.PageTitleByID(atoiPage(recID)), Channel: ChannelFax})
	}
	return nil
}

// resumeFaxWatches は、サーバーが起きたときに、まだ見張っている仕事（サーバーの控え）の見張りを始め直します。
// ⚠ 記録のタグからは探さない（タグは書き替えられる——fax_jobs.go の冒頭）。
func resumeFaxWatches() {
	for _, j := range activeFaxJobs() {
		go watchFaxJob(j.Job)
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
//
// 見せるのは、その仕事の記録を読める人にだけ（2026-10-26。それまではログインしている人ならだれでも、仕事の番号を順に
// 引いて、ほかの人の送り先の番号と様子を読めた）。控えの無い仕事は「見つからない」。
func FaxStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONGet(w, r)
	if !ok {
		return
	}
	job := r.URL.Query().Get("job")
	if job == "" || strings.Trim(job, "0123456789") != "" || len(job) > 9 {
		cms.JSONFail(w, http.StatusBadRequest, "job が読めません")
		return
	}
	if j, found := sentFaxJobOf(job); !found || j.Record == "" || !page.CanView(user, atoiPage(j.Record)) {
		cms.JSONFail(w, http.StatusNotFound, "その仕事は見つかりません")
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

// FaxSendButtonHTML は、送る欄の用件 purpose の紙をページ pageID から FAX で送るボタンです（拡張の送る欄に置く）。
// 押すと、画面（comm.js）が番号を尋ね、`/api/fax/send` に purpose を付けて頼む。送れたかは記録と画面の通知で分かる。
func FaxSendButtonHTML(purpose, pageID string) string {
	return `<button type="button" class="chip-btn" data-fax-purpose="` + html.EscapeString(purpose) +
		`" data-fax-page="` + html.EscapeString(pageID) +
		`" title="この紙を w-cms の FAX で送ります。送れたと分かったときだけ、送付日などを書きます">📠 FAX で送る</button>`
}
