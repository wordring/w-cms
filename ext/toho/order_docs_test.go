package toho

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/signintech/gopdf"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 外注加工の資料を発注書に添える（2026-09-28・order_docs.go）。
//
// 下ごしらえ: 加工製品 31（誰でも読める）に「資料 1」「資料２」の折りたたみ、通信記録 40（誰でも読める）に
// 顧客の図面、41（alice だけ）に読めない図面。見るのは bob（管理者でない）。

var tinyPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

// drawingPDF は A3 横の2ページのPDFを作ります（綴じたとき大きさが保たれるかを見る）。
func drawingPDF(t *testing.T) []byte {
	t.Helper()
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: gopdf.Rect{W: 1190.55, H: 841.89}})
	for i := 0; i < 2; i++ {
		p.AddPage()
		p.Line(40, 40, 1100, 800)
	}
	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func seedOrderDocs(t *testing.T) {
	t.Helper()
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 31, 0, "ブラケット", "root", "302", true)
	addPage(t, 40, 0, "通信記録", "root", "302", true)
	addPage(t, 41, 0, "内緒の記録", "alice", "300", false)
	putAttachment(t, "000040", "dr01.pdf", drawingPDF(t))
	putAttachment(t, "000041", "se01.pdf", drawingPDF(t))
	putAttachment(t, "000031", "ph01.png", tinyPNG)
	putAttachment(t, "000031", "dx01.dxf", []byte("0\nSECTION\n0\nEOF\n"))
	writeBodyFile(t, 31, `<h1>ブラケット</h1>`+
		`<table><caption>外注加工</caption><tbody><tr><th>番号</th><th>加工内容</th><th>個数</th></tr>`+
		`<tr><td>1</td><td>レーザー切断</td><td>1</td></tr><tr><td>2</td><td>塗装</td><td>1</td></tr></tbody></table>`+
		`<details open><summary>資料 1</summary>`+
		`<section data-type="file-view" data-ref="000040-dr01"></section>`+
		`<p><img src="/000031/ph01.png"></p>`+
		`<section data-type="file-view" data-ref="000041-se01"></section>`+
		`<p>📎 <a href="/000031/dx01.dxf" download="展開.dxf">展開.dxf</a></p>`+
		`</details>`+
		`<details><summary>資料２</summary><p>まだ無い</p></details>`)
}

// TestOrderDocsFollowsLineToFold は、発注明細の行（弊社品番＋番号）から加工製品ページの
// 「資料 <番号>」の折りたたみへ辿り、中のファイルを集めることを固定します。
//
//   - 題は空白・全角半角を畳んで比べる（「資料２」も番号 2）
//   - ⚠ **読めないページの図面は渡さない**（理由は出す）
//   - 折りたたみの無い行は「資料がありません」と言う・番号の無い行（材料）は見ない
func TestOrderDocsFollowsLineToFold(t *testing.T) {
	seedOrderDocs(t)
	rows := []map[string]string{
		{"弊社品番": "000031", "番号": "1", "加工内容": "レーザー切断"},
		{"弊社品番": "000031", "番号": "2", "加工内容": "塗装"}, // 題は全角の「資料２」
		{"弊社品番": "000031", "番号": "3", "加工内容": "曲げ"},
		{"弊社品番": "000031", "材質": "鉄"},
	}
	docs, notes := orderDocs(&auth.User{Username: "bob"}, rows)

	var got []string
	for _, d := range docs {
		got = append(got, d.Line+" "+d.PageID+"/"+d.File)
	}
	want := []string{"000031-1 000040/dr01.pdf", "000031-1 000031/ph01.png", "000031-1 000031/dx01.dxf"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("集めた資料が違います:\n got  %v\n want %v", got, want)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "000041-se01 を読めません") {
		t.Errorf("⚠ 読めない図面のことを黙っています: %v", notes)
	}
	if !strings.Contains(joined, "000031-3 は資料がありません") {
		t.Errorf("折りたたみの無い行のことを言っていません: %v", notes)
	}
	if strings.Contains(joined, "000031-2 は資料がありません") {
		t.Errorf("⚠ 「資料２」の折りたたみを見つけていません: %v", notes)
	}
	for _, d := range docs {
		if d.PageID == "000041" {
			t.Errorf("⚠ 読めないページの図面を渡しています: %+v", d)
		}
	}
}

