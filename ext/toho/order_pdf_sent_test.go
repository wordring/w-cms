package toho

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 部材発注書の PDF——送付前は上書き・送付後は新しい版・送るのは表示中の PDF・備考の欄・納品日（2026-10-14・
// order_pdf_sent.go）の番人です。【要求】部材発注フォルダ:
//   「送付前は、このボタンを押すたびに、古いPDFを上書きして、表を基に新しいPDFが作成されます」
//   「古いPDFは消しません。送った紙は、送った事実として保存します」
//   「送るPDFは、部材発注書ページに表示している最新のPDFです（送る直前に作り直しません）」

// sentOrderBody は部材発注書ページの本文です（qty は1行目の数量・今の形——備考の節つき）。
func sentOrderBody(qty, status string) string {
	return `<h1>部材発注　みなと商店</h1><dl data-type="tags"><dt>` + PurchaseNoTag + `</dt><dd>000070</dd>` +
		`<dt>` + SupplierTag + `</dt><dd>みなと商店</dd><dt>` + PurchasedAtTag + `</dt><dd>2026-10-01</dd></dl>` +
		`<table><caption>部材発注明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>SS400</td><td>FB</td><td>t9*50*200</td><td>` + qty + `</td><td>本</td><td></td><td>` + status + `</td></tr>` +
		`</tbody></table><section><h2>` + orderNoteHeading + `</h2><p><br/></p></section>`
}

// pdfFilesOf はページの添付の PDF の保存名を返します（目録は数えない）。
func pdfFilesOf(t *testing.T, pageID string) []string {
	t.Helper()
	entries, _ := os.ReadDir(page.AttachmentDir(pageID))
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".pdf") {
			out = append(out, e.Name())
		}
	}
	return out
}

// pdfTextOfFile は添付の PDF から文字を読み返します。
func pdfTextOfFile(t *testing.T, pageID, file string) string {
	t.Helper()
	path, ok := page.AttachmentPath(pageID, file)
	if !ok {
		t.Fatalf("添付 %s がありません", file)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return pdfTextOf(t, b)
}

// setQty は本文の1行目の数量を書き換えます（表示中の PDF のマーカーは残す）。
func setQty(t *testing.T, id int, from, to string) {
	t.Helper()
	b := mustBody(t, page.FormatID(id))
	nb := strings.Replace(b, `<td>`+from+`</td><td>本</td>`, `<td>`+to+`</td><td>本</td>`, 1)
	if nb == b {
		t.Fatalf("数量 %s が本文にありません:\n%s", from, b)
	}
	writeBodyFile(t, id, nb)
}

// TestOrderPDFOverwritesUntilSent は、送る前の「PDF作成」は表示中の PDF を上書きし（添付は増えない・中身は新しい表）、
// 送ったあとは新しい版を作って表示を差し替え、送った PDF は消さずに記録の節にリンクで残ること・次の版は
// また上書きされることを固定します。
func TestOrderPDFOverwritesUntilSent(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "部材発注　みなと商店", "root", "302", true)
	writeBodyFile(t, 70, sentOrderBody("7", OrderLineUnsent))
	root := &auth.User{Username: "root"}
	makePDF := func() map[string]any {
		t.Helper()
		code, out := postRFQ(t, root, OrderPDFAPIHandler, map[string]any{"page_id": "000070"})
		if code != 200 || out["success"] != true {
			t.Fatalf("PDFを作れません: %d %v", code, out)
		}
		return out
	}

	first := makePDF()
	file1, _ := first["file"].(string)
	if first["overwrote"] != nil {
		t.Errorf("1枚目なのに上書きと言っています: %v", first)
	}
	// 送る前に表を直して押し直す → 同じ添付を上書き（増えない・中身は新しい数量）。
	setQty(t, 70, "7", "8")
	second := makePDF()
	if second["file"] != file1 || second["overwrote"] != true {
		t.Errorf("送る前なのに上書きしていません: 1枚目 %s・2回目 %v", file1, second)
	}
	if files := pdfFilesOf(t, "000070"); len(files) != 1 {
		t.Errorf("送る前の作り直しで添付が増えました: %v", files)
	}
	if got := pdfTextOfFile(t, "000070", file1); !strings.Contains(got, "8") || strings.Contains(got, "7本") {
		t.Errorf("上書きした PDF が新しい表を刷っていません:\n%s", got)
	}
	if m := cms.ReadAttachmentMetas("000070")[file1]; !regexp.MustCompile(` \d{8}-\d{6}\.pdf$`).MatchString(m.Name) {
		t.Errorf("上書きした添付の名前に日時がありません: %q", m.Name)
	}

	// FAX で送った → 行は発注済・記録の節に表示中の PDF のリンク。
	if code, out := postRFQ(t, root, OrderSentAPIHandler, map[string]any{"page_id": "000070", "method": sendByFax}); code != 200 {
		t.Fatalf("送ったの印を付けられません: %d %v", code, out)
	}
	b := mustBody(t, "000070")
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(b, "<h2>"+orderSentHeading+"</h2>") || !strings.Contains(b, `href="/000070/`+file1+`"`) ||
		!strings.Contains(b, today+"　"+sendByFax) {
		t.Fatalf("送った PDF が記録されていません:\n%s", b)
	}
	// 記録の節は表示中の PDF のすぐ下（部材発注明細の表 → PDF → 記録）。
	if iv, ir := strings.Index(b, `data-type="`+cms.FileViewType+`"`), strings.Index(b, orderSentHeading); iv < 0 || ir < iv ||
		strings.Index(b, "</table>") > iv {
		t.Errorf("記録の節の置き場所が違います:\n%s", b)
	}

	// 送ったあとに押す → 新しい版（送った PDF は残る・表示は新しい版）。
	setQty(t, 70, "8", "9")
	third := makePDF()
	file2, _ := third["file"].(string)
	if file2 == file1 || third["overwrote"] != nil {
		t.Fatalf("送ったあとなのに送った PDF を上書きしました: %v", third)
	}
	if files := pdfFilesOf(t, "000070"); len(files) != 2 {
		t.Errorf("送った PDF が残っていません: %v", files)
	}
	if got := pdfTextOfFile(t, "000070", file1); !strings.Contains(got, "8") {
		t.Errorf("送った PDF の中身が変わりました:\n%s", got)
	}
	b = mustBody(t, "000070")
	if !strings.Contains(b, `data-ref="000070-`+attachIDOf(file2)+`"`) || !strings.Contains(b, `href="/000070/`+file1+`"`) {
		t.Errorf("表示が新しい版になっていないか、送った PDF のリンクが消えました:\n%s", b)
	}
	// 新しい版はまだ送っていない → 次はそれを上書き。
	fourth := makePDF()
	if fourth["file"] != file2 || fourth["overwrote"] != true || len(pdfFilesOf(t, "000070")) != 2 {
		t.Errorf("再送の前の作り直しで、まだ送っていない版を上書きしていません: %v %v", fourth, pdfFilesOf(t, "000070"))
	}
}

