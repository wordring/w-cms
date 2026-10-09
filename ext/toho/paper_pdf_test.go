package toho

import (
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/signintech/gopdf"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 紙の PDF を作る口——発注書（/api/order-pdf）・見積書（/api/estimate-pdf）・見積依頼書（/api/rfq-pdf）と、資料を綴じた発注書
// （/api/order-pdf-docs）——の振る舞いを固定します（2026-10-09 に、口が写していた段取りを paper_pdf.go へ寄せたときの番人）。

// estimatePaperBody は見積書ページの本文です（TestEstimatePDFRoundTrips と同じ見本）。
func estimatePaperBody() string {
	return `<h1>見積　みなと商店</h1>` +
		`<dl data-type="tags"><dt>見積番号</dt><dd>000300</dd><dt>見積先</dt><dd>みなと商店</dd><dt>見積先担当</dt><dd>山川</dd>` +
		`<dt>見積日</dt><dd>2026-10-01</dd><dt>受渡期日</dt><dd>受注後2週間</dd></dl>` +
		`<table><caption>見積明細</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>` +
		`<tr><td>000080</td><td>A100-B01-001</td><td>取付ブラケット</td><td>20</td><td>個</td><td>402</td><td>塗装無し</td></tr>` +
		`</tbody></table><section><h2>備考</h2><p>レーザー加工穴でのお見積もりとなります。</p></section>`
}

// TestPaperPDFHandlers は、3つの口が PDF を作ってそのページの添付に残し（名前は紙の題・ページ番号・日時）、
// 明細の表の下にファイル表示を置いて開くURLを返すこと・フォントが無ければ 503・書けない人は 403 を固定します。
func TestPaperPDFHandlers(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	root := &auth.User{Username: "root"}
	orderBody := pdfOrderBody(`<tr><td></td><td></td><td></td><td>鉄STPG370EG</td><td>φ27.2</td><td>t3.4*定尺</td>` +
		`<td></td><td>10</td><td>個</td><td>3200</td><td></td><td>未発注</td></tr>`)
	cases := []struct {
		name    string
		id      int
		handler http.HandlerFunc
		body    string
		prefix  string
	}{
		{"発注書", 60, OrderPDFAPIHandler, orderBody, orderPaperTitleOf(orderBody) + " "},
		{"見積書", 61, EstimatePDFAPIHandler, estimatePaperBody(), "御見積書 000061 "},
		{"見積依頼書", 62, RFQPDFAPIHandler, rfqDocsBody(rfqLineUnanswered), "見積依頼書 000062 "},
	}
	for _, c := range cases {
		pid := page.FormatID(c.id)
		addPage(t, c.id, 0, c.name, "root", "302", true)
		writeBodyFile(t, c.id, c.body)
		code, out := postRFQ(t, root, c.handler, map[string]any{"page_id": pid})
		if code != 200 || out["success"] != true {
			t.Errorf("%s: 作れません %d %v", c.name, code, out)
			continue
		}
		file, _ := out["file"].(string)
		att, _ := out["attach_id"].(string)
		if out["url"] != "/"+pid+"/"+file || att == "" || out["view_note"] != nil {
			t.Errorf("%s: 応答が違います %v", c.name, out)
		}
		if _, ok := page.AttachmentPath(pid, file); !ok {
			t.Errorf("%s: 添付に残っていません %v", c.name, out)
		}
		metas := cms.ReadAttachmentMetas(pid)
		if m, ok := metas[file]; !ok || !strings.HasPrefix(m.Name, c.prefix) || !regexp.MustCompile(` \d{8}-\d{6}\.pdf$`).MatchString(m.Name) {
			t.Errorf("%s: 添付の名前 = %q（%q＋日時.pdf のはず）", c.name, metas[file].Name, c.prefix)
		}
		if b, _ := cms.ReadPageBody(pid); !strings.Contains(b, `data-ref="`+pid+"-"+att+`"`) {
			t.Errorf("%s: ページに表示していません:\n%s", c.name, b)
		}
		// 書けない人は 403（作らない）。
		if code, _ := postRFQ(t, &auth.User{Username: "bob"}, c.handler, map[string]any{"page_id": pid}); code != http.StatusForbidden {
			t.Errorf("%s: 書けない人に %d（403 のはず）", c.name, code)
		}
	}
	// フォントが無ければ 503（作らない）。
	withPDFFont(t, "")
	for _, c := range cases {
		if code, out := postRFQ(t, root, c.handler, map[string]any{"page_id": page.FormatID(c.id)}); code != http.StatusServiceUnavailable {
			t.Errorf("%s: フォントが無いのに %d %v（503 のはず）", c.name, code, out)
		}
	}
	withPDFFont(t, `C:\無い\フォント.ttf`)
	if code, _ := postRFQ(t, root, OrderPDFAPIHandler, map[string]any{"page_id": "000060"}); code != http.StatusServiceUnavailable {
		t.Errorf("フォントのファイルが無いのに %d（503 のはず）", code)
	}
}

// TestOrderPDFDocsHandler は、発注書のうしろに外注加工の資料を綴じた1本を作って添付に残し、綴じなかったものを言うこと・
// 資料が無ければ 400 を固定します（見積依頼書の同じ口は rfq_pdf_docs_test.go）。
func TestOrderPDFDocsHandler(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	seedOrderDocs(t)
	addPage(t, 50, 0, "発注 ひかりレーザー", "root", "302", true)
	root := &auth.User{Username: "root"}
	put := func(no string) {
		writeBodyFile(t, 50, `<h1>発注</h1><dl data-type="tags"><dt>発注書番号</dt><dd>45</dd><dt>`+SupplierTag+`</dt><dd>ひかりレーザー</dd></dl>`+
			`<table data-type="`+ourOrderItemsType+`"><caption>発注明細</caption><tbody>`+
			`<tr><th>弊社品番</th><th>種類</th><th>番号</th><th>加工内容</th><th>数量</th><th>単位</th><th>状態</th></tr>`+
			`<tr><td>000031</td><td>外注加工</td><td>`+no+`</td><td>レーザー切断</td><td>1</td><td>個</td><td>未発注</td></tr>`+
			`</tbody></table>`)
	}
	put("1")
	code, out := postRFQ(t, root, OrderPDFDocsAPIHandler, map[string]any{"page_id": "000050"})
	if code != 200 || out["success"] != true {
		t.Fatalf("綴じた1本を作れません: %d %v", code, out)
	}
	file, _ := out["file"].(string)
	path, ok := page.AttachmentPath("000050", file)
	if !ok || out["url"] != "/000050/"+file {
		t.Fatalf("添付に残っていないか、URL が違います: %v", out)
	}
	if bound, err := os.ReadFile(path); err != nil || len(regexp.MustCompile(`/Type /Page\s`).FindAll(bound, -1)) != 4 {
		t.Errorf("綴じたページの数が違います（発注書1＋図面2＋写真1 のはず）: %v", err)
	}
	if m := cms.ReadAttachmentMetas("000050")[file]; !strings.Contains(m.Name, "＋資料 ") {
		t.Errorf("添付の名前 = %q", m.Name)
	}
	if s, _ := out["skipped"].([]any); len(s) == 0 {
		t.Errorf("綴じなかったもの（.dxf・読めない図面）を言っていません: %v", out)
	}
	put("3") // 資料の無い番号
	if code, _ := postRFQ(t, root, OrderPDFDocsAPIHandler, map[string]any{"page_id": "000050"}); code != http.StatusBadRequest {
		t.Errorf("資料が無いのに %d（400 のはず）", code)
	}
}

// TestPaperSteps は紙の共通の段取りを固定します——差出人の行が左より下まで来たらその下から続ける（pdfSender）・備考は
// 「見出し：」を刷り、長い行は紙の幅で折り、紙の下に来たら改ページする（pdfNote）。
func TestPaperSteps(t *testing.T) {
	font := systemJPFont(t)
	start := func() *gopdf.GoPdf {
		p, err := startPaper(font)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := start()
	if got := pdfSender(p, pdfTop, pdfTop+10, []string{"東邦", "担当 山田", "TEL 000"}); got <= pdfTop+10 {
		t.Errorf("差出人が3行あるのに、明細を左の y（%v）から始めます: %v", pdfTop+10, got)
	}
	if got := pdfSender(p, pdfTop, 700, []string{"東邦"}); got != 700 {
		t.Errorf("左の方が下なのに %v（700 のはず）", got)
	}
	short := pdfNote(start(), pdfTop, "備考", []string{"短い"})
	if long := pdfNote(start(), pdfTop, "備考", []string{strings.Repeat("あ", 200)}); long <= short {
		t.Errorf("紙の幅を超える行を折っていません（%v・1行なら %v）", long, short)
	}
	p = start()
	lines := []string{}
	for i := 0; i < 80; i++ {
		lines = append(lines, "行"+strconv.Itoa(i))
	}
	pdfNote(p, pdfTop, "備考", lines)
	pdf, err := finishPaper(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(regexp.MustCompile(`/Type /Page\s`).FindAll(pdf, -1)); n < 2 {
		t.Errorf("80行の備考が %d 枚に収まっています（紙の下で改ページするはず）", n)
	}
	if got := pdfTextOf(t, pdf); !strings.Contains(got, "備考：") || !strings.Contains(got, "行79") {
		t.Errorf("備考の見出しか最後の行がありません:\n%s", got)
	}
}
