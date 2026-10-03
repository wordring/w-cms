package mail

// 送る欄の初期値・下書き・送る口の用件（2026-09-30）。
//
// ⚠ **本物のメールは出しません**——送る口（`sendMail`）を偽物に差し替えて、何が渡ったかを見ます。

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 試験の用件（送る前に1つ添え・送れたら呼ばれたことを覚える）。
const testPurpose = "試験の用件"

var testAfter struct{ pageID, recordID string }

// testAfterErr を立てると、送れたあとの仕事が失敗したことにする。
var testAfterErr error

func init() {
	comm.RegisterSendPurpose(testPurpose, comm.SendPurpose{
		NeedsPage: true,
		Defaults: func(_ *auth.User, pageID string) (comm.ComposeDraft, error) {
			return comm.ComposeDraft{To: []string{"t@example.jp"}, SendNote: "送ると試験の紙を添えます", Reload: true, Generated: "試験の紙.pdf"}, nil
		},
		Prepare: func(w http.ResponseWriter, r *http.Request, pageID string) ([]comm.ComposeAttachment, bool) {
			return []comm.ComposeAttachment{{PageID: pageID, File: "pp01.pdf", Name: "試験の紙.pdf"}}, true
		},
		AfterSent: func(_ *auth.User, pageID, recordID string) error {
			testAfter.pageID, testAfter.recordID = pageID, recordID
			return testAfterErr
		},
	})
}

var alice = &auth.User{Username: "alice"}

// seedSourceMail は返信元の受信メールの記録（/000200）を置きます。
func seedSourceMail(t *testing.T) {
	t.Helper()
	putPage(t, "000200", "000100", "alice", "330", `<h1>見積の件</h1><dl data-type="tags">`+
		`<dt>`+comm.DirectionTag+`</dt><dd>`+comm.DirectionIn+`</dd>`+
		`<dt>`+comm.ChannelTag+`</dt><dd>`+comm.ChannelMail+`</dd>`+
		`<dt>`+comm.FromTag+`</dt><dd>山田 &lt;yamada@example.jp&gt;</dd>`+
		`<dt>`+comm.MessageIDTag+`</dt><dd>&lt;src@example.jp&gt;</dd></dl>`+
		`<section><h2>`+comm.MailBodyHeading+`</h2><pre>こんにちは
2行目</pre></section>`+
		`<section><h2>`+comm.MailFilesHeading+`</h2><p>📎 <a href="/000200/ab12.pdf" download="見積.pdf">見積.pdf</a></p>`+
		`<p>🔗 <a href="https://example.com/x.pdf">外</a></p></section>`)
	putFile(t, "000200", "ab12.pdf", []byte("%PDF-1.4 見積"))
	putFile(t, "000200", "pp01.pdf", []byte("%PDF-1.4 試験の紙"))
}

func callJSON(t *testing.T, h http.HandlerFunc, method, url string, body any, user *auth.User) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, rd)
	req = auth.WithUser(req, user)
	rec := httptest.NewRecorder()
	h(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestReplyDefaults は返信の初期値を固定します——宛先は素のアドレス・`RE:` は1つ・引用・添付の候補（印なし）。
func TestReplyDefaults(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	d, err := replyDefaults(alice, "000200")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.To, ",") != "yamada@example.jp" {
		t.Errorf("宛先が %v です（素のアドレスのはず）", d.To)
	}
	if d.Subject != "RE: 見積の件" {
		t.Errorf("件名が %q です", d.Subject)
	}
	for _, want := range []string{"山田 <yamada@example.jp> さんは書きました:", "> こんにちは\n> 2行目\n"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("本文に %q がありません:\n%s", want, d.Body)
		}
	}
	if len(d.Attachments) != 1 || d.Attachments[0].File != "ab12.pdf" || d.Attachments[0].Name != "見積.pdf" || d.Attachments[0].Checked {
		t.Errorf("添付の候補が %+v です（見積.pdf を印なしで1つ・外へのリンクは拾わない）", d.Attachments)
	}
	// RE: を重ねない。
	putPage(t, "000201", "000100", "alice", "330", `<h1>Re: 見積の件</h1>`)
	if d, _ := replyDefaults(alice, "000201"); d.Subject != "Re: 見積の件" {
		t.Errorf("RE: を重ねています: %q", d.Subject)
	}
}

