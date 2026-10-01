package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// postAnalyzeMail は /api/analyze-mail-order を呼びます。
func postAnalyzeMail(t *testing.T, u *auth.User, pageID string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"page_id": pageID})
	req := httptest.NewRequest("POST", "/api/analyze-mail-order", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	AnalyzeMailOrderAPIHandler(rr, req)
	return rr
}

// mailRecordBody はメールの記録の本文を組みます（取り込みが作る形——タグと `本文` の節の `<pre>`）。
func mailRecordBody(subject, text string) string {
	return `<h1>` + subject + `</h1><dl data-type="tags"><dt>` + comm.ChannelTag + `</dt><dd>メール</dd>` +
		`<dt>` + comm.DirectionTag + `</dt><dd>受信</dd><dt>` + comm.FromTag + `</dt><dd>試験 &lt;e2e@invalid.example&gt;</dd>` +
		`<dt>` + comm.ReceivedAtTag + `</dt><dd>2026-09-30 10:00</dd></dl>` +
		`<section><h2>` + comm.MailBodyHeading + `</h2><pre>` + text + `</pre></section>`
}

// TestAnalyzeMailCreatesOrderPage は、**メールの本文から受注ページができる**ことを固定します（2026-10-01 利用者:
// 「発注書が無くても、メールから簡単に発注ページを作れませんか？」→ 受注ページ）。
//
//	件名・差出人・日付・本文を判定へ渡す／受注ページはメールの記録の子／`受信元` はメールのページ全体／
//	原本の PDF の枠は残さない（空の枠は「参照がありません」になる）／明細が入る
func TestAnalyzeMailCreatesOrderPage(t *testing.T) {
	const id = "000015"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	seedBody(t, id, mailRecordBody("追加のお願い", "いつもお世話になります。\nK120-3 取付ベース 5個 お願いします。\n\n&gt; 前回の件"))

	var got string
	orig := judgeOrderMail
	judgeOrderMail = func(text string) (*orderJudgment, error) {
		got = text
		return &orderJudgment{IsClientOrder: true, DocType: "order", Customer: "南北スポーツ", OrderDate: "2026-09-30",
			Items: []orderPDFItem{{ItemNo: "K120-3", ItemName: "取付ベース", Quantity: "5"}}}, nil
	}
	t.Cleanup(func() { judgeOrderMail = orig })

	rr := postAnalyzeMail(t, &auth.User{Username: "alice"}, id)
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"件名: 追加のお願い", "差出人: 試験 <e2e@invalid.example>", "日付: 2026-09-30 10:00", "K120-3 取付ベース 5個"} {
		if !strings.Contains(got, want) {
			t.Errorf("判定へ渡す文字に %q がありません:\n%s", want, got)
		}
	}
	var res struct {
		IsClientOrder bool   `json:"is_client_order"`
		PageID        string `json:"page_id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if !res.IsClientOrder || res.PageID == "" {
		t.Fatalf("受注ページができていません: %s", rr.Body.String())
	}
	body := readPageBody(t, res.PageID)
	if !strings.Contains(body, "<td>K120-3</td>") {
		t.Errorf("明細が入っていません:\n%s", body)
	}
	if !strings.Contains(body, "<dt>"+SourceRefTag+"</dt><dd>"+id+"</dd>") {
		t.Errorf("受信元がメールのページ全体になっていません:\n%s", body)
	}
	if strings.Contains(body, `data-type="file-view"`) || strings.Contains(body, sourcePDFCaption) {
		t.Errorf("⚠ 原本の PDF の枠が残っています（空の枠は「参照がありません」になる）:\n%s", body)
	}
	if meta, _ := page.ReadSidecar(res.PageID); meta.ParentID != id {
		t.Errorf("受注ページがメールの記録の子になっていません: 親=%s", meta.ParentID)
	}
	if marks := mailOrderPages(&auth.User{Username: "alice"}, id); len(marks) != 1 || marks[0].PageID != res.PageID {
		t.Errorf("メールの記録から作った受注ページの印が引けません: %+v", marks)
	}
}

// TestAnalyzeMailRefusesNonOrder は、注文ではないと判定されたらページを作らないことと、メールの記録でない
// ページは断ることを固定します。
func TestAnalyzeMailRefusesNonOrder(t *testing.T) {
	const id = "000016"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	seedBody(t, id, mailRecordBody("見積のお願い", "K120-3 を10個の場合の見積をお願いします。"))
	orig := judgeOrderMail
	judgeOrderMail = func(string) (*orderJudgment, error) { return &orderJudgment{DocType: "other"}, nil }
	t.Cleanup(func() { judgeOrderMail = orig })

	rr := postAnalyzeMail(t, &auth.User{Username: "alice"}, id)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"is_client_order":false`) {
		t.Fatalf("注文ではないメールの答えが違います: %d %s", rr.Code, rr.Body.String())
	}
	if marks := mailOrderPages(&auth.User{Username: "alice"}, id); len(marks) != 0 {
		t.Errorf("注文ではないのに受注ページを作っています: %+v", marks)
	}

	seedBody(t, id, "<h1>ただのページ</h1><pre>K120-3 5個</pre>")
	if rr := postAnalyzeMail(t, &auth.User{Username: "alice"}, id); rr.Code != 400 {
		t.Errorf("メールの記録でないページを断っていません: %d %s", rr.Code, rr.Body.String())
	}
}
