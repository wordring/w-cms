package comm

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

// TestMachineCallAnsweredAndHangup は電話の着信の口を固定します（2026-10-21 夜・【考察】通信箱の改善 §7.3）。
//
// > 2026-10-21 利用者:「出た着信だけ記録にします。」
//
//   - answered: チャネル電話・向き受信・対応：未処理の記録が通信箱にでき、番号を数字だけで比べて連絡帳の相手を引く
//   - 番号の代わりの印（P＝非通知）は言葉にして題に入れる
//   - hangup: その記録に「通話終了」を書き足す。電話の記録でなければ断る
//   - 届いた知らせ（NotifyArrival）を出す
func TestMachineCallAnsweredAndHangup(t *testing.T) {
	setupIntakeTest(t)
	// 連絡帳の相手（電話番号はハイフンつきで書いてある）。
	if err := page.WriteSidecar("000300", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000300", `<h1>みなと商店</h1><dl data-type="tags"><dt>`+PhoneTag+`</dt><dd>03-1234-5678</dd></dl>`); err != nil {
		t.Fatal(err)
	}
	got := make(chan Arrival, 4)
	RegisterArrivalListener(func(a Arrival) { got <- a })

	u := &auth.User{Username: "alice"}
	post := func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req := auth.WithUser(httptest.NewRequest("POST", "/api/machine/call", bytes.NewReader(b)), u)
		rr := httptest.NewRecorder()
		MachineCallAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	code, out := post(map[string]any{"event": "answered", "number": "0312345678", "at": "2026-10-21T10:15:00+09:00"})
	if code != http.StatusOK {
		t.Fatalf("記録を作れません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"電話 みなと商店 2026-10-21 10:15", "<dd>電話</dd>", "<dd>受信</dd>", "<dd>" + HandledPending + "</dd>", "<dd>0312345678</dd>", "<dd>000300</dd>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("⚠ 電話の記録に %q がありません:\n%s", want, body)
		}
	}
	select {
	case a := <-got:
		if a.PageID != id || a.Channel != ChannelPhone {
			t.Errorf("届いた知らせが違います: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("⚠ 届いた知らせが出ていません")
	}

	if code, out := post(map[string]any{"event": "hangup", "page_id": id, "at": "2026-10-21T10:20:00+09:00"}); code != http.StatusOK {
		t.Fatalf("通話終了を書けません: %d %v", code, out)
	}
	if v := cms.PageTagValue(database.DB, atoiPage(id), CallEndTag); !strings.HasPrefix(v, "2026-10-21T10:20") {
		t.Errorf("⚠ 通話終了が %q です", v)
	}
	if code, _ := post(map[string]any{"event": "hangup", "page_id": "000300"}); code != http.StatusBadRequest {
		t.Errorf("⚠ 電話の記録でないページに通話終了を書きました（%d）", code)
	}

	// 非通知は言葉にする。
	code, out = post(map[string]any{"event": "answered", "number": "P", "at": "2026-10-21T11:00:00+09:00"})
	if code != http.StatusOK || out["title"] != "電話 非通知 2026-10-21 11:00" {
		t.Errorf("⚠ 非通知の題が違います: %d %v", code, out)
	}
}

// TestMachineAuthGate は機械専用の入口の関門を固定します（鍵・アドレス・書き手）。
func TestMachineAuthGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := auth.MachineAuth(ok)
	call := func(remote, token string) int {
		req := httptest.NewRequest("POST", "/api/machine/call", nil)
		req.RemoteAddr = remote
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	t.Setenv("WCMS_MACHINE_TOKEN", "")
	if c := call("127.0.0.1:5000", "x"); c != http.StatusServiceUnavailable {
		t.Errorf("⚠ 鍵が決まっていないのに閉じていません（%d）", c)
	}
	t.Setenv("WCMS_MACHINE_TOKEN", "0123456789abcdef-test")
	t.Setenv("WCMS_MACHINE_ALLOW", "")
	if c := call("192.168.1.20:5000", "0123456789abcdef-test"); c != http.StatusForbidden {
		t.Errorf("⚠ 許していない機械から通しました（%d）", c)
	}
	if c := call("127.0.0.1:5000", "wrong-token-xxxxxxx"); c != http.StatusUnauthorized {
		t.Errorf("⚠ 鍵が違うのに通しました（%d）", c)
	}
	t.Setenv("WCMS_MACHINE_USER", "")
	if c := call("127.0.0.1:5000", "0123456789abcdef-test"); c != http.StatusServiceUnavailable {
		t.Errorf("⚠ 書き手がいないのに通しました（%d）", c)
	}
	t.Setenv("WCMS_MACHINE_ALLOW", "192.168.1.0/24")
	if c := call("192.168.1.20:5000", "wrong-token-xxxxxxx"); c != http.StatusUnauthorized {
		t.Errorf("⚠ 許した網からでも、鍵が違えば断ること（%d）", c)
	}
}

// TestMachineFax は FAX の受信の口を固定します（2026-10-21 夜・【考察】通信箱の改善 §7.4）。
//
//   - チャネル FAX・向き受信・対応：未処理の記録ができ、FAX が添付に置かれて本文に表示（ファイル表示の印）される
//   - 送ってきた番号を `FAX番号` で連絡帳の相手に引き、記録にも番号を書く
//   - PDF でないもの（先頭が %PDF- でない .pdf・TIFF）は断る
func TestMachineFax(t *testing.T) {
	setupIntakeTest(t)
	if err := page.WriteSidecar("000301", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000301", `<h1>ふじ鍍金</h1><dl data-type="tags"><dt>`+FaxNumberTag+`</dt><dd>06-9876-5432</dd></dl>`); err != nil {
		t.Fatal(err)
	}
	u := &auth.User{Username: "alice"}
	job := ""
	post := func(name string, content []byte, from string) (int, map[string]any) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(content)
		mw.WriteField("from", from)
		mw.WriteField("job", job)
		mw.WriteField("at", "2026-10-21T13:05:00+09:00")
		mw.Close()
		req := auth.WithUser(httptest.NewRequest("POST", "/api/machine/fax", &buf), u)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rr := httptest.NewRecorder()
		MachineFaxAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	code, out := post("fax.pdf", []byte("%PDF-1.4 fax"), "0698765432")
	if code != http.StatusOK {
		t.Fatalf("FAX を受けられません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body, _ := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	for _, want := range []string{"FAX ふじ鍍金 2026-10-21 13:05", "<dd>FAX</dd>", "<dd>受信</dd>", "<dd>000301</dd>", "<dd>0698765432</dd>", `data-type="file-view" data-ref="` + id + `-`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("⚠ FAX の記録に %q がありません:\n%s", want, body)
		}
	}
	if len(cms.ReadAttachmentMetas(id)) != 1 {
		t.Errorf("⚠ FAX が添付に置かれていません")
	}
	if code, _ := post("fax.pdf", []byte("not a pdf"), ""); code != http.StatusBadRequest {
		t.Errorf("⚠ PDF でないものを受けました（%d）", code)
	}
	if code, _ := post("fax.tif", []byte("II*\x00"), ""); code != http.StatusBadRequest {
		t.Errorf("⚠ TIFF を受けました（%d）", code)
	}
	// 同じ受信番号で送り直されたら、記録を増やさない（国の番号で名乗っても、連絡帳の番号に合う）。
	job = "000123"
	_, first := post("fax-000123.pdf", []byte("%PDF-1.4 again"), "+81 6 9876 5432")
	_, second := post("fax-000123.pdf", []byte("%PDF-1.4 again"), "+81 6 9876 5432")
	if first["page_id"] == nil || first["page_id"] != second["page_id"] || second["duplicate"] != true {
		t.Errorf("⚠ 送り直しで記録が増えました: %v / %v", first, second)
	}
	if id2, _ := first["page_id"].(string); id2 != "" {
		b, _ := os.ReadFile(filepath.Join(page.GetPageDir(id2), id2+".html"))
		if !strings.Contains(string(b), "<dd>000301</dd>") || len(cms.ReadAttachmentMetas(id2)) != 1 {
			t.Errorf("⚠ +81 の番号で相手を引けないか、添付が増えました:\n%s", b)
		}
	}
}