// TestComposeRefusesUnknownAndUnreadable は、無い用件は 400・読めない元のページは 404（「無い」と同じ顔）。
func TestComposeRefusesUnknownAndUnreadable(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	putPage(t, "000300", "000100", "bob", "300", `<h1>内緒</h1>`)
	if code, _ := callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?purpose=無い用件&page_id=000200", nil, alice); code != 400 {
		t.Errorf("無い用件で %d です", code)
	}
	if code, _ := callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?purpose="+comm.PurposeReply+"&page_id=000300", nil, alice); code != 404 {
		t.Errorf("読めないページへの返信の初期値が %d です（404 のはず）", code)
	}
	if code, _ := callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?purpose="+comm.PurposeReply, nil, alice); code != 400 {
		t.Errorf("元のページの無い返信で %d です", code)
	}
	code, out := callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?purpose="+comm.PurposeNew, nil, alice)
	if code != 200 || out["success"] != true {
		t.Errorf("新規の初期値が %d %v です", code, out)
	}
}

// TestDraftRoundTrips は、下書きを保存して開き直すと**書いたとおり**に戻ること（空行で始まる本文も）、
// 書き換えは同じページ・下書きでないページは書き換えないことを固定します。
func TestDraftRoundTrips(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	body := "\n\nよろしくお願いします。\n\n> こんにちは\n"
	req := map[string]any{
		"purpose": comm.PurposeReply, "page_id": "000200",
		"to": []string{"yamada@example.jp"}, "cc": []string{"cc@example.jp"},
		"subject": "RE: 見積の件", "body": body,
		"attachments": []map[string]string{{"page_id": "000200", "file": "ab12.pdf", "name": "見積.pdf"}},
	}
	code, out := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", req, alice)
	if code != 200 {
		t.Fatalf("下書きを保存できません: %d %v", code, out)
	}
	draftID, _ := out["draft_id"].(string)
	n, _ := strconv.Atoi(draftID)
	tags, _ := cms.TagsOfPage(database.DB, n)
	if got := tags[comm.DraftTag]; len(got) != 1 || got[0] != comm.PurposeReply {
		t.Errorf("下書きの印が %v です", got)
	}
	if got := tags[comm.DraftSourceTag]; len(got) != 1 || got[0] != "000200" {
		t.Errorf("下書きの元が %v です", got)
	}
	// ⚠ **対応の印は付けない**（未処理に並ぶ——書きかけを忘れない）・`返信元` もまだ付けない。
	if len(tags[comm.HandledTag]) > 0 || len(tags[comm.ReplySourceTag]) > 0 || len(tags[comm.SentAtTag]) > 0 {
		t.Errorf("下書きに送った印が付いています: %v", tags)
	}

	d, ok := draftOf(draftID)
	if !ok {
		t.Fatal("下書きを読み戻せません")
	}
	if d.Body != strings.TrimRight(body, "\n") {
		t.Errorf("本文が書いたとおりに戻りません:\n%q\n%q", d.Body, body)
	}
	if d.Purpose != comm.PurposeReply || d.PageID != "000200" || d.Subject != "RE: 見積の件" ||
		strings.Join(d.To, ",") != "yamada@example.jp" || strings.Join(d.Cc, ",") != "cc@example.jp" {
		t.Errorf("下書きの中身が違います: %+v", d)
	}
	if len(d.Attachments) != 1 || !d.Attachments[0].Checked || d.Attachments[0].Name != "見積.pdf" {
		t.Errorf("添付が %+v です（選んだものを印付きで）", d.Attachments)
	}

	// 書き換えは同じページ。
	req["draft_id"] = draftID
	req["subject"] = "RE: 見積の件（直した）"
	if code, out := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", req, alice); code != 200 || out["draft_id"] != draftID {
		t.Fatalf("書き換えが %d %v です", code, out)
	}
	if got := cms.PageTitleByID(n); got != "RE: 見積の件（直した）" {
		t.Errorf("題が %q です", got)
	}
	// ⚠ **下書きでないページは書き換えない**（この口で任意のページを上書きさせない）。
	req["draft_id"] = "000200"
	if code, _ := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", req, alice); code != 409 {
		t.Errorf("下書きでないページを書き換えようとして %d です（409 のはず）", code)
	}
	if b, _ := cms.ReadPageBody("000200"); !strings.Contains(b, "<h1>見積の件</h1>") {
		t.Errorf("⚠ 下書きでないページが書き換わりました:\n%s", b)
	}
}