// TestOrderPDFOnOldPages は、記録の節が無い（2026-10-14 より前の）部材発注書ページを壊さずに扱うことを固定します——
// 行が出ていれば表示中の PDF を送ったものとみなして新しい版を作り（上書きしない）、出ていなければ上書きする。
// 古い形（属性で名乗る表・タグの備考・未納品）も読めること。
func TestOrderPDFOnOldPages(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	root := &auth.User{Username: "root"}
	cases := []struct {
		id        int
		status    string
		overwrite bool
	}{
		{71, orderLineLegacySent, false}, // 09-22 より前の「未納品」＝出した
		{72, OrderLineSent, false},
		{73, OrderLineDelivered, false},
		{74, OrderLineUnsent, true},
	}
	for _, c := range cases {
		pid := page.FormatID(c.id)
		addPage(t, c.id, 0, "部材発注", "root", "302", true)
		old := []byte("%PDF-1.4 old paper")
		putAttachment(t, pid, "old1.pdf", old)
		body := pdfOrderBody(`<tr><td></td><td></td><td></td><td>SS400</td><td>FB</td><td>t9*50*200</td>` +
			`<td></td><td>3</td><td>本</td><td></td><td></td><td>` + c.status + `</td></tr>`)
		body = strings.Replace(body, `<table`, `<section data-type="`+cms.FileViewType+`" data-ref="`+pid+`-old1"></section><table`, 1)
		writeBodyFile(t, c.id, body)
		code, out := postRFQ(t, root, OrderPDFAPIHandler, map[string]any{"page_id": pid})
		if code != 200 {
			t.Errorf("%s: 古い形の部材発注書で PDF を作れません: %d %v", c.status, code, out)
			continue
		}
		path, _ := page.AttachmentPath(pid, "old1.pdf")
		kept, _ := os.ReadFile(path)
		if c.overwrite {
			if out["file"] != "old1.pdf" || string(kept) == string(old) {
				t.Errorf("%s: まだ出していないのに上書きしていません: %v", c.status, out)
			}
			continue
		}
		if out["file"] == "old1.pdf" || string(kept) != string(old) {
			t.Errorf("%s: 出した部材発注書の PDF を上書きしました: %v", c.status, out)
		}
		if b := mustBody(t, pid); strings.Contains(b, `data-ref="`+pid+`-old1"`) {
			t.Errorf("%s: 表示が新しい版になっていません:\n%s", c.status, b)
		}
		// 古い形の備考（タグ）は紙に刷られ、欄にも出る。
		if got := pdfTextOfFile(t, pid, out["file"].(string)); !strings.Contains(got, "急ぎでお願いします") {
			t.Errorf("%s: タグの備考が紙にありません:\n%s", c.status, got)
		}
		if n := orderNoteOf(mustBody(t, pid)); n != "急ぎでお願いします" {
			t.Errorf("%s: 欄に出す備考 = %q", c.status, n)
		}
	}
}

