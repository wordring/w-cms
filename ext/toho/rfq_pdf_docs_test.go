package toho

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 見積依頼書の FAX・印刷用（資料を綴じる）（2026-10-05・rfq_pdf.go の RFQPDFDocsAPIHandler）。
// 【要求】見積依頼 §1・§2「外注加工の見積依頼には図面を綴じる」。下ごしらえは発注書の資料の試験と同じ（seedOrderDocs——
// 加工製品 31 に「資料 1」・通信記録 40 に A3 横2ページの図面・41 は持ち主だけが読める図面）。

// rfqDocsBody は見積依頼書ページの本文です（外注加工の行が加工製品 31 の番号 1 を指す）。
func rfqDocsBody(state string) string {
	return `<h1>見積依頼　ひかりレーザー</h1><dl data-type="tags"><dt>` + RFQNoTag + `</dt><dd>000050</dd><dt>` + SupplierTag +
		`</dt><dd>ひかりレーザー</dd><dt>` + RFQDateTag + `</dt><dd>2026-10-05</dd></dl>` +
		`<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>番号</th><th>加工内容</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>外注加工</td><td>1</td><td>レーザー切断</td><td>10</td><td>個</td><td></td><td>` + state + `</td></tr>` +
		`</tbody></table><section><h2>備考</h2><p>標準2輪用</p></section>`
}

// TestRFQPDFDocsBindsDrawings は、見積依頼書のうしろに外注加工の資料（図面の PDF を元の大きさで・画像）を綴じた1本を作り、
// そのページの添付に残すこと・綴じなかったもの（紙にできない形式・読めない図面）を言うこと・ボタンは綴じられる資料が
// あるときだけ出ること・辞退の行の資料は綴じないこと・見積依頼書でないページでは作らないことを固定します。
func TestRFQPDFDocsBindsDrawings(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	seedOrderDocs(t)
	addPage(t, 50, 0, "見積依頼　ひかりレーザー", "root", "302", true)
	root := &auth.User{Username: "root"} // 持ち主（管理者ではない——41 の図面は読めない）
	writeBodyFile(t, 50, rfqDocsBody(rfqLineUnanswered))

	if v := rfqSendViewHTML(root, 50); !strings.Contains(v, `class="chip-btn rfq-pdf-docs-go"`) {
		t.Errorf("綴じられる資料があるのに「📠 FAX・印刷用（資料を綴じる）」が出ていません:\n%s", v)
	}
	code, out := postRFQ(t, root, RFQPDFDocsAPIHandler, map[string]any{"page_id": "000050"})
	if code != 200 || out["success"] != true {
		t.Fatalf("綴じた1本を作れません: %d %v", code, out)
	}
	file, _ := out["file"].(string)
	path, ok := page.AttachmentPath("000050", file)
	if !ok {
		t.Fatalf("添付に残っていません: %v", out)
	}
	bound, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if pages := len(regexp.MustCompile(`/Type /Page\s`).FindAll(bound, -1)); pages != 4 { // 見積依頼書1 ＋ 図面2（A3 横）＋ 写真1
		t.Errorf("ページが %d 枚です（4枚のはず）", pages)
	}
	if !regexp.MustCompile(`/MediaBox \[\s*0 0 1190\.55`).Match(bound) {
		t.Errorf("⚠ 図面が A3 横の大きさで綴じられていません")
	}
	var skipped []string
	for _, s := range out["skipped"].([]any) {
		skipped = append(skipped, s.(string))
	}
	j := strings.Join(skipped, "\n")
	// 名前は添付の目録の名前（下ごしらえは目録を書かないので保存名）。
	if !strings.Contains(j, "dx01.dxf は紙にできません") || !strings.Contains(j, "000041-se01 を読めません") {
		t.Errorf("綴じなかったものを言っていません: %v", skipped)
	}

	// 辞退の行の資料は綴じない——綴じるものが無ければ作らず、ボタンも出さない。
	writeBodyFile(t, 50, rfqDocsBody(rfqLineDeclined))
	if v := rfqSendViewHTML(root, 50); strings.Contains(v, "rfq-pdf-docs-go") {
		t.Errorf("辞退の行しか無いのに FAX・印刷用のボタンが出ています")
	}
	if code, _ := postRFQ(t, root, RFQPDFDocsAPIHandler, map[string]any{"page_id": "000050"}); code != 400 {
		t.Errorf("辞退の行しか無いのに %d（400 のはず）", code)
	}
	// 見積依頼書でないページ（明細の表はあるが見積依頼番号が無い）では作らない。
	addPage(t, 51, 0, "写し", "root", "302", true)
	writeBodyFile(t, 51, strings.Replace(rfqDocsBody(rfqLineUnanswered), `<dt>`+RFQNoTag+`</dt><dd>000050</dd>`, "", 1))
	if code, _ := postRFQ(t, root, RFQPDFDocsAPIHandler, map[string]any{"page_id": "000051"}); code != 400 {
		t.Errorf("見積依頼書でないページで %d（400 のはず）", code)
	}
}