// TestSendFromDraftRunsPurposeHooks は、用件つきで下書きから送ると——送る直前の添付が足され、
// 返信として In-Reply-To が付き、控えができ、送れたあとの仕事が呼ばれ、下書きはごみ箱へ——を固定します。
func TestSendFromDraftRunsPurposeHooks(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	var sent comm.OutgoingMail
	orig := sendMail
	sendMail = func(_ *auth.User, m comm.OutgoingMail) (string, error) { sent = m; return "<new@example.jp>", nil }
	t.Cleanup(func() { sendMail = orig })

	_, out := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", map[string]any{
		"purpose": testPurpose, "page_id": "000200", "to": []string{"t@example.jp"}, "subject": "試験", "body": "本文",
	}, alice)
	draftID, _ := out["draft_id"].(string)

	testAfter.pageID, testAfter.recordID, testAfterErr = "", "", nil
	code, out := callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
		"purpose": testPurpose, "page_id": "000200", "draft_id": draftID,
		"to": []string{"t@example.jp"}, "subject": "試験", "body": "本文",
		"attachments": []map[string]string{{"page_id": "000200", "file": "ab12.pdf", "name": "見積.pdf"}},
	}, alice)
	if code != 200 || out["success"] != true {
		t.Fatalf("送れません: %d %v", code, out)
	}
	var names []string
	for _, a := range sent.Attachments {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "見積.pdf,試験の紙.pdf" {
		t.Errorf("添付が %v です（選んだもの＋送る直前に足したもの）", names)
	}
	recordID, _ := out["page_id"].(string)
	if recordID == "" || testAfter.pageID != "000200" || testAfter.recordID != recordID {
		t.Errorf("送れたあとの仕事が %+v で呼ばれました（控え %q）", testAfter, recordID)
	}
	// 下書きはごみ箱へ（控えは別に作った）。
	if _, err := os.Stat(page.GetPageDir(draftID)); !os.IsNotExist(err) {
		t.Errorf("⚠ 下書き /%s が残っています", draftID)
	}
	if recordID == draftID {
		t.Errorf("控えが下書きと同じページです")
	}
	if _, ok := out["after_error"]; ok {
		t.Errorf("失敗していないのに after_error: %v", out)
	}

	// 送れたあとの仕事が失敗しても、送れたことは成功のまま（理由を添える）。
	testAfterErr = errors.New("印を付けられませんでした")
	code, out = callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
		"purpose": testPurpose, "page_id": "000200", "to": []string{"t@example.jp"}, "body": "本文",
	}, alice)
	if code != 200 || out["success"] != true || !strings.Contains(out["after_error"].(string), "印を付けられませんでした") {
		t.Errorf("後の仕事の失敗の扱いが違います: %d %v", code, out)
	}
	testAfterErr = nil
}