// orderMailRequest は送る欄の口を真似た要求です。
func orderMailRequest(u *auth.User) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest("POST", "/api/mail/send", nil)
	return httptest.NewRecorder(), auth.WithUser(req, u)
}

// TestOrderMailSendsTheShownPDF は、メールが表示中の PDF を添え（作り直さない・添付が増えない）、まだ PDF が無い
// ときだけ作ること・送る欄の一言がそれに合うことを固定します。
func TestOrderMailSendsTheShownPDF(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "部材発注　みなと商店", "root", "302", true)
	writeBodyFile(t, 70, sentOrderBody("7", OrderLineUnsent))
	root := &auth.User{Username: "root"}

	// まだ PDF が無い → 送るときに作る（表示もする）。
	d, err := orderMailDefaults(root, "000070")
	if err != nil {
		t.Fatal(err)
	}
	if d.Generated != "発注書 000070.pdf" || d.GeneratedNote != "" {
		t.Errorf("PDF が無いのに表示中の PDF を添えると言っています: %+v", d)
	}
	w, r := orderMailRequest(root)
	atts, ok := prepareOrderMail(w, r, "000070")
	if !ok || len(atts) != 1 {
		t.Fatalf("PDF を作れません: %d %s", w.Code, w.Body.String())
	}
	made := atts[0].File
	if !strings.Contains(mustBody(t, "000070"), `data-ref="000070-`+attachIDOf(made)+`"`) {
		t.Errorf("送るときに作った PDF をページに表示していません")
	}

	// 表示中の PDF がある → それを添える（作り直さない）。
	setQty(t, 70, "7", "8")
	if d, _ = orderMailDefaults(root, "000070"); d.GeneratedNote == "" || !strings.Contains(d.SendNote, "作り直しません") {
		t.Errorf("送る欄が表示中の PDF を添えると言っていません: %+v", d)
	}
	w, r = orderMailRequest(root)
	atts, ok = prepareOrderMail(w, r, "000070")
	if !ok || len(atts) != 1 || atts[0].File != made || atts[0].PageID != "000070" || atts[0].Name != "発注書 000070.pdf" {
		t.Fatalf("表示中の PDF を添えていません: %+v（表示中は %s）", atts, made)
	}
	if files := pdfFilesOf(t, "000070"); len(files) != 1 {
		t.Errorf("送るときに PDF を作り直しました: %v", files)
	}
	if got := pdfTextOfFile(t, "000070", made); strings.Contains(got, "8") {
		t.Errorf("送る PDF の中身が変わりました（作り直した）:\n%s", got)
	}
	// 書けない人は送る前に断る（メールは出ない）。
	w, r = orderMailRequest(&auth.User{Username: "bob"})
	if _, ok := prepareOrderMail(w, r, "000070"); ok || w.Code != http.StatusForbidden {
		t.Errorf("書けない人なのに添えました: %d", w.Code)
	}
}

