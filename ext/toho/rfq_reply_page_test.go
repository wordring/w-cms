package toho

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 見積依頼の返事ページ（2026-10-05・rfq_reply_page.go）——通信記録の 🤖 解析が返事と判定したら返事ページを作り、
// どの見積依頼書への返事かを突き合わせる（候補が1つで仕入先も合えば結んで見積依頼書の子へ・それ以外は人が選ぶ）。
// ⚠ **Gemini は呼びません**——判定（judgeOrderPDF・judgeRFQReplyMail）を偽物に差し替えます。

const rfqReplyBox = "000040"

var rfqReplyRoot = &auth.User{Username: "root", IsAdmin: true}

// newRFQDoc は回答待ちの見積依頼書ページを1枚作って、そのページ番号を返します。
func newRFQDoc(t *testing.T, supplier string) string {
	t.Helper()
	seedBody(t, rfqReplyBox, `<h1>`+RFQBoxTitle+`</h1>`+tableOfLinesHTML(RFQDraftType, []ourOrderLine{
		{ProductID: "000041", Kind: "材料", Material: "SS400", Shape: "板", Size: "t6*80*120", Quantity: "6", Unit: "枚"},
	}, true))
	_, out := postRFQ(t, rfqReplyRoot, RFQNewDocAPIHandler, map[string]any{"page_id": rfqReplyBox, "table": 1, "supplier": supplier})
	id, _ := out["page_id"].(string)
	if id == "" {
		t.Fatalf("見積依頼書ページを作れません: %v", out)
	}
	return id
}

// newReplyMail は返事のメールの記録（件名 subject・PDF の添付 rp01.pdf）を作って、そのページ番号を返します。
func newReplyMail(t *testing.T, subject, bodyText string) string {
	t.Helper()
	id, err := cms.CreateChildPage(rfqReplyBox, "root", `<h1>`+subject+`</h1><dl data-type="tags"><dt>`+comm.DirectionTag+`</dt><dd>`+
		comm.DirectionIn+`</dd><dt>`+comm.ChannelTag+`</dt><dd>メール</dd><dt>`+comm.FromTag+`</dt><dd>sales@example.jp</dd><dt>`+
		comm.ReceivedAtTag+`</dt><dd>2026-10-05T09:00:00+09:00</dd></dl><section><h2>`+comm.MailBodyHeading+`</h2><pre>`+bodyText+`</pre></section>`)
	if err != nil {
		t.Fatal(err)
	}
	putAttachment(t, id, "rp01.pdf", []byte("%PDF-1.4 返事"))
	return id
}

// stubReply は解析の判定を「見積依頼の返事」に差し替えます。
func stubReply(t *testing.T, rfqNo, supplier string) {
	t.Helper()
	stubJudge(t, func(_ []byte) (*orderJudgment, error) {
		return parseOrderJudgment(`{"doc_type":"rfq_reply","is_client_order":false,"rfq_reply":{"rfq_no":"` + rfqNo + `","supplier":"` + supplier +
			`","date":"2026-10-04","source_table":{"headers":["品名","数量","単価"],"rows":[["SS400 t6","6","1,200"]]}}}`)
	})
}

type replyOut struct {
	Success    bool     `json:"success"`
	DocType    string   `json:"doc_type"`
	PageID     string   `json:"page_id"`
	Title      string   `json:"title"`
	LinkedRFQ  string   `json:"linked_rfq"`
	Candidates []string `json:"candidates"`
	Message    string   `json:"message"`
}

func analyzeReply(t *testing.T, mailID string) replyOut {
	t.Helper()
	rr := postAnalyze(t, rfqReplyRoot, map[string]string{"page_id": mailID, "file": "rp01.pdf"})
	var out replyOut
	json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || !out.Success || out.DocType != "rfq_reply" || out.PageID == "" {
		t.Fatalf("返事ページを作れません: %d %s", rr.Code, rr.Body.String())
	}
	return out
}

func parentOf(t *testing.T, id string) string {
	t.Helper()
	m, _ := page.ReadSidecar(id)
	return m.ParentID
}