// TestSendReplyPurposeThreads は、用件「返信」で送ると元のメールへの返信（In-Reply-To・返信元）になることを固定します。
func TestSendReplyPurposeThreads(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	var sent comm.OutgoingMail
	orig := sendMail
	sendMail = func(_ *auth.User, m comm.OutgoingMail) (string, error) { sent = m; return "<new@example.jp>", nil }
	t.Cleanup(func() { sendMail = orig })

	code, out := callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
		"purpose": comm.PurposeReply, "page_id": "000200", "to": []string{"yamada@example.jp"}, "body": "ありがとうございます",
	}, alice)
	if code != 200 {
		t.Fatalf("送れません: %d %v", code, out)
	}
	if sent.InReplyTo != "<src@example.jp>" {
		t.Errorf("In-Reply-To が %q です", sent.InReplyTo)
	}
	rec, _ := strconv.Atoi(out["page_id"].(string))
	tags, _ := cms.TagsOfPage(database.DB, rec)
	if got := tags[comm.ReplySourceTag]; len(got) != 1 || got[0] != "000200" {
		t.Errorf("控えの返信元が %v です", got)
	}
}

// TestSendRefusesUnwritableDraftBeforeSending は、⚠ **書けない下書きからは1通も送らない**ことを固定します
// （送ってから片付けられないと分かっても、出たメールは戻せない）。
func TestSendRefusesUnwritableDraftBeforeSending(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	called := false
	orig := sendMail
	sendMail = func(_ *auth.User, m comm.OutgoingMail) (string, error) { called = true; return "", nil }
	t.Cleanup(func() { sendMail = orig })
	code, _ := callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
		"draft_id": "000200", "to": []string{"t@example.jp"}, "body": "本文",
	}, alice)
	if code != 409 || called {
		t.Errorf("下書きでないページを draft_id にして %d・送った=%v です（409 で送らないはず）", code, called)
	}
}

// TestSendSkipsGeneratedWhenUnchecked は、送る欄で「送るときに作るファイル」（Generated）の印を外すと、送る直前の仕事
// （Prepare——PDF を作って添える）を呼ばず、その添付が付かないことを固定します（2026-10-03 利用者:「見積書PDFも他と同じように
// 表示し、ただし最初から添付に入っているように」——印つきで並び、外せば作らない）。初期値には Generated が載る。
func TestSendSkipsGeneratedWhenUnchecked(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	var sent comm.OutgoingMail
	orig := sendMail
	sendMail = func(_ *auth.User, m comm.OutgoingMail) (string, error) { sent = m; return "<new@example.jp>", nil }
	t.Cleanup(func() { sendMail = orig })
	send := func(skip bool) []string {
		t.Helper()
		sent = comm.OutgoingMail{}
		code, out := callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
			"purpose": testPurpose, "page_id": "000200", "to": []string{"t@example.jp"}, "body": "本文",
			"skip_generated": skip,
		}, alice)
		if code != 200 || out["success"] != true {
			t.Fatalf("送れません: %d %v", code, out)
		}
		var names []string
		for _, a := range sent.Attachments {
			names = append(names, a.Name)
		}
		return names
	}
	// 初期値にも、保存した下書きを開き直したときにも、送るときに作るファイルの名前が載る（送る欄が印つきで並べる）。
	_, out := callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?purpose="+url.QueryEscape(testPurpose)+"&page_id=000200", nil, alice)
	if d, _ := out["draft"].(map[string]any); d == nil || d["generated"] != "試験の紙.pdf" {
		t.Errorf("初期値に generated がありません: %v", out)
	}
	_, saved := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", map[string]any{
		"purpose": testPurpose, "page_id": "000200", "to": []string{"t@example.jp"}, "subject": "試験", "body": "本文",
	}, alice)
	_, out = callJSON(t, ComposeAPIHandler, "GET", "/api/mail/compose?draft="+saved["draft_id"].(string), nil, alice)
	if d, _ := out["draft"].(map[string]any); d == nil || d["generated"] != "試験の紙.pdf" {
		t.Errorf("下書きを開き直すと generated がありません: %v", out)
	}
	if got := send(false); strings.Join(got, ",") != "試験の紙.pdf" {
		t.Errorf("印のままなら作って添えるはず: %v", got)
	}
	if got := send(true); len(got) != 0 {
		t.Errorf("印を外したのに添えています: %v", got)
	}
}
