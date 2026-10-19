package toho

// 業者が、送った品物の一覧に値段を書き足して返してきた表計算（2026-10-19）の番人です。
//
// 利用者:「…は見積回答ですが、xlsxの解析で発注書・図面・部材見積依頼の回答のいずれでもないと出ます。単なる単価の入った表に
// 過ぎないからかもしれませんが、ここから見積もりの返答を作りたいです。見積依頼も図面を送っただけですが、ここからDBに入る元の
// ページを作りたいです」「このようなエクセル形式でも解析できるようにならないでしょうか？ならない場合、手作業で見積もり回答書を
// 作れるとありがたいです」。

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// sentMailBody は、こちらが送ったメールの控えの本文です。
func sentMailBody(subject, at, text string) string {
	return `<h1>` + subject + `</h1><dl data-type="tags"><dt>` + comm.ChannelTag + `</dt><dd>メール</dd>` +
		`<dt>` + comm.DirectionTag + `</dt><dd>送信</dd><dt>` + comm.SentAtTag + `</dt><dd>` + at + `</dd></dl>` +
		`<section><h2>` + comm.MailBodyHeading + `</h2><pre>` + text + `</pre></section>`
}

// replyMailBody は、送ったメール parent への返信の本文です。
func replyMailBody(subject, parent, text string) string {
	return strings.Replace(mailRecordBody(subject, text), `</dl>`,
		`<dt>`+comm.ParentPageTag+`</dt><dd>`+parent+`</dd></dl>`, 1)
}

func seedPage(t *testing.T, id, parent, body string) {
	t.Helper()
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "root", Mode: "302", ParentID: parent}); err != nil {
		t.Fatal(err)
	}
	seedBody(t, id, body)
}

// TestSheetMailContextTellsTheMail は、表計算の判定にメールの文脈を添えることを固定します——届いたメールの件名と本文、
// 返信なら送ったメールも。メールの記録でないページには何も足さない。
func TestSheetMailContextTellsTheMail(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	seedPage(t, "000600", cms.TopPageID, sentMailBody("塗装のお見積りのお願い", "2026-07-17T16:39:09+09:00", "図面と参考価格の一覧を送ります"))
	seedPage(t, "000601", cms.TopPageID, replyMailBody("RE: 塗装のお見積りのお願い", "000600", "希望単価を追記しました"))
	got := sheetMailContext("000601")
	for _, want := range []string{"RE: 塗装のお見積りのお願い", "希望単価を追記しました", "当社が送った次のメールへの返信", "図面と参考価格の一覧を送ります"} {
		if !strings.Contains(got, want) {
			t.Errorf("判定に添える文に %q がありません:\n%s", want, got)
		}
	}
	if got := sheetMailContext("000600"); got != "" {
		t.Errorf("送ったメールの控えに文脈を足しています: %q", got)
	}
	if got := sheetMailContext("000500"); got != "" {
		t.Errorf("メールの記録でないページに文脈を足しています: %q", got)
	}
}

// TestReplyColumnsPickTheSuppliersPrice は、返事の表の値段の列の読み替えを固定します——「参考：」の列は読まない・
// 「価格」も単価・単価の列が2つ以上なら「単価」を含む列、無ければいちばん右。
func TestReplyColumnsPickTheSuppliersPrice(t *testing.T) {
	for h, want := range map[string]string{"参考：昨年価格": "", "単価：希望価格": "cost", "希望価格": "cost", "単価": "cost", "金額": ""} {
		if got := replyColumnOf(h); got != want {
			t.Errorf("replyColumnOf(%q) = %q, want %q", h, got, want)
		}
	}
	for _, c := range []struct {
		headers []string
		want    int
	}{
		{[]string{"品名", "昨年価格", "希望価格"}, 2},
		{[]string{"品名", "単価", "希望価格"}, 1},
		{[]string{"品名", "単価：希望価格", "参考：昨年価格"}, 1},
	} {
		cols := make([]string, len(c.headers))
		for i, h := range c.headers {
			cols[i] = replyColumnOf(h)
		}
		keepOneCostColumn(c.headers, cols)
		n, at := 0, -1
		for i, col := range cols {
			if col == "cost" {
				n, at = n+1, i
			}
		}
		if n != 1 || at != c.want {
			t.Errorf("%v の単価の列 = %v（%d 個）, want 列 %d", c.headers, cols, n, c.want)
		}
	}
}

