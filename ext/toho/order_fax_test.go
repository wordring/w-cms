package toho

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// w-cms の FAX で送れたあとの後始末（2026-10-25 夜・ext/comm/fax_send.go の AfterFaxed）の番人です。
// 利用者:「部材発注書・部材見積依頼書・見積書の「送る」から FAX で送れるようにしてください。送れたときだけ送付日を書いてください。」
// 送れなかったら呼ばれないことは ext/comm の TestFaxSendPurposeAfterFaxed が見る。

// TestFaxablePurposes は、三つの用件が FAX で送れる（AfterFaxed がある）ことを固定します——無いと送る欄のボタンが断られる。
func TestFaxablePurposes(t *testing.T) {
	for _, name := range []string{OrderMailPurpose, RFQMailPurpose, EstimateMailPurpose} {
		if p, ok := comm.SendPurposeOf(name); !ok || p.AfterFaxed == nil || p.Prepare == nil {
			t.Errorf("用件「%s」が FAX で送れません", name)
		}
	}
}

// TestAfterOrderFaxedRecordsTheFaxedPDF は、FAX で送れたあと、送ったこのページの PDF を記録の節に「FAX」で書き、
// 行を発注済みにすることを固定します。
func TestAfterOrderFaxedRecordsTheFaxedPDF(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "部材発注　みなと商店", "root", "302", true)
	addPage(t, 90, 0, "FAX の記録", "root", "302", true)
	putAttachment(t, "000070", "pa01.pdf", []byte("%PDF-1.4 a"))
	writeBodyFile(t, 70, strings.Replace(sentOrderBody("7", OrderLineUnsent), `<section><h2>`+orderNoteHeading,
		`<section data-type="`+cms.FileViewType+`" data-ref="000070-pa01"></section><section><h2>`+orderNoteHeading, 1))
	if err := afterOrderFaxed(&auth.User{Username: "root"}, "000070", "000090", "pa01.pdf"); err != nil {
		t.Fatal(err)
	}
	b := mustBody(t, "000070")
	if !strings.Contains(b, sendByFax) || !strings.Contains(b, `href="/000070/pa01.pdf"`) || !shownPDFWasSent(b, "000070", "pa01") {
		t.Errorf("FAX で送った PDF を記録していません:\n%s", b)
	}
	if !strings.Contains(b, "<td>"+OrderLineSent+"</td>") {
		t.Errorf("行が発注済みになっていません:\n%s", b)
	}
}

// TestEstimateFaxedWritesSentDay は、見積書を FAX で送れたあと、送付日に今日を書くことを固定します（部材見積依頼書も同じ markSent）。
func TestEstimateFaxedWritesSentDay(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 61, 0, "見積", "root", "302", true)
	writeBodyFile(t, 61, estimatePaperBody())
	p, _ := comm.SendPurposeOf(EstimateMailPurpose)
	if err := p.AfterFaxed(&auth.User{Username: "root"}, "000061", "000090", "x.pdf"); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	if b := mustBody(t, "000061"); !regexp.MustCompile(`<dt>` + EstimateSentTag + `</dt>\s*<dd>` + today + `</dd>`).MatchString(b) {
		t.Errorf("送付日に今日（%s）が入っていません:\n%s", today, b)
	}
}

// TestFaxPaperBindsDocs は、FAX で送る紙（FaxPrepare）が、外注加工の資料のうち紙にできるものを部材見積依頼書のうしろに綴じた
// 1本を送り、送ったと記録するのは綴じる前の紙であること・綴じる資料が無ければ紙だけを送ることを固定します（FAX は1本の紙・
// fax_paper.go）。下ごしらえは TestRFQPDFDocsBindsDrawings と同じ。
func TestFaxPaperBindsDocs(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	seedOrderDocs(t)
	addPage(t, 50, 0, "部材見積依頼　ひかりレーザー", "root", "302", true)
	root := &auth.User{Username: "root"}
	writeBodyFile(t, 50, rfqDocsBody(rfqLineUnanswered))
	pagesOf := func(file string) int {
		path, ok := page.AttachmentPath("000050", file)
		if !ok {
			t.Fatalf("添付 %s がありません", file)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return len(regexp.MustCompile(`/Type /Page\s`).FindAll(b, -1))
	}
	prep := faxPaperWithDocs(prepareRFQMail, rfqBoundKind)
	w, r := orderMailRequest(root)
	paper, send, ok := prep(w, r, "000050")
	if !ok {
		t.Fatalf("紙を用意できません: %d %s", w.Code, w.Body.String())
	}
	if paper.File == send.File || pagesOf(paper.File) != 1 || pagesOf(send.File) != 4 { // 依頼書1 ＋ 図面2 ＋ 写真1
		t.Errorf("資料を綴じた1本を送っていません: paper=%+v send=%+v", paper, send)
	}
	if !strings.HasSuffix(send.Name, "＋資料.pdf") {
		t.Errorf("送る1本の名前: %q", send.Name)
	}
	// 行の番号が資料を指さない——綴じる資料が無いので、紙だけを送る。
	writeBodyFile(t, 50, strings.Replace(rfqDocsBody(rfqLineUnanswered), "<td>外注加工</td><td>1</td>", "<td>外注加工</td><td>9</td>", 1))
	w, r = orderMailRequest(root)
	paper, send, ok = prep(w, r, "000050")
	if !ok || paper != send {
		t.Errorf("綴じる資料が無いのに紙と違うものを送ります: %v %+v %+v %s", ok, paper, send, w.Body.String())
	}
}
