package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"w-cms/internal/auth"
)

// 新しいメールの読み込みを裏で続ける（fetch_job.go・2026-10-09）の番人です。
// 利用者:「新しいメールの読み込みは、ページを閉じても継続するようにしてください」。
// 本物のメールのサーバーへは繋がず、1回分の取り込み（importBatch）とサインインの見分け（signedIn）を差し替えます。

// fakeBatches は箱ごとに、1回ごとの取り込んだ数を順に返す importBatch です（呼ばれた箱の順も記録する）。
type fakeBatches struct {
	mu    sync.Mutex
	plan  map[string][]int
	calls []string
	gate  chan struct{} // nil でなければ、1回ごとにここから受け取るまで待つ（動いている最中を見るため）
	err   map[string]error
	panicOn string
	fixes int // 記録の直し（fixRecordTags）を呼んだ回数
}

func (f *fakeBatches) batch(ctx context.Context, username string, opt ListOptions) (ImportSummary, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opt.Folder)
	if opt.Folder == f.panicOn {
		panic("試験の落ち")
	}
	if err := f.err[opt.Folder]; err != nil {
		return ImportSummary{}, err
	}
	plan := f.plan[opt.Folder]
	if len(plan) == 0 {
		return ImportSummary{}, nil
	}
	n := plan[0]
	f.plan[opt.Folder] = plan[1:]
	return ImportSummary{Imported: n, Duplicate: 1, Titles: []string{opt.Folder + "の題"}}, nil
}

func withFakeFetch(t *testing.T, f *fakeBatches, signed bool) {
	t.Helper()
	oldB, oldS, oldF := importBatch, signedIn, fixRecordTags
	importBatch = f.batch
	signedIn = func(string) bool { return signed }
	fixRecordTags = func(string) (int, int, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fixes++
		return 0, 0, nil
	}
	t.Cleanup(func() { importBatch, signedIn, fixRecordTags = oldB, oldS, oldF })
}