// TestAfterOrderMailRecordsTheMailedPDF は、メールで送ったあと、控えが添えたこのページの PDF を記録の節に書き
// （控えへのリンクつき）、行を発注済みにすること・添えずに送ったらそう書いて、表示中の PDF を送っていない扱いに
// 残すことを固定します。
func TestAfterOrderMailRecordsTheMailedPDF(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "部材発注　みなと商店", "root", "302", true)
	addPage(t, 80, 0, "控え", "root", "302", true)
	addPage(t, 81, 0, "控え2", "root", "302", true)
	root := &auth.User{Username: "root"}
	putAttachment(t, "000070", "pa01.pdf", []byte("%PDF-1.4 a"))
	body := strings.Replace(sentOrderBody("7", OrderLineUnsent), `<section><h2>`+orderNoteHeading,
		`<section data-type="`+cms.FileViewType+`" data-ref="000070-pa01"></section><section><h2>`+orderNoteHeading, 1)
	writeBodyFile(t, 70, body)
	// 添えずに送った控え（ほかのページのファイルだけ）。
	writeBodyFile(t, 81, `<h1>控え2</h1><section><h2>添付ファイル</h2><p>📎 <a href="/000031/zz01.pdf" download="図面.pdf">図面.pdf</a></p></section>`)
	if err := afterOrderMail(root, "000070", "000081"); err != nil {
		t.Fatal(err)
	}
	b := mustBody(t, "000070")
	if !strings.Contains(b, "部材発注書の PDF は添えていません") || !strings.Contains(b, `href="/000081"`) {
		t.Errorf("添えずに送ったことを記録していません:\n%s", b)
	}
	if shownPDFWasSent(b, "000070", "pa01") {
		t.Errorf("添えていない PDF を送ったことにしています")
	}
	// 添えて送った控え。
	writeBodyFile(t, 80, `<h1>控え</h1><section><h2>添付ファイル</h2><p>📎 <a href="/000070/pa01.pdf" download="発注書 000070.pdf">部材発注書 000070.pdf</a></p></section>`)
	if err := afterOrderMail(root, "000070", "000080"); err != nil {
		t.Fatal(err)
	}
	b = mustBody(t, "000070")
	if !strings.Contains(b, `href="/000070/pa01.pdf"`) || !strings.Contains(b, `href="/000080"`) || !shownPDFWasSent(b, "000070", "pa01") {
		t.Errorf("送った PDF を記録していません:\n%s", b)
	}
	if strings.Count(b, "<h2>"+orderSentHeading+"</h2>") != 1 || strings.Count(b, sendByMail+"（控え") != 2 {
		t.Errorf("記録の節が1つに2行になっていません:\n%s", b)
	}
	if !strings.Contains(b, "<td>"+OrderLineSent+"</td>") {
		t.Errorf("行が発注済みになっていません:\n%s", b)
	}
	// 記録の行はサニタイズを通っても残る（リンクが消えると、次の「PDF作成」が送った PDF を上書きする）。
	if clean := cms.Sanitize(b); !shownPDFWasSent(clean, "000070", "pa01") {
		t.Errorf("サニタイズで記録のリンクが落ちます:\n%s", clean)
	}
}

// TestOrderNoteBox は、部材発注明細の足元に備考の欄（見積書と同じ部品）とその下の「PDF作成」が書ける人にだけ出て、
// 欄の口が本文の「備考」の節を書き換え、紙がそれを刷ることを固定します。
func TestOrderNoteBox(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "部材発注　みなと商店", "root", "302", true)
	addPage(t, 61, 0, "見積", "root", "302", true)
	writeBodyFile(t, 70, sentOrderBody("7", OrderLineUnsent))
	writeBodyFile(t, 61, estimatePaperBody())
	root := &auth.User{Username: "root", IsAdmin: true}

	got := showPage(t, root, 70, mustBody(t, "000070"))
	box, pdf, ways := strings.Index(got, `data-note-url="/api/our-order/note"`), strings.Index(got, `data-order-pdf="1"`), strings.Index(got, `data-order-sent="`)
	if box < 0 || pdf < 0 || !strings.Contains(got, "📄 PDF作成") || !(box < pdf && pdf < ways) {
		t.Errorf("備考の欄 → PDF作成 → 送る、の並びになっていません（%d %d %d）:\n%s", box, pdf, ways, got)
	}
	if bob := showPage(t, &auth.User{Username: "bob"}, 70, mustBody(t, "000070")); strings.Contains(bob, "estimate-note-input") {
		t.Errorf("書けない人に備考の欄が出ています")
	}

	code, out := postRFQ(t, root, OrderNoteAPIHandler, map[string]any{"page_id": "000070", "note": "訂正版です\n午前中に納品してください"})
	if code != 200 || out["success"] != true {
		t.Fatalf("備考を保存できません: %d %v", code, out)
	}
	b := mustBody(t, "000070")
	if n := orderNoteOf(b); n != "訂正版です\n午前中に納品してください" {
		t.Errorf("備考の節 = %q", n)
	}
	if got := showPage(t, root, 70, b); !strings.Contains(got, "午前中に納品してください</textarea>") {
		t.Errorf("欄にいまの備考が入っていません:\n%s", got)
	}
	code, out = postRFQ(t, root, OrderPDFAPIHandler, map[string]any{"page_id": "000070"})
	if code != 200 {
		t.Fatalf("PDFを作れません: %d %v", code, out)
	}
	if txt := pdfTextOfFile(t, "000070", out["file"].(string)); !strings.Contains(txt, "備考：") || !strings.Contains(txt, "午前中に納品してください") {
		t.Errorf("備考が紙に刷られていません:\n%s", txt)
	}
	// 部材発注書でないページ・書けない人は断る。
	if code, _ := postRFQ(t, root, OrderNoteAPIHandler, map[string]any{"page_id": "000061", "note": "x"}); code != http.StatusBadRequest {
		t.Errorf("見積書ページに書きました: %d", code)
	}
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, OrderNoteAPIHandler, map[string]any{"page_id": "000070", "note": "x"}); code != http.StatusForbidden {
		t.Errorf("書けない人が書きました: %d", code)
	}
}