// TestOrderSendFormOffersDocs は、発注書ページの送信欄に**資料のチェック**（全部チェック済み）と、
// 綴じられる資料があるときだけ「📠 FAX・印刷用（資料を綴じる）」が出ることを固定します。
func TestOrderSendFormOffersDocs(t *testing.T) {
	seedOrderDocs(t)
	addPage(t, 50, 0, "発注 ひかりレーザー", "root", "302", true)
	render := func(no string) string {
		body := `<h1>発注</h1><table data-type="` + ourOrderItemsType + `"><tbody>` +
			`<tr><th>弊社品番</th><th>種類</th><th>番号</th><th>加工内容</th><th>数量</th><th>状態</th></tr>` +
			`<tr><td>000031</td><td>外注加工</td><td>` + no + `</td><td>レーザー切断</td><td>1</td><td>未発注</td></tr>` +
			`</tbody></table>`
		syncBody(t, 50, body)
		req := httptest.NewRequest("GET", "/000050", nil)
		req = auth.WithUser(req, &auth.User{Username: "bob"})
		return cms.RenderComputedViews(req, 50, body)
	}
	out := render("1")
	if !regexp.MustCompile(`<input type="checkbox" checked(="")? data-doc-page="000040" data-doc-file="dr01.pdf"`).MatchString(out) {
		t.Errorf("図面のチェックが出ていません:\n%s", out)
	}
	if !strings.Contains(out, `data-order-pdf-docs="1"`) {
		t.Errorf("綴じられる資料があるのに FAX・印刷用のボタンが出ていません:\n%s", out)
	}
	if strings.Contains(out, "se01.pdf") {
		t.Errorf("⚠ 読めない図面を候補に出しています:\n%s", out)
	}

	out = render("3")
	if strings.Contains(out, `data-order-pdf-docs="1"`) || strings.Contains(out, `data-doc-file=`) {
		t.Errorf("資料の無い行でボタンや候補が出ています:\n%s", out)
	}
	if !strings.Contains(out, "000031-3 は資料がありません") {
		t.Errorf("資料が無いことを言っていません:\n%s", out)
	}
}

// TestBindOrderDocsKeepsSizes は、FAX・印刷用の1本が**発注書のうしろに資料を元の大きさで綴じる**
// ことと、⚠ **綴じられないものを飛ばして言う**（落ちない）ことを固定します。
func TestBindOrderDocsKeepsSizes(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	seedOrderDocs(t)
	// ⚠ **末尾に startxref の無いPDF**——gofpdi はこれで終わらない繰り返しに入っていた（guardedReader）。
	putAttachment(t, "000031", "br01.pdf", []byte("%PDF-1.4\n壊れたPDF\n"))
	orderPDF, err := buildOrderPDF(pdfOrderBody(`<tr><td>000031</td><td></td><td>ブラケット</td>`+
		`<td></td><td></td><td></td><td></td><td>1</td><td>個</td><td></td><td></td><td>未発注</td></tr>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	docs := []orderDoc{
		{PageID: "000040", File: "dr01.pdf", Name: "図面.pdf", Line: "000031-1"},
		{PageID: "000031", File: "ph01.png", Name: "写真.png", Line: "000031-1"},
		{PageID: "000031", File: "dx01.dxf", Name: "展開.dxf", Line: "000031-1"},
		{PageID: "000031", File: "br01.pdf", Name: "壊れた.pdf", Line: "000031-1"},
	}
	bound, skipped, err := bindOrderDocs(orderPDF, docs)
	if err != nil {
		t.Fatalf("綴じられません: %v", err)
	}
	pages := len(regexp.MustCompile(`/Type /Page\s`).FindAll(bound, -1))
	if pages != 4 { // 発注書1 ＋ 図面2（A3 横）＋ 写真1
		t.Errorf("ページが %d 枚です（4枚のはず）", pages)
	}
	if !regexp.MustCompile(`/MediaBox \[\s*0 0 1190\.55`).Match(bound) {
		t.Errorf("⚠ 図面が A3 横の大きさで綴じられていません（縮めていないか）")
	}
	j := strings.Join(skipped, "\n")
	if !strings.Contains(j, "展開.dxf は紙にできません") || !strings.Contains(j, "壊れた.pdf は綴じられません") {
		t.Errorf("綴じなかったものを言っていません: %v", skipped)
	}
}
