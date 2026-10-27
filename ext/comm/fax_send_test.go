package comm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestFaxSendNumber は、打たれた FAX の番号を数字だけにし、使えない文字や長さを断ることを固定します
// （sendfax の引数に人の書いた文字をそのまま渡さないため）。
func TestFaxSendNumber(t *testing.T) {
	for in, want := range map[string]string{
		"06-1234-5678":     "0612345678",
		"０６－１２３４－５６７８":     "0612345678",
		"(06) 1234 5678":   "0612345678",
		"06-1234-5678;rm":  "",
		"12345":            "",
		"0612345678901234": "",
		"":                 "",
	} {
		if got := faxSendNumber(in); got != want {
			t.Errorf("faxSendNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFaxJobState は、faxstat の出力から仕事の様子を読むことを固定します（2026-10-25 に業務サーバーで見た形）。
func TestFaxJobState(t *testing.T) {
	out := `HylaFAX scheduler on wcms-server: Running
Modem faxmodem (): Running and idle

JID  Pri S  Owner Number       Pages Dials     TTS Status
1    126 F   toho 0612345678    0:1   1:12         Job aborted by request
2    127 D   toho 0612345678    1:1   1:12
3    127 R   w-cms 0612345678   0:1   0:12
`
	for job, want := range map[string]string{"1": "failed", "2": "done", "3": "queued", "9": "unknown"} {
		if got, _ := faxJobState(out, job); got != want {
			t.Errorf("faxJobState(%s) = %s, want %s", job, got, want)
		}
	}
	if _, line := faxJobState(out, "1"); line == "" {
		t.Error("理由の行が返らない")
	}
}

// TestFaxSendRecordsAndWatches は、FAX を送る口が、送った FAX を通信箱の記録にし、見張りが様子を書き替えることを固定します
// （2026-10-25 夜）。HylaFAX は差し替える（runSendfax・readFaxStat）。
//
//   - 送ると、通信箱に「FAX 送信 …」の記録ができる（向き送信・チャネル FAX・対応 済・FAX番号・FAX送信番号・親ページID・送っている）
//   - 送った PDF が記録に写り、本文に表示される（ファイル表示の印）
//   - 見張りが「送れた」に書き替える／送れなかったら「送れなかった：理由」・対応を未処理に戻し、知らせる
//   - 書けないページからは送れない・PDF でなければ送れない
func TestFaxSendRecordsAndWatches(t *testing.T) {
	setupIntakeTest(t)
	resetFaxJobs(t)
	if err := page.WriteSidecar("000400", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000400", `<h1>部材発注書</h1>`); err != nil {
		t.Fatal(err)
	}
	_, pdfName, err := cms.SaveAttachment("000400", "alice", "発注書.pdf", []byte("%PDF-1.4\n%%EOF\n"))
	if err != nil {
		t.Fatal(err)
	}
	var sentArgs []string
	nextJob := 7
	status := map[string]string{}
	oldSend, oldStat, oldInt := runSendfax, readFaxStat, faxWatchInterval
	t.Cleanup(func() { runSendfax, readFaxStat, faxWatchInterval = oldSend, oldStat, oldInt })
	runSendfax = func(_ context.Context, number, path string) ([]byte, error) {
		sentArgs = []string{number, path}
		nextJob++
		return []byte(fmt.Sprintf("request id is %d (group id %d) for host localhost (1 file)\n", nextJob, nextJob)), nil
	}
	var mu sync.Mutex
	readFaxStat = func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		out := "JID  Pri S  Owner Number       Pages Dials     TTS Status\n"
		for job, st := range status {
			out += job + "    127 " + st + "\n"
		}
		return out
	}
	faxWatchInterval = 10 * time.Millisecond
	got := make(chan Arrival, 4)
	RegisterArrivalListener(func(a Arrival) { got <- a })

	u := &auth.User{Username: "alice"}
	send := func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := auth.WithUser(httptest.NewRequest("POST", "/api/fax/send", bytes.NewReader(b)), u)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		FaxSendAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	// bodyOf は記録の本文を読みます。裏の見張りが書き替えている最中は Windows では開けないので、少し待って読み直す。
	bodyOf := func(id string) string {
		var err error
		for i := 0; i < 50; i++ {
			var b []byte
			if b, err = os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html")); err == nil {
				return string(b)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal(err)
		return ""
	}
	waitFor := func(id, want string) string {
		for i := 0; i < 1000; i++ { // 全部のパッケージを一緒に流すと遅くなるので、10秒まで待つ
			if b := bodyOf(id); strings.Contains(b, want) {
				return b
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("記録 %s に %q が書かれない: %s", id, want, bodyOf(id))
		return ""
	}

	// 送れる場合。
	code, out := send(map[string]any{"page_id": "000400", "file": pdfName, "number": "03-1234-5678"})
	if code != http.StatusOK || out["record"] == nil {
		t.Fatalf("送れない: %d %v", code, out)
	}
	if sentArgs[0] != "0312345678" || !strings.HasSuffix(filepath.ToSlash(sentArgs[1]), "/"+pdfName) {
		t.Errorf("sendfax に渡したもの: %v", sentArgs)
	}
	rec := out["record"].(string)
	b := bodyOf(rec)
	for _, want := range []string{"FAX 送信 0312345678", "<dd>" + DirectionOut + "</dd>", "<dd>" + ChannelFax + "</dd>", "<dd>" + HandledDone + "</dd>",
		"<dd>0312345678</dd>", "<dd>8</dd>", "<dd>000400</dd>", "<dd>" + faxStateSending + "</dd>", `data-type="file-view"`} {
		if !strings.Contains(b, want) {
			t.Errorf("記録に %q が無い: %s", want, b)
		}
	}
	mu.Lock()
	status["8"] = "D  alice 0312345678 1:1 1:12"
	mu.Unlock()
	waitFor(rec, "<dd>"+faxStateDone+"</dd>")

	// 送れなかった場合: 理由・未処理・知らせ。
	code, out = send(map[string]any{"page_id": "000400", "file": pdfName, "number": "0312345678"})
	if out["record"] == nil {
		t.Fatalf("2通目: %d %v", code, out)
	}
	rec2 := out["record"].(string)
	mu.Lock()
	status["9"] = "F  alice 0312345678 0:1 1:12 Busy signal detected"
	mu.Unlock()
	b = waitFor(rec2, faxStateFailed+"：Busy signal detected")
	if !strings.Contains(b, "<dd>"+HandledPending+"</dd>") {
		t.Errorf("送れなかったのに未処理に戻らない: %s", b)
	}
	select {
	case a := <-got:
		if a.PageID != rec2 || !strings.Contains(a.Title, "送れませんでした") {
			t.Errorf("知らせが違う: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Error("送れなかった知らせが届かない")
	}

	// 断る場合。
	if code, _ := send(map[string]any{"page_id": "000400", "file": "x.txt", "number": "0312345678"}); code != http.StatusBadRequest {
		t.Errorf("PDF でないのに断らない: %d", code)
	}
	if err := page.WriteSidecar("000401", page.PageMeta{Owner: "bob", Mode: "300", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := send(map[string]any{"page_id": "000401", "file": pdfName, "number": "0312345678"}); code == http.StatusOK {
		t.Error("書けないページから送れてしまう")
	}
}

// TestFaxCandidates は、FAX 番号の候補が、ページ自身の FAX番号・相手のページの FAX番号・登録された出どころから、
// 同じ番号を1つにまとめて出ることを固定します（2026-10-25 夜・fax_numbers.go）。
func TestFaxCandidates(t *testing.T) {
	setupIntakeTest(t)
	for id, body := range map[string]string{
		"000500": `<h1>連絡帳の相手</h1><dl data-type="tags"><dt>` + FaxNumberTag + `</dt><dd>06-1111-2222</dd></dl>`,
		"000501": `<h1>書類</h1><dl data-type="tags"><dt>` + FaxNumberTag + `</dt><dd>03-3333-4444</dd><dt>` + CounterpartTag + `</dt><dd>000500</dd></dl>`,
	} {
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(id, body); err != nil {
			t.Fatal(err)
		}
	}
	old := faxNumberSources.fns
	t.Cleanup(func() { faxNumberSources.fns = old })
	RegisterFaxNumberSource(func(_ *auth.User, pid int) []FaxCandidate {
		return []FaxCandidate{{Number: "0611112222", Label: "重なり"}, {Number: "0455556666", Label: "拡張"}}
	})
	got := faxCandidates(&auth.User{Username: "alice"}, 501)
	want := []string{"0333334444", "0611112222", "0455556666"}
	if len(got) != len(want) {
		t.Fatalf("候補 = %+v", got)
	}
	for i, w := range want {
		if got[i].Number != w {
			t.Errorf("候補[%d] = %+v, want %s", i, got[i], w)
		}
	}
}

// TestFaxSendPurposeAfterFaxed は、送る欄の用件から送った FAX で、用件の Prepare の PDF を送り、HylaFAX が「送れた」と
// 答えたときだけ AfterFaxed を送った人として呼ぶこと（送れなかったら呼ばない・AfterFaxed の無い用件は断る・後始末に
// 失敗したら記録を未処理に戻す）を固定します（2026-10-25 夜・利用者:「送れたときだけ送付日を書いてください」）。
func TestFaxSendPurposeAfterFaxed(t *testing.T) {
	setupIntakeTest(t)
	resetFaxJobs(t)
	if err := page.WriteSidecar("000410", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000410", `<h1>見積書</h1>`); err != nil {
		t.Fatal(err)
	}
	_, pdfName, err := cms.SaveAttachment("000410", "alice", "御見積書 20261025.pdf", []byte("%PDF-1.4\n%%EOF\n"))
	if err != nil {
		t.Fatal(err)
	}
	type call struct{ user, page, rec, file string }
	var mu sync.Mutex
	var calls []call
	afterErr := error(nil)
	sendPurposes["試験の紙"] = SendPurpose{
		Prepare: func(w http.ResponseWriter, r *http.Request, pageID string) ([]ComposeAttachment, bool) {
			return []ComposeAttachment{{PageID: pageID, File: pdfName, Name: "御見積書 " + pageID + ".pdf"}}, true
		},
		AfterFaxed: func(user *auth.User, pageID, recordID, file string) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, call{user.Username, pageID, recordID, file})
			return afterErr
		},
	}
	sendPurposes["メールだけの紙"] = SendPurpose{
		Prepare: func(w http.ResponseWriter, r *http.Request, pageID string) ([]ComposeAttachment, bool) {
			t.Error("FAX で送れない用件の Prepare が呼ばれた")
			return nil, false
		},
	}
	t.Cleanup(func() { delete(sendPurposes, "試験の紙"); delete(sendPurposes, "メールだけの紙") })

	nextJob := 20
	status := map[string]string{}
	oldSend, oldStat, oldInt, oldLookup := runSendfax, readFaxStat, faxWatchInterval, lookupFaxSender
	t.Cleanup(func() { runSendfax, readFaxStat, faxWatchInterval, lookupFaxSender = oldSend, oldStat, oldInt, oldLookup })
	var sentPath string
	runSendfax = func(_ context.Context, number, path string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		sentPath = path
		nextJob++
		return []byte(fmt.Sprintf("request id is %d (group id %d) for host localhost (1 file)\n", nextJob, nextJob)), nil
	}
	readFaxStat = func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		out := "JID  Pri S  Owner Number       Pages Dials     TTS Status\n"
		for job, st := range status {
			out += job + "    127 " + st + "\n"
		}
		return out
	}
	lookupFaxSender = func(name string) (*auth.User, error) { return &auth.User{Username: name}, nil }
	faxWatchInterval = 10 * time.Millisecond

	u := &auth.User{Username: "alice"}
	send := func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := auth.WithUser(httptest.NewRequest("POST", "/api/fax/send", bytes.NewReader(b)), u)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		FaxSendAPIHandler(rr, req)
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Logf("応答が JSON でない: %d %q", rr.Code, rr.Body.String())
		}
		return rr.Code, out
	}
	// bodyOf は記録の本文を読みます。裏の見張りが書き替えている最中は Windows では開けないので、少し待って読み直す。
	bodyOf := func(id string) string {
		var err error
		for i := 0; i < 50; i++ {
			var b []byte
			if b, err = os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html")); err == nil {
				return string(b)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal(err)
		return ""
	}
	waitFor := func(id, want string) string {
		for i := 0; i < 1000; i++ { // 全部のパッケージを一緒に流すと遅くなるので、10秒まで待つ
			if b := bodyOf(id); strings.Contains(b, want) {
				return b
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("記録 %s に %q が書かれない: %s", id, want, bodyOf(id))
		return ""
	}
	setStatus := func(job, st string) {
		mu.Lock()
		status[job] = st
		mu.Unlock()
	}
	nCalls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(calls)
	}

	// 送れた場合: 用件の PDF を送り、記録に用件とファイル、送れたら AfterFaxed を1回。
	code, out := send(map[string]any{"page_id": "000410", "purpose": "試験の紙", "number": "0312345678"})
	if code != http.StatusOK || out["record"] == nil {
		t.Fatalf("送れない: %d %v", code, out)
	}
	if !strings.HasSuffix(filepath.ToSlash(sentPath), "/"+pdfName) {
		t.Errorf("sendfax に渡した紙: %s", sentPath)
	}
	rec := out["record"].(string)
	b := bodyOf(rec)
	for _, want := range []string{"<dd>試験の紙</dd>", "<dd>" + pdfName + "</dd>", "<dd>000410</dd>"} {
		if !strings.Contains(b, want) {
			t.Errorf("記録に %q が無い: %s", want, b)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := nCalls(); n != 0 {
		t.Fatalf("送り終わる前に AfterFaxed が呼ばれた: %d", n)
	}
	setStatus("21", "D  alice 0312345678 1:1 1:12")
	waitFor(rec, "<dd>"+faxStateDone+"</dd>")
	mu.Lock()
	if len(calls) != 1 || calls[0] != (call{"alice", "000410", rec, pdfName}) {
		t.Errorf("AfterFaxed の呼ばれ方: %+v", calls)
	}
	mu.Unlock()

	// 送れなかった場合: AfterFaxed は呼ばない。
	code, out = send(map[string]any{"page_id": "000410", "purpose": "試験の紙", "number": "0312345678"})
	if out["record"] == nil {
		t.Fatalf("2通目: %d %v", code, out)
	}
	rec2 := out["record"].(string)
	setStatus("22", "F  alice 0312345678 0:1 1:12 No answer from remote")
	waitFor(rec2, faxStateFailed+"：No answer from remote")
	if n := nCalls(); n != 1 {
		t.Errorf("送れなかったのに AfterFaxed が呼ばれた: %d", n)
	}

	// 後始末に失敗した場合: 送れたことは残し、理由を書いて未処理に戻す。
	mu.Lock()
	afterErr = fmt.Errorf("送付日を書けませんでした")
	mu.Unlock()
	_, out = send(map[string]any{"page_id": "000410", "purpose": "試験の紙", "number": "0312345678"})
	rec3 := out["record"].(string)
	setStatus("23", "D  alice 0312345678 1:1 1:12")
	b = waitFor(rec3, faxStateDone+"：⚠ 送付日を書けませんでした")
	if !strings.Contains(b, "<dd>"+HandledPending+"</dd>") {
		t.Errorf("後始末に失敗したのに未処理に戻らない: %s", b)
	}

	// FaxPrepare のある用件: 送る1本（資料を綴じたもの）と、送ったと記録する紙が違う。
	_, boundName, err := cms.SaveAttachment("000410", "alice", "御見積書＋資料.pdf", []byte("%PDF-1.4\n%%EOF\n"))
	if err != nil {
		t.Fatal(err)
	}
	sendPurposes["綴じる紙"] = SendPurpose{
		FaxPrepare: func(w http.ResponseWriter, r *http.Request, pageID string) (ComposeAttachment, ComposeAttachment, bool) {
			return ComposeAttachment{PageID: pageID, File: pdfName}, ComposeAttachment{PageID: pageID, File: boundName, Name: "綴じた1本.pdf"}, true
		},
		AfterFaxed: sendPurposes["試験の紙"].AfterFaxed,
	}
	t.Cleanup(func() { delete(sendPurposes, "綴じる紙") })
	mu.Lock()
	afterErr = nil
	mu.Unlock()
	_, out = send(map[string]any{"page_id": "000410", "purpose": "綴じる紙", "number": "0312345678"})
	if out["record"] == nil {
		t.Fatalf("綴じる紙を送れない: %v", out)
	}
	rec4 := out["record"].(string)
	if !strings.HasSuffix(filepath.ToSlash(sentPath), "/"+boundName) {
		t.Errorf("綴じた1本を送っていない: %s", sentPath)
	}
	if b := bodyOf(rec4); !strings.Contains(b, "<dt>"+FaxSendFileTag+"</dt><dd>"+pdfName+"</dd>") {
		t.Errorf("記録の「%s」が綴じる前の紙でない: %s", FaxSendFileTag, b)
	}
	setStatus("24", "D  alice 0312345678 1:1 1:12")
	waitFor(rec4, "<dd>"+faxStateDone+"</dd>")
	mu.Lock()
	if last := calls[len(calls)-1]; last.rec != rec4 || last.file != pdfName {
		t.Errorf("AfterFaxed に渡した紙: %+v", last)
	}
	mu.Unlock()

	// 断る場合: FAX の後始末の無い用件・知らない用件。
	for _, p := range []string{"メールだけの紙", "無い用件"} {
		if code, _ := send(map[string]any{"page_id": "000410", "purpose": p, "number": "0312345678"}); code != http.StatusBadRequest {
			t.Errorf("用件 %q を断らない: %d", p, code)
		}
	}
}

// resetFaxJobs は、送った FAX の仕事の控えを空から始めます（控えは試験ごとの一時の置き場の data/fax に書く）。
func resetFaxJobs(t *testing.T) {
	t.Helper()
	reset := func() {
		faxJobs.Lock()
		faxJobs.loaded, faxJobs.m = false, nil
		faxJobs.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// TestFaxWatchUsesStoreNotTags は、送れたかの見張りと後始末が、記録のタグではなくサーバーの控えだけを見ることを固定します
// （2026-10-26）。タグを書き替えて「送っている」の記録を作っても、起き直したときの見張りは始まらず、用件の後始末は
// 呼ばれない。控えのある仕事は、タグを書き替えても、控えの元のページ・送った人で後始末を呼ぶ。様子を見る口は、記録を
// 読める人にだけ答える。HylaFAX の一覧から消えた仕事は、しばらくして「送れたか分からない」で見張りをやめる。
func TestFaxWatchUsesStoreNotTags(t *testing.T) {
	setupIntakeTest(t)
	resetFaxJobs(t)
	var mu sync.Mutex
	var calls []string
	sendPurposes["控えの紙"] = SendPurpose{AfterFaxed: func(user *auth.User, pageID, recordID, file string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, user.Username+" "+pageID+" "+file)
		return nil
	}}
	t.Cleanup(func() { delete(sendPurposes, "控えの紙") })
	status := map[string]string{}
	oldStat, oldInt, oldLookup, oldUnknown := readFaxStat, faxWatchInterval, lookupFaxSender, faxUnknownFor
	t.Cleanup(func() { readFaxStat, faxWatchInterval, lookupFaxSender, faxUnknownFor = oldStat, oldInt, oldLookup, oldUnknown })
	readFaxStat = func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		out := "JID  Pri S  Owner Number       Pages Dials     TTS Status\n"
		for job, st := range status {
			out += job + "    127 " + st + "\n"
		}
		return out
	}
	lookupFaxSender = func(name string) (*auth.User, error) { return &auth.User{Username: name}, nil }
	faxWatchInterval = 10 * time.Millisecond
	tagged := func(id, job, parent string) {
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "a", CreatedBy: "a", Mode: "333", ParentID: "000100"}); err != nil {
			t.Fatal(err)
		}
		body := `<h1>FAX 送信</h1><dl data-type="tags"><dt>` + FaxSendStateTag + `</dt><dd>` + faxStateSending + `</dd><dt>` + FaxSendJobTag +
			`</dt><dd>` + job + `</dd><dt>` + FaxSendPurposeTag + `</dt><dd>控えの紙</dd><dt>` + ParentPageTag + `</dt><dd>` + parent +
			`</dd><dt>` + FaxSendFileTag + `</dt><dd>x.pdf</dd></dl>`
		if err := os.MkdirAll(page.GetPageDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(page.GetPageDir(id), id+".html"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(id, body); err != nil {
			t.Fatal(err)
		}
	}
	waitCalls := func(n int) []string {
		for i := 0; i < 1000; i++ { // 全部のパッケージを一緒に流すと遅くなるので、10秒まで待つ
			mu.Lock()
			c := append([]string(nil), calls...)
			mu.Unlock()
			if len(c) >= n {
				return c
			}
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}

	// タグだけで作った「送っている」の記録——控えが無いので、起き直しても見張らない。
	tagged("000420", "31", "000999")
	mu.Lock()
	status["31"] = "D  a 0312345678 1:1 1:12"
	mu.Unlock()
	resumeFaxWatches()
	time.Sleep(100 * time.Millisecond)
	if c := waitCalls(0); len(c) != 0 {
		t.Fatalf("⚠ タグだけの記録で後始末が呼ばれた: %v", c)
	}

	// 控えのある仕事——タグに別の元のページを書いても、控えの元のページ・送った人で呼ぶ。
	tagged("000421", "32", "000999")
	if err := putFaxJob(faxJob{Job: "32", Record: "000421", User: "bob", Page: "000410", Purpose: "控えの紙", File: "paper.pdf", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	status["32"] = "D  bob 0312345678 1:1 1:12"
	mu.Unlock()
	resumeFaxWatches()
	if c := waitCalls(1); len(c) != 1 || c[0] != "bob 000410 paper.pdf" {
		t.Fatalf("⚠ 後始末の呼ばれ方: %v（控えの bob 000410 paper.pdf のはず）", c)
	}
	for i := 0; i < 100; i++ {
		if j, _ := sentFaxJobOf("32"); j.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if j, _ := sentFaxJobOf("32"); !j.Done {
		t.Errorf("送れた仕事の控えが終わりになっていません: %+v", j)
	}

	// 様子を見る口: 記録を読める人にだけ（000421 は mode 333 でだれでも読める・000422 は a だけ）。
	if err := page.WriteSidecar("000422", page.PageMeta{Owner: "a", Mode: "300", ParentID: "000100"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000422", `<h1>秘密の FAX</h1>`); err != nil {
		t.Fatal(err)
	}
	if err := putFaxJob(faxJob{Job: "33", Record: "000422", User: "a", Started: time.Now(), Done: true, Ended: time.Now()}); err != nil {
		t.Fatal(err)
	}
	statusOf := func(job string) int {
		req := auth.WithUser(httptest.NewRequest("GET", "/api/fax/status?job="+job, nil), &auth.User{Username: "bob"})
		rr := httptest.NewRecorder()
		FaxStatusAPIHandler(rr, req)
		return rr.Code
	}
	if c := statusOf("32"); c != http.StatusOK {
		t.Errorf("読める記録の仕事の様子が見えません: %d", c)
	}
	if c := statusOf("33"); c != http.StatusNotFound {
		t.Errorf("⚠ 読めない記録の仕事の様子が見えます: %d", c)
	}
	if c := statusOf("99"); c != http.StatusNotFound {
		t.Errorf("控えの無い仕事の様子に答えています: %d", c)
	}

	// 一覧から消えた仕事——しばらくして「送れたか分からない」で見張りをやめる。
	faxUnknownFor = 50 * time.Millisecond
	tagged("000423", "34", "000410")
	if err := putFaxJob(faxJob{Job: "34", Record: "000423", User: "a", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	go watchFaxJob("34")
	for i := 0; i < 1000; i++ { // 全部のパッケージを一緒に流すと遅くなるので、10秒まで待つ
		if j, _ := sentFaxJobOf("34"); j.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(page.GetPageDir("000423"), "000423.html"))
	if j, _ := sentFaxJobOf("34"); !j.Done || !strings.Contains(string(b), "送れたか分かりません") {
		t.Errorf("⚠ 一覧から消えた仕事を見張り続けています: %+v\n%s", j, b)
	}
}

// TestCostlyFaxNumber は、料金の高くなる番号（国際・事業者を選ぶ・有料）を見分けることを固定します（2026-10-26）。
func TestCostlyFaxNumber(t *testing.T) {
	for n, want := range map[string]bool{"0312345678": false, "0667537225": false, "0120123456": false,
		"0101234567890": true, "0033123456": true, "0990123456": true,
		// 2026-10-27 の見直しで足した: 1 で始まる番号（104 など）・ナビダイヤル・テレドーム・020。
		"1040000": true, "1100000000": true, "0570123456": true, "0180123456": true, "0201234567": true, "0901234567": false} {
		if got := costlyFaxNumber(n); got != want {
			t.Errorf("costlyFaxNumber(%q) = %v（%v のはず）", n, got, want)
		}
	}
}