// TestBuildOurOrderPlacesDeliveredAt は、部材発注書ページを作るとき `納品日` のタグを空の欄で置き（テンプレートに無くても）、
// 紙には刷らないことを固定します。
func TestBuildOurOrderPlacesDeliveredAt(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	body, err := buildOurOrderHTML(testPurchaseOrderTemplate(), "000070", "みなと商店", "2026-10-14", "", "", "",
		[]ourOrderLine{{ProductID: "000031", Material: "SS400", Shape: "FB", Size: "t9*50*200", Quantity: "2", Unit: "本"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `<dt>`+DeliveredAtTag+`</dt><dd><br/></dd>`) {
		t.Errorf("納品日の空の欄がありません:\n%s", body)
	}
	pdf, err := buildOrderPDF(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if txt := pdfTextOf(t, pdf); strings.Contains(txt, DeliveredAtTag) {
		t.Errorf("納品日を紙に刷っています:\n%s", txt)
	}
	// テンプレートが欄を持っていれば、増やさない。
	tmpl := strings.Replace(testPurchaseOrderTemplate(), `</dl>`, `<dt>`+DeliveredAtTag+`</dt><dd><br/></dd></dl>`, 1)
	body, _ = buildOurOrderHTML(tmpl, "000070", "みなと商店", "2026-10-14", "", "", "",
		[]ourOrderLine{{ProductID: "000031", Quantity: "2"}})
	if n := strings.Count(body, `<dt>`+DeliveredAtTag+`</dt>`); n != 1 {
		t.Errorf("納品日の欄が %d 個あります", n)
	}
}

// TestEstimateAndRFQStillMakeAtSend は、共通の部品を直しても見積書・部材見積依頼書の送り方は変わらないこと——「PDFを作る」は
// 押すたびに新しい添付を足し（上書きしない）、メールは送るときに作り直して添え、送る欄の一言も「送るときに作ります」の
// まま——を固定します。
func TestEstimateAndRFQStillMakeAtSend(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 61, 0, "見積", "root", "302", true)
	addPage(t, 62, 0, "部材見積依頼", "root", "302", true)
	writeBodyFile(t, 61, estimatePaperBody())
	writeBodyFile(t, 62, rfqDocsBody(rfqLineUnanswered))
	root := &auth.User{Username: "root"}
	for _, c := range []struct {
		pid     string
		handler http.HandlerFunc
	}{{"000061", EstimatePDFAPIHandler}, {"000062", RFQPDFAPIHandler}} {
		for i := 0; i < 2; i++ {
			if code, out := postRFQ(t, root, c.handler, map[string]any{"page_id": c.pid}); code != 200 || out["overwrote"] != nil {
				t.Fatalf("%s: %d %v", c.pid, code, out)
			}
		}
		if files := pdfFilesOf(t, c.pid); len(files) != 2 {
			t.Errorf("%s: 押すたびに新しい添付を足すはず: %v", c.pid, files)
		}
	}
	w, r := orderMailRequest(root)
	if atts, ok := prepareEstimateMail(w, r, "000061"); !ok || len(atts) != 1 || len(pdfFilesOf(t, "000061")) != 3 {
		t.Errorf("見積書のメールが送るときに作り直していません: %v %v", atts, pdfFilesOf(t, "000061"))
	}
	w, r = orderMailRequest(root)
	if atts, ok := prepareRFQMail(w, r, "000062"); !ok || len(atts) != 1 || len(pdfFilesOf(t, "000062")) != 3 {
		t.Errorf("部材見積依頼書のメールが送るときに作り直していません: %v %v", atts, pdfFilesOf(t, "000062"))
	}
	if d, err := estimateMailDefaults(root, "000061"); err != nil || d.GeneratedNote != "" {
		t.Errorf("見積書の送る欄の一言が変わりました: %+v %v", d, err)
	}
	if d, err := rfqMailDefaults(root, "000062"); err != nil || d.GeneratedNote != "" {
		t.Errorf("部材見積依頼書の送る欄の一言が変わりました: %+v %v", d, err)
	}
}
