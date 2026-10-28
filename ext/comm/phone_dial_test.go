package comm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
)

// TestPhoneDial は、業務サーバーのモデムでかける口（phone_dial.go・2026-10-28）を固定します。
//
//   - 道具が無ければ、かけ方の候補に出さない（info の available が偽）・かけようとしても断る
//   - 番号は数字だけにして道具へ渡す・読めない番号と料金の高い番号（管理者でない）は道具へ頼まない
//   - 道具の「話し中」（409）はそのまま返す・受話器を上げたら道具に離すよう頼む
func TestPhoneDial(t *testing.T) {
	setupIntakeTest(t)
	var calls []string
	var reply = map[string]any{"ok": true, "hold_sec": float64(30)}
	replyCode := http.StatusOK
	oldCall, oldAvail := callDialer, dialAvailable
	t.Cleanup(func() { callDialer, dialAvailable = oldCall, oldAvail })
	callDialer = func(_ context.Context, path string, body any) (int, map[string]any, error) {
		b, _ := json.Marshal(body)
		calls = append(calls, path+" "+string(b))
		return replyCode, reply, nil
	}
	available := false
	dialAvailable = func() bool { return available }

	u := &auth.User{Username: "alice"}
	post := func(h http.HandlerFunc, url string, body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := auth.WithUser(httptest.NewRequest("POST", url, bytes.NewReader(b)), u)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	info := func() bool {
		rr := httptest.NewRecorder()
		PhoneDialInfoAPIHandler(rr, auth.WithUser(httptest.NewRequest("GET", "/api/phone/dial/info", nil), u))
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return out["available"] == true
	}

	if info() {
		t.Error("⚠ 道具が無いのに、モデムでかけられると答えました")
	}
	if code, _ := post(PhoneDialAPIHandler, "/api/phone/dial", map[string]any{"number": "03-1234-5678"}); code != http.StatusServiceUnavailable {
		t.Errorf("道具が無いのにかけました: %d", code)
	}
	available = true
	if !info() {
		t.Error("道具があるのに、かけられないと答えました")
	}
	for _, c := range []struct {
		number string
		code   int
	}{{"03-ABCD", http.StatusBadRequest}, {"0570-123-456", http.StatusForbidden}, {"010-1-234-5678", http.StatusForbidden}} {
		if code, out := post(PhoneDialAPIHandler, "/api/phone/dial", map[string]any{"number": c.number}); code != c.code {
			t.Errorf("%s: %d %v（%d のはず）", c.number, code, out, c.code)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("⚠ 断るはずの番号を道具へ頼みました: %v", calls)
	}
	code, out := post(PhoneDialAPIHandler, "/api/phone/dial", map[string]any{"number": "03-1234-5678"})
	if code != http.StatusOK || out["number"] != "0312345678" || out["hold_sec"] != float64(30) {
		t.Errorf("かけられません: %d %v", code, out)
	}
	if len(calls) != 1 || calls[0] != `/dial {"number":"0312345678"}` {
		t.Errorf("道具へ頼んだもの = %v（数字だけの番号のはず）", calls)
	}
	replyCode, reply = http.StatusConflict, map[string]any{"ok": false, "error": "いま、ほかの電話をかけています"}
	if code, out := post(PhoneDialAPIHandler, "/api/phone/dial", map[string]any{"number": "0312345678"}); code != http.StatusConflict {
		t.Errorf("話し中を返していません: %d %v", code, out)
	}
	if code, _ := post(PhoneDialReleaseAPIHandler, "/api/phone/dial/release", map[string]any{}); code != http.StatusOK || calls[len(calls)-1] != "/release {}" {
		t.Errorf("離すよう頼んでいません: %d %v", code, calls)
	}
}
