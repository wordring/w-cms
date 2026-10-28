package comm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

// TestFaxSendFileFromBox は、通信箱の「📠 FAX を送る」（パソコンの PDF をそのまま・fax_send_file.go・2026-10-28）を固定します。
//
//   - PDF を HylaFAX へ頼み、送った FAX の記録を通信箱に作る（向き送信・FAX番号・送った PDF の表示・親ページID は無い）
//   - 番号は連絡帳の FAX番号から相手を引いて題に入れる
//   - PDF でない・PDF の中身でない・番号が読めない・料金の高い番号（管理者でない）は断り、HylaFAX へ頼まない
//
// > 2026-10-28 利用者:「FAXの送信と電話のダイヤルを通信フォルダから出来るようにしたいです。」
func TestFaxSendFileFromBox(t *testing.T) {
	setupIntakeTest(t)
	resetFaxJobs(t)
	if err := page.WriteSidecar("000300", page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex("000300", `<h1>みなと商店</h1><dl data-type="tags"><dt>`+FaxNumberTag+`</dt><dd>03-1234-5678</dd></dl>`); err != nil {
		t.Fatal(err)
	}
	var sent []string
	oldSend, oldStat := runSendfax, readFaxStat
	t.Cleanup(func() { runSendfax, readFaxStat = oldSend, oldStat })
	runSendfax = func(_ context.Context, number, path string) ([]byte, error) {
		b, _ := os.ReadFile(path) // 頼む時点で、一時のファイルに中身があること
		sent = append(sent, number+"|"+string(b))
		return []byte(fmt.Sprintf("request id is %d (group id %d) for host localhost (1 file)\n", 40+len(sent), 40+len(sent))), nil
	}
	readFaxStat = func(context.Context) string { return "" }

	u := &auth.User{Username: "alice"}
	post := func(name string, content []byte, number string) (int, map[string]any) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(content)
		mw.WriteField("number", number)
		mw.Close()
		req := auth.WithUser(httptest.NewRequest("POST", "/api/fax/send-file", &buf), u)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rr := httptest.NewRecorder()
		FaxSendFileAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	pdf := []byte("%PDF-1.4\n%%EOF\n")

	for _, c := range []struct {
		name, number string
		content      []byte
		code         int
	}{
		{"見積.docx", "03-1234-5678", pdf, http.StatusBadRequest},
		{"見積.pdf", "03-1234-5678", []byte("PDF ではない"), http.StatusBadRequest},
		{"見積.pdf", "abc", pdf, http.StatusBadRequest},
		{"見積.pdf", "010-1-234-5678", pdf, http.StatusForbidden},
	} {
		if code, out := post(c.name, c.content, c.number); code != c.code {
			t.Errorf("%s・%s: %d %v（%d のはず）", c.name, c.number, code, out, c.code)
		}
	}
	if len(sent) != 0 {
		t.Fatalf("⚠ 断るはずのものを HylaFAX へ頼みました: %v", sent)
	}

	code, out := post("見積.pdf", pdf, "03-1234-5678")
	if code != http.StatusOK || out["record"] == nil {
		t.Fatalf("送れません: %d %v", code, out)
	}
	if len(sent) != 1 || sent[0] != "0312345678|"+string(pdf) {
		t.Errorf("HylaFAX へ頼んだもの = %q", sent)
	}
	rec := out["record"].(string)
	var body string
	for i := 0; i < 50; i++ { // 裏の見張りが書いている最中は開けないことがある
		b, err := os.ReadFile(filepath.Join(page.GetPageDir(rec), rec+".html"))
		if err == nil {
			body = string(b)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"FAX 送信 みなと商店", "<dd>" + DirectionOut + "</dd>", "<dd>0312345678</dd>", `data-type="file-view"`, "<dd>000300</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("記録に %q がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<dt>"+ParentPageTag+"</dt>") {
		t.Errorf("⚠ 元のページが無いのに 親ページID を書いています:\n%s", body)
	}
	if v := cms.ReadAttachmentMetas(rec); len(v) != 1 {
		t.Errorf("記録に写した PDF が %d 個（1つのはず）", len(v))
	}
}

// TestBoxNumbers は、通信箱の相手の候補（box_numbers.go）が、読めるページの番号だけを出し、記録の番号を入れないことを固定します。
func TestBoxNumbers(t *testing.T) {
	setupIntakeTest(t)
	for _, p := range []struct{ id, parent, mode, body string }{
		{"000300", "000000", "330", `<h1>みなと商店</h1><dl data-type="tags"><dt>` + PhoneTag + `</dt><dd>03-1111-2222</dd><dt>` + FaxNumberTag + `</dt><dd>03-1111-3333</dd></dl>`},
		{"000301", "000000", "300", `<h1>読めない会社</h1><dl data-type="tags"><dt>` + PhoneTag + `</dt><dd>03-9999-0000</dd></dl>`},
		{"000302", "000100", "330", `<h1>電話の記録</h1><dl data-type="tags"><dt>` + ChannelTag + `</dt><dd>電話</dd><dt>` + PhoneTag + `</dt><dd>03-5555-6666</dd></dl>`},
	} {
		owner := "alice"
		if p.id == "000301" {
			owner = "carol"
		}
		if err := page.WriteSidecar(p.id, page.PageMeta{Owner: owner, Mode: p.mode, ParentID: p.parent}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(p.id, p.body); err != nil {
			t.Fatal(err)
		}
	}
	u := &auth.User{Username: "alice"}
	phones := boxNumbers(u, PhoneTag)
	if len(phones) != 1 || phones[0].Number != "03-1111-2222" || phones[0].Label != "みなと商店" || phones[0].Page != "000300" {
		t.Errorf("電話の候補 = %+v（みなと商店の1つだけのはず・読めない会社と記録は入れない）", phones)
	}
	if faxes := boxNumbers(u, FaxNumberTag); len(faxes) != 1 || faxes[0].Number != "03-1111-3333" {
		t.Errorf("FAX の候補 = %+v", faxes)
	}
	if v := unhandledViewHTML(u, 100); !strings.Contains(v, `id="w-dial-go"`) || !strings.Contains(v, `id="w-fax-go"`) {
		t.Errorf("通信箱に「☎ かける」「📠 FAX を送る」がありません:\n%s", v)
	}
}
