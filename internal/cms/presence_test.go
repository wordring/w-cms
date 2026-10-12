package cms

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ほかの人のキャレットのあるブロック（page_events.go の PresenceAPIHandler・2026-10-12 昼）の番人です。
// 利用者:「他人が同じブロックにキャレットを置いている、あるいはすでに文字を打ち込んでいるような場合は、早期に警告して欲しいです」
// →「置いただけで背景色を黄色系の彩度の低い色にしては？説明が多すぎるのでシンプルに」「表ごと」。

// postPresence は /api/presence を user として呼びます。
func postPresence(t *testing.T, user, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/presence", strings.NewReader(body))
	req = auth.WithUser(req, &auth.User{Username: user})
	rr := httptest.NewRecorder()
	PresenceAPIHandler(rr, req)
	return rr.Code
}

// TestPresenceIsSharedWithOtherTabs は、あるタブがブロックにキャレットを置くと、同じページを開いているタブへ配られ、
// 動かせば配り直し、同じなら配らず、タブを閉じれば消えることを確かめます。
func TestPresenceIsSharedWithOtherTabs(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000061", `<h1>いる</h1><p data-id="aa01">一</p><p data-id="bb02">二</p>`, page.PageMeta{Owner: "alice", Mode: "332"}) // ほかの人も読める
	a := watchers.subscribe("000061", pageWatcher{user: "alice", tab: "t-a"})
	b := watchers.subscribe("000061", pageWatcher{user: "bob", tab: "t-b"})
	defer watchers.unsubscribe("000061", b)

	if code := postPresence(t, "alice", `{"id":"61","tab":"t-a","block":"aa01"}`); code != 200 {
		t.Fatalf("置いた: status=%d", code)
	}
	ev := nextEvent(t, b)
	if ev.Type != "presence" || len(ev.Presence) != 1 || ev.Presence[0] != (presenceItem{Tab: "t-a", User: "alice", Block: "aa01"}) {
		t.Errorf("置いた知らせ = %+v", ev)
	}
	nextEvent(t, a) // 自分のタブにも届く（画面が自分のタブを除く）

	// 同じブロックのまま——配らない。
	postPresence(t, "alice", `{"id":"000061","tab":"t-a","block":"aa01"}`)
	noEvent(t, b, "同じブロックのまま")

	// 動かした——配り直す。
	postPresence(t, "alice", `{"id":"000061","tab":"t-a","block":"bb02"}`)
	if ev := nextEvent(t, b); len(ev.Presence) != 1 || ev.Presence[0].Block != "bb02" {
		t.Errorf("動かした知らせ = %+v", ev)
	}
	nextEvent(t, a)

	// 別の人は、ほかの人のタブの印を変えられない。
	if code := postPresence(t, "bob", `{"id":"000061","tab":"t-a","block":"aa01"}`); code != 200 {
		t.Fatalf("別の人の呼び出し = %d（読める人なので 200——断られると名乗りの守りを試せない）", code)
	}
	noEvent(t, b, "別の人がほかの人のタブを名乗った")
	if got := watchers.presence("000061"); len(got) != 1 || got[0].Block != "bb02" {
		t.Errorf("別の人が名乗ったあと = %+v", got)
	}

	// つないだ直後の hello に入る中身（いまの印）。
	if got := watchers.presence("000061"); len(got) != 1 || got[0].User != "alice" {
		t.Errorf("いまの印 = %+v", got)
	}

	// タブを閉じた——印が消えて配り直す。
	if !watchers.unsubscribe("000061", a) {
		t.Error("ブロックに置いていたタブを外したのに true にならない")
	}
	publishPresence("000061")
	if ev := nextEvent(t, b); ev.Type != "presence" || len(ev.Presence) != 0 {
		t.Errorf("閉じたあとの知らせ = %+v", ev)
	}
}

// TestPresenceRejectsBadRequests は、口の守り（POST だけ・ブロックの形・読めないページ）を確かめます。
func TestPresenceRejectsBadRequests(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000062", `<h1>守り</h1>`, page.PageMeta{Owner: "alice", Mode: "300"}) // 持ち主だけ読める
	w := watchers.subscribe("000062", pageWatcher{user: "mallory", tab: "t-m"})
	defer watchers.unsubscribe("000062", w)

	req := httptest.NewRequest("GET", "/api/presence", nil)
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	PresenceAPIHandler(rr, req)
	if rr.Code != 405 {
		t.Errorf("GET = %d（405 のはず）", rr.Code)
	}
	if code := postPresence(t, "alice", `{"id":"000062","tab":"t-a","block":"<b>"}`); code != 400 {
		t.Errorf("ブロックの形が不正 = %d（400 のはず）", code)
	}
	if code := postPresence(t, "alice", `{"id":"000062","tab":"","block":"aa01"}`); code != 400 {
		t.Errorf("タブが空 = %d（400 のはず）", code)
	}
	if code := postPresence(t, "mallory", `{"id":"000062","tab":"t-m","block":"aa01"}`); code != 403 {
		t.Errorf("読めない人 = %d（403 のはず）", code)
	}
	if got := watchers.presence("000062"); len(got) != 0 {
		t.Errorf("断ったのに印が残っている: %+v", got)
	}
	// 空のブロック（どこにも置いていない）は通る。
	if code := postPresence(t, "alice", `{"id":"000062","tab":"t-a","block":""}`); code != 200 {
		t.Errorf("空のブロック = %d（200 のはず）", code)
	}
}