func postFetch(t *testing.T, username string) (int, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/mail/fetch-new", nil).WithContext(ctx)
	req = auth.WithUser(req, &auth.User{Username: username})
	rr := httptest.NewRecorder()
	MailFetchAPIHandler(rr, req)
	cancel() // ⚠ 画面を閉じたのと同じ——要求の文脈が切れても、裏の仕事は続くはず
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func waitFetchDone(t *testing.T, username string) FetchStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := fetchStatusOf(username); ok && !st.Running {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("読み込みが終わりません")
	return FetchStatus{}
}

// TestFetchRunsToEndAfterRequestEnds は、要求が切れても、受信と送信の箱を新しいメールが尽きるまで（1回分に満たなく
// なるまで）50通ずつ読み続けることを固定します。
func TestFetchRunsToEndAfterRequestEnds(t *testing.T) {
	f := &fakeBatches{plan: map[string][]int{"受信": {50, 50, 7}, FolderSent: {3}}}
	withFakeFetch(t, f, true)
	code, out := postFetch(t, "fetch-a")
	if code != 200 || out["started"] != true {
		t.Fatalf("始められません: %d %v", code, out)
	}
	st := waitFetchDone(t, "fetch-a")
	if got := strings.Join(f.calls, ","); got != "受信,受信,受信,"+FolderSent {
		t.Errorf("読んだ順 = %s（受信を3回・送信を1回のはず）", got)
	}
	if st.Folders["受信"].Imported != 107 || st.Folders[FolderSent].Imported != 3 || st.Folders["受信"].Duplicate != 3 {
		t.Errorf("数が違います: 受信 %+v 送信 %+v", *st.Folders["受信"], *st.Folders[FolderSent])
	}
	// 取り込み済みの記録の直し（古い名前・親ページID）は、読み込みの前と後に1回ずつ（2026-10-09・comm/parent_link.go）。
	if f.fixes != 2 {
		t.Errorf("記録の直しを %d 回呼びました（前と後の2回のはず）", f.fixes)
	}
	if st.FinishedAt == "" || st.Folder != "" || len(st.Errors) != 0 || len(st.Titles) == 0 {
		t.Errorf("終わった様子が違います: %+v", st)
	}
}

// TestFetchDoesNotStartTwice は、動いているあいだにもう一度押しても二つ目を始めず、動いている回の様子を返すことを固定します。
func TestFetchDoesNotStartTwice(t *testing.T) {
	f := &fakeBatches{plan: map[string][]int{"受信": {1}, FolderSent: {0}}, gate: make(chan struct{})}
	withFakeFetch(t, f, true)
	_, first := postFetch(t, "fetch-b")
	_, second := postFetch(t, "fetch-b")
	run := func(m map[string]any) any { return m["status"].(map[string]any)["run"] }
	if second["started"] != false || run(second) != run(first) || second["status"].(map[string]any)["running"] != true {
		t.Errorf("動いているのに二つ目を始めています: %v / %v", first, second)
	}
	close(f.gate)
	waitFetchDone(t, "fetch-b")
	_, third := postFetch(t, "fetch-b")
	if third["started"] != true || run(third) == run(first) {
		t.Errorf("終わったあとに押しても始めません: %v", third)
	}
	waitFetchDone(t, "fetch-b")
}

// TestFetchStopsWhenNotSignedIn は、サインインしていなければ始めず（409）、読む途中でサインインが無いと分かったら残りの
// 箱も読まずに理由を残すことを固定します。
func TestFetchStopsWhenNotSignedIn(t *testing.T) {
	withFakeFetch(t, &fakeBatches{plan: map[string][]int{}}, false)
	if code, _ := postFetch(t, "fetch-c"); code != 409 {
		t.Errorf("サインインしていないのに %d（409 のはず）", code)
	}
	if _, ok := fetchStatusOf("fetch-c"); ok {
		t.Errorf("サインインしていないのに仕事を始めています")
	}
	f := &fakeBatches{plan: map[string][]int{}, err: map[string]error{"受信": errNotSignedIn}}
	withFakeFetch(t, f, true)
	postFetch(t, "fetch-c")
	st := waitFetchDone(t, "fetch-c")
	if strings.Join(f.calls, ",") != "受信" || len(st.Errors) != 1 || !strings.Contains(st.Errors[0], "サインインしていません") {
		t.Errorf("サインインが無いのに送信の箱まで読んだか、理由がありません: %v %+v", f.calls, st)
	}
}

// TestFetchGoesOnAfterOneFolderFails は、1つの箱が読めなくても次の箱を読み、理由を残すことを固定します。
func TestFetchGoesOnAfterOneFolderFails(t *testing.T) {
	f := &fakeBatches{plan: map[string][]int{FolderSent: {2}}, err: map[string]error{"受信": errors.New("箱を開けません")}}
	withFakeFetch(t, f, true)
	postFetch(t, "fetch-d")
	st := waitFetchDone(t, "fetch-d")
	if st.Folders[FolderSent].Imported != 2 || len(st.Errors) != 1 || !strings.HasPrefix(st.Errors[0], "受信: ") {
		t.Errorf("受信が読めないと送信を読まないか、理由がありません: %v %+v", f.calls, st)
	}
}

// TestFetchPanicDoesNotStickRunning は、仕事が途中で落ちても「動いている」のまま残らない（次に押せる）ことを固定します。
func TestFetchPanicDoesNotStickRunning(t *testing.T) {
	withFakeFetch(t, &fakeBatches{plan: map[string][]int{}, panicOn: "受信"}, true)
	postFetch(t, "fetch-e")
	st := waitFetchDone(t, "fetch-e")
	if len(st.Errors) != 1 || !strings.Contains(st.Errors[0], "途中で止まりました") {
		t.Errorf("落ちた理由がありません: %+v", st)
	}
}

// TestFetchStatusBeforeAnyRun は、一度も始めていなければ様子が null であることを固定します。
func TestFetchStatusBeforeAnyRun(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/mail/fetch-new/status", nil)
	req = auth.WithUser(req, &auth.User{Username: "fetch-never"})
	rr := httptest.NewRecorder()
	MailFetchStatusAPIHandler(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"status":null`) {
		t.Errorf("一度も始めていないのに: %d %s", rr.Code, rr.Body.String())
	}
}