// TestAnalyzeRFQReplyLinksByNumber は、紙の№で見積依頼書が1つに決まり仕入先も合えば——返事ページを作り（題・タグ・原本・
// 読んだままの表）、結んで（タグ 見積依頼）見積依頼書の子へ移し、見積依頼書の「🤖 返事を読む」の候補のいちばん上に原本が出る——
// を固定します。解析済みの印は「返事」。
func TestAnalyzeRFQReplyLinksByNumber(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	rfq := newRFQDoc(t, "わかば鋼業")
	other := newRFQDoc(t, "わかば鋼業") // 同じ仕入先の回答待ちがもう1つ——№が強い手掛かりなので候補に混ぜない
	mail := newReplyMail(t, "お見積りの件", "添付のとおりです。")
	stubReply(t, "№ "+rfq, "株式会社わかば鋼業")

	out := analyzeReply(t, mail)
	if out.LinkedRFQ != rfq {
		t.Fatalf("№ %s の見積依頼書に結んでいません: %+v（もう1枚は %s）", rfq, out, other)
	}
	if got := parentOf(t, out.PageID); got != rfq {
		t.Errorf("返事ページが見積依頼書の子へ移っていません（親 %s）", got)
	}
	body := readPageBody(t, out.PageID)
	for _, want := range []string{"<h1>見積依頼の返事　わかば鋼業</h1>", "<dt>" + RFQAnsweredTag + "</dt><dd>2026-10-04</dd>",
		"<dt>" + RFQReplyLinkTag + "</dt><dd>" + rfq + "</dd>", "<dt>" + SourceRefTag + "</dt><dd>" + mail + "-rp01</dd>",
		`data-ref="` + mail + `-rp01"`, "SS400 t6", "1,200"} {
		if !strings.Contains(body, want) {
			t.Errorf("返事ページに %q がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<dt>"+RFQNoTag+"</dt>") {
		t.Errorf("⚠ 返事ページが見積依頼番号のタグを持っています（見積依頼書と見分けられない）")
	}
	if kindOfPage(pageNum(out.PageID)) != rfqReplyKind {
		t.Errorf("解析済みの印が %q です", kindOfPage(pageNum(out.PageID)))
	}
	srcs := rfqReplyPageSources(rfqReplyRoot, rfq)
	if len(srcs) != 1 || srcs[0].PageID != mail || srcs[0].File != "rp01.pdf" || !strings.HasPrefix(srcs[0].Label, "返事ページ: ") {
		t.Errorf("見積依頼書の候補に返事ページの原本がありません: %+v", srcs)
	}
	// 結んだあとの欄は見積依頼書へのリンク（単価を写す道）。
	if v := rfqReplyMatchViewHTML(rfqReplyRoot, pageNum(out.PageID)); !strings.Contains(v, `href="/`+rfq+`#rfq-reply"`) {
		t.Errorf("結んだ返事ページに見積依頼書で単価を写すリンクがありません:\n%s", v)
	}
}

// TestAnalyzeRFQReplyNeedsHumanWhenUnsure は、結ばない場合——№の先の仕入先が食い違う・同じ仕入先の回答待ちが2つ・手掛かりが無い——
// は返事ページをメールの子に残して候補を出し、人が選ぶと結んで移す、ことを固定します。件名の№と、仕入先の回答待ちが1つなら結ぶ。
func TestAnalyzeRFQReplyNeedsHumanWhenUnsure(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	fuji := newRFQDoc(t, "ふじ鍍金")
	w1 := newRFQDoc(t, "わかば鋼業")
	w2 := newRFQDoc(t, "わかば鋼業")

	// ① №の先は ふじ鍍金 の見積依頼——返事は わかば鋼業（№の読み違い）→ 結ばない・候補に出す。
	mail := newReplyMail(t, "お見積り", "")
	stubReply(t, fuji, "わかば鋼業")
	out := analyzeReply(t, mail)
	if out.LinkedRFQ != "" || strings.Join(out.Candidates, ",") != fuji || parentOf(t, out.PageID) != mail {
		t.Errorf("仕入先が食い違うのに結んだか、候補が違います: %+v（親 %s）", out, parentOf(t, out.PageID))
	}

	// ② 手掛かりが仕入先だけで、回答待ちが2つ → 候補2つ・結ばない。人が選ぶと結んで移す。
	mail2 := newReplyMail(t, "お見積り", "")
	stubReply(t, "", "わかば鋼業")
	out2 := analyzeReply(t, mail2)
	if out2.LinkedRFQ != "" || len(out2.Candidates) != 2 {
		t.Fatalf("候補が2つなのに結んだか、候補が違います: %+v", out2)
	}
	v := rfqReplyMatchViewHTML(rfqReplyRoot, pageNum(out2.PageID))
	if !strings.Contains(v, `data-rfq="`+w1+`"`) || !strings.Contains(v, `data-rfq="`+w2+`"`) {
		t.Errorf("突き合わせの欄に候補2つがありません:\n%s", v)
	}
	if code, res := postRFQ(t, rfqReplyRoot, RFQReplyLinkAPIHandler, map[string]any{"page_id": out2.PageID, "rfq": w2}); code != 200 || res["rfq"] != w2 {
		t.Fatalf("人が選んで結べません: %d %v", code, res)
	}
	if parentOf(t, out2.PageID) != w2 || !strings.Contains(readPageBody(t, out2.PageID), "<dt>"+RFQReplyLinkTag+"</dt><dd>"+w2+"</dd>") {
		t.Errorf("結んだのに見積依頼書の子へ移っていないか、タグがありません")
	}
	// 見積依頼書でない番号・返事ページでないページは結ばない。
	if code, _ := postRFQ(t, rfqReplyRoot, RFQReplyLinkAPIHandler, map[string]any{"page_id": out2.PageID, "rfq": mail}); code != 404 {
		t.Errorf("見積依頼書でない番号へ結ぼうとして %d", code)
	}
	if code, _ := postRFQ(t, rfqReplyRoot, RFQReplyLinkAPIHandler, map[string]any{"page_id": mail, "rfq": w1}); code != 400 {
		t.Errorf("返事ページでないページを結ぼうとして %d", code)
	}

	// ③ 件名の№（見積依頼のメールへの返信）→ 1つに決まり仕入先も合う → 結ぶ。
	mail3 := newReplyMail(t, "RE: 見積依頼（№ "+w1+"）", "")
	stubReply(t, "", "わかば鋼業")
	if out3 := analyzeReply(t, mail3); out3.LinkedRFQ != w1 {
		t.Errorf("件名の№で結んでいません: %+v", out3)
	}

	// ④ 件名に№が無くても、こちらが送った見積依頼のメール（控え）への返信なら、控えの件名の№で結ぶ。
	sent, err := cms.CreateChildPage(rfqReplyBox, "root", `<h1>見積依頼（№ `+w2+`）</h1><dl data-type="tags"><dt>`+comm.DirectionTag+
		`</dt><dd>`+comm.DirectionOut+`</dd><dt>`+comm.MessageIDTag+`</dt><dd>&lt;rfq-`+w2+`@example.jp&gt;</dd></dl>`)
	if err != nil {
		t.Fatal(err)
	}
	mail4 := newReplyMail(t, "お見積りの件", "")
	if err := cms.RewriteBody(mail4, "root", func(b string) string {
		return withTagValues(b, comm.InReplyToTag, []string{"<rfq-" + w2 + "@example.jp>"}, false)
	}); err != nil {
		t.Fatal(err)
	}
	// 親子は 親ページID（2026-10-09）——控えがあとから入った返事にも、読み込みの仕事の最後に書き足される（FixRecordTags）。
	if _, linked, err := comm.FixRecordTags("root"); err != nil || linked < 1 {
		t.Fatalf("返事に親ページID を書き足せません: %d %v", linked, err)
	}
	if !strings.Contains(readPageBody(t, mail4), "<dt>"+comm.ParentPageTag+"</dt><dd>"+sent+"</dd>") {
		t.Fatalf("返事の親ページID が控えを指していません:\n%s", readPageBody(t, mail4))
	}
	stubReply(t, "", "わかば鋼業")
	if out4 := analyzeReply(t, mail4); out4.LinkedRFQ != w2 {
		t.Errorf("送った見積依頼のメールへの返信のつながりで結んでいません: %+v", out4)
	}
}

// TestRFQReplyFromMailBody は、メールの「作るページ」の「見積依頼の返事ページ」——本文を読んで（原本の枠は作らない・受信元は
// メールのページ全体）返事ページを作り、返事でなければ作らない——を固定します。
func TestRFQReplyFromMailBody(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	rfq := newRFQDoc(t, "わかば鋼業")
	mail := newReplyMail(t, "RE: 見積依頼（№ "+rfq+"）", "SS400 t6 は 1枚 1,200円 です。")
	var gotText string
	orig := judgeRFQReplyMail
	judgeRFQReplyMail = func(text string) (*rfqReplyMailJudgment, error) {
		gotText = text
		return parseRFQReplyMail(`{"is_rfq_reply":true,"rfq_no":"","supplier":"わかば鋼業","date":"2026-10-05","source_table":{"headers":["品名","単価"],"rows":[["SS400 t6","1,200円"]]}}`)
	}
	t.Cleanup(func() { judgeRFQReplyMail = orig })

	res, err := makeRFQReplyFromMail(rfqReplyRoot, mail)
	if err != nil || len(res.Pages) != 1 {
		t.Fatalf("返事ページを作れません: %+v %v", res, err)
	}
	if !strings.Contains(gotText, "SS400 t6 は 1枚 1,200円 です。") {
		t.Errorf("メールの本文を渡していません: %q", gotText)
	}
	id := res.Pages[0].PageID
	body := readPageBody(t, id)
	if !strings.Contains(body, "<dt>"+SourceRefTag+"</dt><dd>"+mail+"</dd>") || strings.Contains(body, `data-type="file-view"`) {
		t.Errorf("受信元がメールのページ全体でないか、原本の枠が残っています:\n%s", body)
	}
	if parentOf(t, id) != rfq || !strings.Contains(res.Say, "/"+rfq) {
		t.Errorf("件名の№で結んでいません: 親 %s・%q", parentOf(t, id), res.Say)
	}
	if made := rfqReplyPagesFrom(rfqReplyRoot, mail); len(made) != 1 || made[0] != id {
		t.Errorf("メールから作った返事ページが %v です", made)
	}
	if srcs := rfqReplyPageSources(rfqReplyRoot, rfq); len(srcs) != 1 || srcs[0].PageID != mail || srcs[0].File != "" {
		t.Errorf("見積依頼書の候補にメールの本文がありません: %+v", srcs)
	}
	// 返事でなければ作らない。
	judgeRFQReplyMail = func(string) (*rfqReplyMailJudgment, error) { return parseRFQReplyMail(`{"is_rfq_reply":false}`) }
	if res, err := makeRFQReplyFromMail(rfqReplyRoot, mail); err != nil || len(res.Pages) != 0 || !strings.Contains(res.Say, "ではない") {
		t.Errorf("返事でないのに作りました: %+v %v", res, err)
	}
}

// TestRFQReplyMatchViewOnlyOnReplyPages は、突き合わせの欄が返事ページにだけ出ることを固定します（見積依頼書・テンプレートには出ない）。
func TestRFQReplyMatchViewOnlyOnReplyPages(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	rfq := newRFQDoc(t, "わかば鋼業")
	if v := rfqReplyMatchViewHTML(rfqReplyRoot, pageNum(rfq)); v != "" {
		t.Errorf("見積依頼書に突き合わせの欄が出ています:\n%s", v)
	}
	if v := rfqReplyMatchViewHTML(rfqReplyRoot, 915); v != "" {
		t.Errorf("テンプレートに突き合わせの欄が出ています:\n%s", v)
	}
	// 回答日と受信元を持っても、見積依頼番号を持つページ（見積依頼書）は返事ページではない（人がタグを足した見積依頼書など）。
	if err := cms.RewriteBody(rfq, "root", func(b string) string {
		b = withTagValues(b, RFQAnsweredTag, []string{"2026-10-05"}, true)
		return withTagValues(b, SourceRefTag, []string{rfqReplyBox}, false)
	}); err != nil {
		t.Fatal(err)
	}
	if v := rfqReplyMatchViewHTML(rfqReplyRoot, pageNum(rfq)); v != "" || kindOfPage(pageNum(rfq)) == rfqReplyKind {
		t.Errorf("見積依頼番号を持つページを返事ページと見ています:\n%s", v)
	}
	_ = httptest.NewRequest
}
