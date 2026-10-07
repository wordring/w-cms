package cms

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ページの更新の知らせ（page_events.go・2026-10-07）の番人です。
// 利用者:「誰かが書き込むと、同じページを見ているほかの人に再読み込み通知が送られて、
// 編集されたブロックだけ再読み込みできますか？」

// nextEvent は購読の口から1件を待ちます（来なければ試験を落とす）。
func nextEvent(t *testing.T, ch chan pageEvent) pageEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("知らせが来ません")
		return pageEvent{}
	}
}

// noEvent は、その口に知らせが来ていないことを確かめます。
func noEvent(t *testing.T, ch chan pageEvent, why string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Errorf("%s: 知らせが来ています: %+v", why, ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestPageEventsSaveNotifiesOpenTabs は、エディタの保存（全文・ブロック）で、そのページを
// 開いているタブにだけ「誰が・いつ」が届くことを確かめます。
func TestPageEventsSaveNotifiesOpenTabs(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000051", `<h1>見る</h1><p data-id="aa01">前</p>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000052", `<h1>ほか</h1>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	mine := watchers.subscribe("000051")
	defer watchers.unsubscribe("000051", mine)
	other := watchers.subscribe("000052")
	defer watchers.unsubscribe("000052", other)

	// 揺れた表記の ID で保存しても、ゼロ詰めの購読に届く。
	resp := postSaveAs(t, "51", `<h1>見る</h1><p data-id="aa01">後</p>`, "bob")
	ev := nextEvent(t, mine)
	if ev.Type != "updated" || ev.By != "bob" || ev.UpdatedAt == "" || ev.UpdatedAt != resp["updated_at"] {
		t.Errorf("全文保存の知らせ = %+v（保存の返事の更新日時 %v）", ev, resp["updated_at"])
	}
	noEvent(t, other, "ほかのページを開いているタブ")

	// ブロック保存でも届く。
	payload, _ := json.Marshal(map[string]string{"page_id": "000051", "block_id": "aa01", "html": `<p data-id="aa01">もっと後</p>`})
	req := httptest.NewRequest("POST", "/api/save-block", strings.NewReader(string(payload)))
	req = auth.WithUser(req, &auth.User{Username: "carol", IsAdmin: true})
	rr := httptest.NewRecorder()
	SaveBlockAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("ブロック保存: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ev := nextEvent(t, mine); ev.Type != "updated" || ev.By != "carol" || ev.UpdatedAt == "" {
		t.Errorf("ブロック保存の知らせ = %+v", ev)
	}
}

// TestPageEventsMachineWritesAndRevertNotify は、機械の書き換え（RewriteBody・SetPageH1）と
// 版を戻す操作でも知らせが届くことを確かめます——人の保存だけでなく、一覧のボタンや解析が
// 書き換えたときも、開いている人の画面は古くなるため。
func TestPageEventsMachineWritesAndRevertNotify(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000053", `<h1>機械</h1><p>一</p>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	ch := watchers.subscribe("000053")
	defer watchers.unsubscribe("000053", ch)

	if err := RewriteBody("000053", "解析", func(cur string) string { return cur + "<p>二</p>" }); err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	ev := nextEvent(t, ch)
	meta, _ := page.ReadSidecar("000053")
	if ev.Type != "updated" || ev.By != "解析" || ev.UpdatedAt != meta.UpdatedAt {
		t.Errorf("機械の書き換えの知らせ = %+v（サイドカーの更新日時 %s）", ev, meta.UpdatedAt)
	}

	if err := SetPageH1("000053", "整理", "新しい題"); err != nil {
		t.Fatalf("SetPageH1: %v", err)
	}
	if ev := nextEvent(t, ch); ev.By != "整理" {
		t.Errorf("題の書き換えの知らせ = %+v", ev)
	}

	versions, err := ListVersions("000053")
	if err != nil || len(versions) == 0 {
		t.Fatalf("版がありません: %v %v", versions, err)
	}
	if err := RevertToVersion("000053", versions[len(versions)-1].ID, "dave"); err != nil {
		t.Fatalf("RevertToVersion: %v", err)
	}
	if ev := nextEvent(t, ch); ev.Type != "updated" || ev.By != "dave" || ev.UpdatedAt == "" {
		t.Errorf("版を戻したときの知らせ = %+v", ev)
	}
}

// TestPageEventsPublishDoesNotWaitForStuckTabs は、読まないタブがあっても書き込みの口が
// 止まらないことを確かめます（詰まった購読者は飛ばす）。
func TestPageEventsPublishDoesNotWaitForStuckTabs(t *testing.T) {
	stuck := watchers.subscribe("000054")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			notifyPageUpdated("000054", "bob", "t")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("読まないタブがあると知らせる側が止まっています")
		// 止まった知らせる側は錠を持ったまま——読んでほどいてから片付ける（でないと試験ごと止まる）。
		for {
			select {
			case <-stuck:
				continue
			case <-done:
			}
			break
		}
		watchers.unsubscribe("000054", stuck)
		return
	}
	defer watchers.unsubscribe("000054", stuck)
	if ev := nextEvent(t, stuck); ev.Type != "updated" {
		t.Errorf("溜まっていた知らせ = %+v", ev)
	}
}

// TestPageEventsStream は口そのもの（SSE）を確かめます——つないだ直後に hello と更新日時、
// 書き込みで updated。読めない人は断られ、購読も残らない。
func TestPageEventsStream(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000055", `<h1>流れ</h1>`, page.PageMeta{Owner: "alice", Mode: "300"}) // 持ち主だけ読める
	var user *auth.User
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		PageEventsAPIHandler(w, auth.WithUser(r, user))
	}))
	defer srv.Close()

	// 読めない人。
	user = &auth.User{Username: "mallory"}
	res, err := http.Get(srv.URL + "/api/page-events?id=55")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("読めない人の購読 = %d（403 のはず）", res.StatusCode)
	}

	// 読める人。
	user = &auth.User{Username: "alice"}
	res, err = http.Get(srv.URL + "/api/page-events?id=55")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if s := sc.Text(); strings.HasPrefix(s, "data: ") {
				lines <- strings.TrimPrefix(s, "data: ")
			}
		}
	}()
	read := func() pageEvent {
		t.Helper()
		select {
		case s := <-lines:
			var ev pageEvent
			if err := json.Unmarshal([]byte(s), &ev); err != nil {
				t.Fatalf("読めない知らせ: %s", s)
			}
			return ev
		case <-time.After(2 * time.Second):
			t.Fatal("知らせが流れてきません")
			return pageEvent{}
		}
	}
	meta, _ := page.ReadSidecar("000055")
	if ev := read(); ev.Type != "hello" || ev.UpdatedAt == "" || ev.UpdatedAt != meta.UpdatedAt {
		t.Errorf("つないだ直後 = %+v（サイドカーの更新日時 %s）", ev, meta.UpdatedAt)
	}
	if err := RewriteBody("000055", "alice", func(cur string) string { return cur + "<p>足した</p>" }); err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	if ev := read(); ev.Type != "updated" || ev.By != "alice" {
		t.Errorf("書き込みの知らせ = %+v", ev)
	}

	// 断った人の購読は残っていない（読める人の1本だけ）。
	watchers.mu.Lock()
	n := len(watchers.subs["000055"])
	watchers.mu.Unlock()
	if n != 1 {
		t.Errorf("購読の数 = %d（読める人の1本のはず）", n)
	}
}