// TestLinkRFQReplySetsTheAskedDate は、返事を部材見積依頼書に結んだとき、返事のメールがこちらの送ったメールへの返信なら、
// 部材見積依頼日（送った日より後なら）と送付日（空なら）をその送った日に合わせることを固定します。人が書いた前の日付は触らない。
func TestLinkRFQReplySetsTheAskedDate(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	seedPage(t, "000600", cms.TopPageID, sentMailBody("塗装のお見積りのお願い", "2026-07-17T16:39:09+09:00", "図面を送ります"))
	seedPage(t, "000601", cms.TopPageID, replyMailBody("RE: 塗装のお見積りのお願い", "000600", "単価を追記しました"))
	rfq := func(asked, sent string) string {
		return `<h1>部材見積依頼</h1><dl data-type="tags"><dt>` + RFQNoTag + `</dt><dd>000603</dd><dt>` + SupplierTag + `</dt><dd>ふじ鍍金</dd>` +
			`<dt>` + RFQDateTag + `</dt><dd>` + asked + `</dd><dt>` + EstimateSentTag + `</dt><dd>` + sent + `</dd>` +
			`<dt>` + RFQAnsweredTag + `</dt><dd>2026-08-07</dd></dl>`
	}
	reply := `<h1>部材見積依頼の返事</h1><dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>ふじ鍍金</dd><dt>` + RFQAnsweredTag +
		`</dt><dd>2026-08-07</dd><dt>` + SourceRefTag + `</dt><dd>000601-ab12</dd></dl>`
	for _, c := range []struct{ asked, sent, wantAsked, wantSent string }{
		{"2026-08-07", "", "2026-07-17", "2026-07-17"},           // 回答日を入れていた（過去の見積もりの移し）→ 送った日
		{"2026-07-01", "2026-07-02", "2026-07-01", "2026-07-02"}, // 人が書いた前の日付は残す
	} {
		seedPage(t, "000603", cms.TopPageID, rfq(c.asked, c.sent))
		seedPage(t, "000602", "000601", reply)
		if err := linkRFQReply(&auth.User{Username: "root", IsAdmin: true}, "000602", "000603"); err != nil {
			t.Fatal(err)
		}
		body := readPageBody(t, "000603")
		root := wrapInDiv(mustFragment(t, body))
		if got := cms.TagValue(root, RFQDateTag); got != c.wantAsked {
			t.Errorf("部材見積依頼日 %s のあと = %q, want %q", c.asked, got, c.wantAsked)
		}
		if got := cms.TagValue(root, EstimateSentTag); got != c.wantSent {
			t.Errorf("送付日 %q のあと = %q, want %q", c.sent, got, c.wantSent)
		}
		if got := cms.TagValue(root, RFQAnsweredTag); got != "2026-08-07" {
			t.Errorf("回答日が変わりました: %q", got)
		}
	}
}

// TestCreateAnsweredRFQPageWithAskedDate は、返事から作る部材見積依頼書の部材見積依頼日・送付日が送った日、回答日が返事の日になることを固定します。
func TestCreateAnsweredRFQPageWithAskedDate(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	id, code, err := createAnsweredRFQPage(&auth.User{Username: "root", IsAdmin: true}, "ふじ鍍金", "2026-07-17", "2026-08-07", "試験",
		[]ourOrderLine{{ItemID: "A100-B01", ItemName: "天板", Quantity: "20", Cost: "800"}})
	if err != nil {
		t.Fatalf("作れません: %d %v", code, err)
	}
	root := wrapInDiv(mustFragment(t, readPageBody(t, id)))
	for tag, want := range map[string]string{RFQDateTag: "2026-07-17", EstimateSentTag: "2026-07-17", RFQAnsweredTag: "2026-08-07"} {
		if got := cms.TagValue(root, tag); got != want {
			t.Errorf("%s = %q, want %q", tag, got, want)
		}
	}
}

