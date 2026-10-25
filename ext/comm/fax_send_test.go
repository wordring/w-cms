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
	bodyOf := func(id string) string {
		b, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	waitFor := func(id, want string) string {
		for i := 0; i < 200; i++ {
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
	_, out = send(map[string]any{"page_id": "000400", "file": pdfName, "number": "0312345678"})
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
