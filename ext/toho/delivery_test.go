package toho

import (
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms/page"
)

// 納品書（2026-10-17・delivery.go）——受注明細の行を選んで作る。

// TestDeliveryNewFromBacklogRows は、
//   - 選んだ行の品番・品名・単位・単価・発注書番号を受注明細から写し、数量は今回納める数
//   - 別々の受注の行を1枚に載せられる（同じ客先なら）・客先が違えば断る（1枚に1社）
//   - 行の品番が変わっていたら断る（取り違えない）
//   - 置き場は 記録／年／月／製造記録・題は【弊社】納品書　<客先>・納品書番号はページ番号・ページ種類は弊社納品書・発行日は空（未発行）
//
// ことを固定します。
func TestDeliveryNewFromBacklogRows(t *testing.T) {
	setupExtTest(t, "000860", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 861, -1, "受注", "alice", "302", true)
	row := func(item, name, qty, price string) string {
		return `<tr><td>000777</td><td>` + item + `</td><td>` + name + `</td><td>` + qty + `</td><td>個</td><td>` + price +
			`</td><td>2026-11-01</td><td></td><td></td><td>未着手</td></tr>`
	}
	seedOrder(t, 862, 861, "あけぼの精工", "2026-11-01", row("A-1", "ブラケット", "20", "1500")+row("A-2", "カバー", "5", ""))
	seedOrder(t, 863, 861, "あけぼの精工", "2026-11-05", row("B-1", "シャフト", "10", "800"))
	seedOrder(t, 864, 861, "やまと工作所", "2026-11-05", row("C-1", "台", "1", "9000"))

	code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000862", "row": 0, "item": "A-1", "qty": "10"}, // 分納（20個のうち10個）
		{"order": "000862", "row": 1, "item": "A-2", "qty": "5"},  // 単価が決まっていない行
		{"order": "000863", "row": 0, "item": "B-1", "qty": "10"}, // 別の受注の行
	}})
	if code != 200 {
		t.Fatalf("作れません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body := readPageBody(t, id)
	for _, want := range []string{
		"<h1>【弊社】納品書　あけぼの精工</h1>",
		"<dt>" + DeliveryNoTag + "</dt><dd>" + id + "</dd>",
		"<dt>" + DeliveryClientTag + "</dt><dd>あけぼの精工</dd>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindOurDelivery + "</dd>",
		"<dt>" + DeliveryIssuedTag + "</dt><dd><br/></dd>",
		"<caption>納品明細表</caption>",
		"<td>000862</td><td></td><td>PO-000862</td><td>A-1</td><td>ブラケット</td><td>10</td><td>個</td><td>1500</td>",
		"<td>000863</td><td></td><td>PO-000863</td><td>B-1</td><td>シャフト</td><td>10</td><td>個</td><td>800</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("納品書に %q がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<td>カバー</td><td>5</td><td>個</td><td>0</td>") {
		t.Errorf("決まっていない単価を 0 にしています:\n%s", body)
	}
	if got, want := placeOf(id), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("置き場が %s です（%s のはず）", got, want)
	}

	// 客先が違う行は混ぜない。
	if code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000862", "row": 0, "item": "A-1", "qty": "1"}, {"order": "000864", "row": 0, "item": "C-1", "qty": "1"},
	}}); code != 400 || !strings.Contains(out["message"].(string), "1枚に1社") {
		t.Errorf("客先の違う行を混ぜています: %d %v", code, out)
	}
	// 品番が変わった行（取り違え）は断る。
	if code, _ := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000862", "row": 0, "item": "Z-9", "qty": "1"},
	}}); code != 409 {
		t.Errorf("品番の違う行で作れてしまいます: %d", code)
	}
}

// TestDeliveryPDFRoundTrips は納品書の紙に欄が刷られることを、読み返して確かめます（単価が全部ある・決まっていない行がある）。
func TestDeliveryPDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	doc := func(rows string) string {
		return `<h1>【弊社】納品書　あけぼの精工</h1><dl data-type="tags"><dt>` + DeliveryNoTag + `</dt><dd>000870</dd>` +
			`<dt>` + DeliveryClientTag + `</dt><dd>あけぼの精工</dd><dt>` + DeliveredAtTag + `</dt><dd>2026-10-20</dd></dl>` +
			`<table><caption>納品明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>発注書番号</th><th>品番</th><th>品名</th>` +
			`<th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>` + rows + `</tbody></table>`
	}
	priced := `<tr><td>000777</td><td>000862</td><td>PO-1</td><td>A-1</td><td>ブラケット</td><td>10</td><td>個</td><td>1500</td><td></td></tr>`
	pdf, err := buildDeliveryPDF(doc(priced), nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"2026年10月20日", "000870", "あけぼの精工", "御中", "下記のとおり納品いたします", "PO-1", "A-1", "ブラケット", "15,000", "小計", "合計"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ PDFに %q が入っていません:\n%s", want, got)
		}
	}
	// 単価の決まっていない行があれば、合計を刷らず、そう書く（0 にしない）。
	open := priced + `<tr><td>000778</td><td>000863</td><td>PO-2</td><td>B-1</td><td>シャフト</td><td>5</td><td>個</td><td></td><td></td></tr>`
	pdf2, err := buildDeliveryPDF(doc(open), nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got2 := pdfTextOf(t, pdf2)
	if strings.Contains(got2, "合計　") || !strings.Contains(got2, "単価の決まっていない品") || !strings.Contains(got2, "注文番号") {
		t.Errorf("⚠ 決まっていない単価の行がある紙が違います（合計を刷らない・注文番号は列）:\n%s", got2)
	}
}

// 貴社注文番号は、全部の行が同じ番号のときだけ紙の上に1つ刷る。番号の無い行が混ざれば列にする（2026-10-22 の見直し）。
func TestDeliveryOrderNos(t *testing.T) {
	row := func(no string) map[string]string { return map[string]string{OrderNoTag: no} }
	cases := []struct {
		name     string
		rows     []map[string]string
		single   string
		asColumn bool
	}{
		{"全部同じ", []map[string]string{row("A-1"), row("A-1")}, "A-1", false},
		{"1行だけに番号", []map[string]string{row("A-1"), row("")}, "", true},
		{"いくつもある", []map[string]string{row("A-1"), row("B-2")}, "", true},
		{"どこにも無い", []map[string]string{row(""), row("")}, "", false},
	}
	for _, c := range cases {
		if s, col := deliveryOrderNos(c.rows); s != c.single || col != c.asColumn {
			t.Errorf("%s: (%q, %v) のはずが (%q, %v)", c.name, c.single, c.asColumn, s, col)
		}
	}
}