// TestRFQReplyByHandMakesAnEmptyTable は、手で書く返事ページ（メールの「作るページ」）が、🤖 を呼ばずに、空の表を置いた返事ページを
// メールの子に作ることを固定します（仕入先は差出人・回答日はメールの日付・受信元はメール）。
func TestRFQReplyByHandMakesAnEmptyTable(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	seedPage(t, "000601", cms.TopPageID, mailRecordBody("RE: お見積り", "単価を追記しました"))
	judgeCalled := false
	orig := judgeRFQReplyMail
	judgeRFQReplyMail = func(string) (*rfqReplyMailJudgment, error) { judgeCalled = true; return nil, nil }
	t.Cleanup(func() { judgeRFQReplyMail = orig })
	res, err := makeRFQReplyByHand(&auth.User{Username: "root", IsAdmin: true}, "000601")
	if err != nil || len(res.Pages) != 1 {
		t.Fatalf("作れません: %+v %v", res, err)
	}
	if judgeCalled {
		t.Error("手で書く返事ページで 🤖 を呼んでいます")
	}
	id := res.Pages[0].PageID
	body := readPageBody(t, id)
	for _, want := range []string{"<th>品番</th>", "<th>単価</th>", rfqReplySourceCaption} {
		if !strings.Contains(body, want) {
			t.Errorf("返事ページに %q がありません:\n%s", want, body)
		}
	}
	root := wrapInDiv(mustFragment(t, body))
	if got := cms.TagValue(root, SourceRefTag); got != "000601" {
		t.Errorf("受信元 = %q", got)
	}
	if got := cms.TagValue(root, RFQAnsweredTag); got != "2026-09-30" {
		t.Errorf("回答日 = %q（メールの日付のはず）", got)
	}
	if meta, _ := page.ReadSidecar(id); meta.ParentID != "000601" {
		t.Errorf("返事ページの親 = %q", meta.ParentID)
	}
}

func mustFragment(t *testing.T, body string) []*html.Node {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

// TestAnalyzeSheetPassesMailContext は、メールの添付の表計算を 🤖 解析すると、判定にメールの文脈が渡ることを固定します（配線）。
func TestAnalyzeSheetPassesMailContext(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	seedPage(t, "000600", cms.TopPageID, sentMailBody("塗装のお見積りのお願い", "2026-07-17T16:39:09+09:00", "図面を送ります"))
	seedPage(t, "000601", cms.TopPageID, replyMailBody("RE: 塗装のお見積りのお願い", "000600", "希望単価を追記しました"))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":          `<workbook><sheets><sheet name="塗装単価" sheetId="1"/></sheets></workbook>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>天板</t></is></c><c r="B1"><v>800</v></c></row></sheetData></worksheet>`,
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	putAttachment(t, "000601", "ab12.xlsx", buf.Bytes())
	var got string
	orig := judgeOrderSheet
	judgeOrderSheet = func(text string) (*orderJudgment, error) { got = text; return &orderJudgment{DocType: "other"}, nil }
	t.Cleanup(func() { judgeOrderSheet = orig })
	rr := postAnalyze(t, &auth.User{Username: "root", IsAdmin: true}, map[string]string{"page_id": "000601", "file": "ab12.xlsx"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(got, "希望単価を追記しました") || !strings.Contains(got, "図面を送ります") || !strings.Contains(got, "A=天板 | B=800") {
		t.Errorf("判定にメールの文脈と表が渡っていません:\n%s", got)
	}
}

// TestRFQReplyDraftReadsTheSuppliersPrice は、返事の表から部材見積依頼明細の案を作るとき、値段の列が2つあれば業者の答え
// （いちばん右）だけを単価にすることを固定します（値段をつないで読まない）。
func TestRFQReplyDraftReadsTheSuppliersPrice(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	body := `<details open><summary>` + rfqReplySourceCaption + `</summary><table><tbody>` +
		`<tr><th>図面番号</th><th>品名</th><th>数量</th><th>昨年価格</th><th>希望価格</th></tr>` +
		`<tr><td>A100-B01</td><td>天板</td><td>20</td><td>713</td><td>800</td></tr></tbody></table></details>`
	rows := rfqReplyDraft(adminUser(), body)
	if len(rows) != 1 || rows[0].Cost != "800" || rows[0].ItemID != "A100-B01" || rows[0].Quantity != "20" {
		t.Fatalf("案 = %+v", rows)
	}
}