// TestLoadViewMatchesPageRendering は、/api/load?view=1 がページを開いたときと同じ描き方
// （見出しのアンカー・参照リンク）で返し、素の /api/load は返さないことを確かめます。
// 閲覧の画面は、開いたときの姿と読み直したものを比べて変わったブロックだけを差し替えるので、
// 描き方が違うと変わっていないブロックまで差し替えてしまいます（参照リンクも消える）。
func TestLoadViewMatchesPageRendering(t *testing.T) {
	setupSaveTest(t)
	writeTestShell(t)
	newPage(t, "000056", `<p>指す先</p>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000057", `<h1>見る</h1><dl data-type="tags"><dt>受信元</dt><dd>000056</dd></dl><h2>材料</h2>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	load := func(q string) string {
		req := httptest.NewRequest("GET", "/api/load?id=000057"+q, nil)
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		LoadAPIHandler(rr, req)
		if rr.Code != 200 {
			t.Fatalf("/api/load%s: %d %s", q, rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}
	plain, view := load(""), load("&view=1")
	if strings.Contains(plain, `href="/000056"`) || strings.Contains(plain, `id="材料"`) {
		t.Errorf("素の /api/load にアンカーか参照リンクが入っています（本文として保存されてしまう）: %s", plain)
	}
	if !strings.Contains(view, `href="/000056"`) || !strings.Contains(view, `id="材料"`) {
		t.Errorf("view=1 にアンカーか参照リンクがありません: %s", view)
	}
	// ページを開いたときの本文（殻の中）と同じ描き方であること。
	shell := getPage(t, "/000057", &auth.User{Username: "alice"}).Body.String()
	if !strings.Contains(shell, view) {
		t.Errorf("view=1 がページの描き方と違います:\nview=%s\nshell=%s", view, shell)
	}
}

// TestPageEventsNotifyFoldsID は、揺れた表記の ID で知らせても、ゼロ詰めの購読に届くことを確かめます
// （いまの呼び手はどれも畳んでから渡すが、知らせる側でも畳んでおく——取りこぼしは黙って起きるので）。
func TestPageEventsNotifyFoldsID(t *testing.T) {
	ch := watchers.subscribe("000058")
	defer watchers.unsubscribe("000058", ch)
	notifyPageUpdated("58", "bob", "t")
	if ev := nextEvent(t, ch); ev.By != "bob" {
		t.Errorf("知らせ = %+v", ev)
	}
}
