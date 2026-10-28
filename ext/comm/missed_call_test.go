package comm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestMissedCallLogAndMake は出ていない着信（missed_call.go・2026-10-28）を固定します。
//
// > 2026-10-28 利用者:「電話が鳴るだけでページを作るということではなく、なった記録だけして、編集者がページにするか選択すればよいと思うのです」
//
//   - missed は**ページを作らない**——履歴に1行残すだけ（同じ番号・同じ鳴り始めの送り直しは1つ）
//   - 通信箱の未処理の一覧の上に並び、「記録にする」で初めて電話の記録のページができる（着信の結果・呼び出しの秒数）
//   - もう一度押しても、同じ記録を返す（2つ作らない）・「外す」と一覧から消える
func TestMissedCallLogAndMake(t *testing.T) {
	setupIntakeTest(t)
	if err := page.WriteSidecar("000300", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000300", `<h1>みなと商店</h1><dl data-type="tags"><dt>`+PhoneTag+`</dt><dd>03-1234-5678</dd></dl>`); err != nil {
		t.Fatal(err)
	}
	u := &auth.User{Username: "alice"}
	post := func(h http.HandlerFunc, url string, body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := auth.WithUser(httptest.NewRequest("POST", url, bytes.NewReader(b)), u)
		rr := httptest.NewRecorder()
		h(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	pagesUnder := func() int {
		n := 0
		database.DB.QueryRow("SELECT COUNT(*) FROM pages").Scan(&n)
		return n
	}
	before := pagesUnder()
	missed := map[string]any{"event": "missed", "number": "0312345678", "at": "2026-10-28T10:07:26+09:00", "ended": "2026-10-28T10:07:42+09:00"}
	for i := 0; i < 2; i++ { // 道具の送り直し
		if code, out := post(MachineCallAPIHandler, "/api/machine/call", missed); code != http.StatusOK {
			t.Fatalf("出ていない着信を残せません: %d %v", code, out)
		}
	}
	if code, _ := post(MachineCallAPIHandler, "/api/machine/call", map[string]any{"event": "missed", "number": "P",
		"at": time.Now().Format(time.RFC3339), "ended": time.Now().Format(time.RFC3339)}); code != http.StatusOK {
		t.Fatalf("非通知の出ていない着信を残せません: %d", code)
	}
	if n := pagesUnder(); n != before {
		t.Errorf("⚠ 鳴っただけでページを作った: %d → %d", before, n)
	}
	// 一覧（14 日より前の着信は出ないので、いまの日付の非通知と、試験の日付のものは日付しだい——数えるのは非通知の1件と同じ番号の行）
	list := RecentMissedCalls()
	var target MissedCall
	for _, m := range list {
		if m.Number == "0312345678" {
			target = m
		}
	}
	if target.ID == "" {
		// 試験の日付が14日より前（いま走らせた日しだい）のときは、履歴から直に取る。
		missedMu.Lock()
		for _, m := range readMissedLocked() {
			if m.Number == "0312345678" {
				target = m
			}
		}
		missedMu.Unlock()
	}
	if target.ID == "" || target.RingSec != 16 {
		t.Fatalf("履歴の行が違う: %+v（呼び出し 16秒のはず）", target)
	}
	all := 0
	missedMu.Lock()
	all = len(readMissedLocked())
	missedMu.Unlock()
	if all != 2 {
		t.Errorf("送り直しで行が増えた: %d 行（2のはず）", all)
	}
	box, _ := MailBoxPageID()
	inboxView := func() string { return unhandledViewHTML(u, atoiPage(box)) }
	// 未処理の表の行として出る（別の表にしない——利用者:「出てましたが、違う表なので気が付きませんでした」）。
	view := inboxView()
	if strings.Count(view, `class="missed-row"`) < 1 || !strings.Contains(view, "非通知") || !strings.Contains(view, "missed-make") ||
		!strings.Contains(view, `<table class="materials-table unhandled-table">`) || strings.Contains(view, "未処理はありません") {
		t.Errorf("未処理の表に出ていない:\n%s", view)
	}
	if strings.Count(view, "<table") != 1 {
		t.Errorf("表が1つでない（別の表に出している）:\n%s", view)
	}

	// 記録にする → 電話の記録のページ（着信の結果・呼び出し）
	code, out := post(MissedCallMakeAPIHandler, "/api/phone/missed/make", map[string]any{"id": target.ID})
	if code != http.StatusOK {
		t.Fatalf("記録にできません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"電話（出ていない） みなと商店 2026-10-28 10:07", "<dd>" + CallResultMissed + "</dd>", "<dd>16秒</dd>", "<dd>" + HandledPending + "</dd>", "<dd>000300</dd>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("記録に %q がありません:\n%s", want, body)
		}
	}
	if _, out2 := post(MissedCallMakeAPIHandler, "/api/phone/missed/make", map[string]any{"id": target.ID}); out2["page_id"] != id {
		t.Errorf("もう一度押して別の記録ができた: %v（%s のはず）", out2["page_id"], id)
	}
	for _, m := range RecentMissedCalls() {
		if m.ID == target.ID {
			t.Errorf("記録にした着信が一覧に残った")
		}
	}
	// 外す
	var other string
	for _, m := range RecentMissedCalls() {
		other = m.ID
	}
	if other == "" {
		t.Fatal("外す相手（非通知）が一覧にない")
	}
	if code, _ := post(MissedCallDismissAPIHandler, "/api/phone/missed/dismiss", map[string]any{"id": other}); code != http.StatusOK {
		t.Fatalf("外せません: %d", code)
	}
	if len(RecentMissedCalls()) != 0 || strings.Contains(inboxView(), "missed-row") {
		t.Errorf("外したのに一覧に残った")
	}
	if code, _ := post(MissedCallDismissAPIHandler, "/api/phone/missed/dismiss", map[string]any{"id": "nope"}); code != http.StatusNotFound {
		t.Errorf("無い着信を外した: %d", code)
	}
}
