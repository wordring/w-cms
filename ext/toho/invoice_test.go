package toho

import (
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms/page"
)

// 請求書（2026-10-17・invoice.go・invoice_pdf.go）。

// seedIssuedDelivery は納品書ページ id を作って索引に入れます。issued が空なら未発行。行は {受注, 品番, 品名, 数量, 単価}。
func seedIssuedDelivery(t *testing.T, id, parent int, client, delivered, issued string, rows ...[5]string) {
	t.Helper()
	addPage(t, id, parent, "納品書", "alice", "302", true)
	body := `<h1>【弊社】納品書　` + client + `</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindOurDelivery + `</dd>` +
		`<dt>` + DeliveryClientTag + `</dt><dd>` + client + `</dd><dt>` + DeliveredAtTag + `</dt><dd>` + delivered + `</dd>` +
		`<dt>` + DeliveryIssuedTag + `</dt><dd>` + issued + `</dd></dl>` +
		`<table><caption>納品明細表</caption><tbody><tr><th>弊社品番</th><th>受注</th><th>発注書番号</th><th>品番</th><th>品名</th>` +
		`<th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>`
	for _, r := range rows {
		body += `<tr><td></td><td>` + r[0] + `</td><td>PO-1</td><td>` + r[1] + `</td><td>` + r[2] + `</td><td>` + r[3] + `</td><td>個</td><td>` + r[4] + `</td><td></td></tr>`
	}
	syncBody(t, id, body+`</tbody></table>`)
}

// TestClosingDates は締め日の読み方と、締めの日付の数え方を固定します。
func TestClosingDates(t *testing.T) {
	for in, want := range map[string]int{"": 0, "末日": 0, "月末": 0, "20": 20, "20日": 20, "２０日": 20, "31": 0, "15日締め": 15} {
		if got, ok := closingDayOf(in); !ok || got != want {
			t.Errorf("締め日 %q が %d（%v）です（%d を期待）", in, got, ok, want)
		}
	}
	if _, ok := closingDayOf("毎月"); ok {
		t.Errorf("読めない締め日を読めたことにしています")
	}
	day := func(s string) time.Time { d, _ := time.ParseInLocation("2006-01-02", s, time.Local); return d }
	for _, c := range []struct {
		today string
		day   int
		want  string
		prev  string
	}{
		{"2026-10-17", 0, "2026-09-30", "2026-08-31"},
		{"2026-10-31", 0, "2026-10-31", "2026-09-30"},
		{"2026-10-17", 20, "2026-09-20", "2026-08-20"},
		{"2026-10-20", 20, "2026-10-20", "2026-09-20"},
		{"2026-03-10", 0, "2026-02-28", "2026-01-31"},
	} {
		got := latestClosing(day(c.today), c.day)
		if got.Format("2006-01-02") != c.want {
			t.Errorf("%s の締め（%d）が %s です（%s を期待）", c.today, c.day, got.Format("2006-01-02"), c.want)
		}
		if p := previousClosing(got, c.day).Format("2006-01-02"); p != c.prev {
			t.Errorf("%s の前の締めが %s です（%s を期待）", c.want, p, c.prev)
		}
	}
}

// TestInvoiceNewCollectsUnbilledLines は、
//   - 締め日までの**発行済**の納品書の行だけを載せる（未発行・締めより後・単価の空いた行は載せない）
//   - 一度載せた行は、次の請求書に載せない（二重に請求しない）・何も残っていなければ断る
//   - 題・請求書番号・請求日・請求期間・ページ種類・置き場（記録／年／月／製造記録）
//
// ことを固定します。
func TestInvoiceNewCollectsUnbilledLines(t *testing.T) {
	setupExtTest(t, "000950", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 951, -1, "記録の外", "alice", "302", true)
	seedIssuedDelivery(t, 952, 951, "あけぼの精工", "2026-09-25", "2026-09-25",
		[5]string{"000900", "A-1", "ブラケット", "10", "100"}, [5]string{"000900", "A-2", "カバー", "5", ""})
	seedIssuedDelivery(t, 953, 951, "あけぼの精工", "2026-10-05", "2026-10-05", [5]string{"000900", "B-1", "シャフト", "2", "1500"})
	seedIssuedDelivery(t, 954, 951, "あけぼの精工", "2026-09-20", "", [5]string{"000900", "C-1", "まだ紙が無い", "1", "500"})
	seedIssuedDelivery(t, 955, 951, "やまと工作所", "2026-09-10", "2026-09-10", [5]string{"000901", "D-1", "台", "1", "9000"})

	code, out := postRFQ(t, u, InvoiceNewAPIHandler, map[string]any{"client": "あけぼの精工", "close": "2026-09-30"})
	if code != 200 {
		t.Fatalf("作れません: %d %v", code, out)
	}
	if out["later"].(float64) != 1 || out["no_price"].(float64) != 1 || out["rows"].(float64) != 1 {
		t.Errorf("数が違います（載せる1・締めより後1・単価がまだ1）: %v", out)
	}
	id := out["page_id"].(string)
	body := readPageBody(t, id)
	for _, want := range []string{
		"<h1>【弊社】請求書　あけぼの精工</h1>",
		"<dt>" + InvoiceNoTag + "</dt><dd>" + id + "</dd>",
		"<dt>" + InvoiceDateTag + "</dt><dd>2026-09-30</dd>",
		"<dt>" + InvoicePeriodTag + "</dt><dd>2026年9月1日〜2026年9月30日</dd>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindOurInvoice + "</dd>",
		"<caption>請求明細表</caption>",
		"<td>2026-09-25</td><td>000952</td><td>000900</td><td>PO-1</td><td></td><td>A-1</td><td>ブラケット</td><td>10</td><td>個</td><td>100</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("請求書に %q がありません:\n%s", want, body)
		}
	}
	for _, not := range []string{"カバー", "シャフト", "まだ紙が無い", "台"} {
		if strings.Contains(body, "<td>"+not+"</td>") {
			t.Errorf("⚠ 載せない行 %q が載っています:\n%s", not, body)
		}
	}
	if got, want := placeOf(id), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("置き場が %s です（%s のはず）", got, want)
	}

	// 次の締め: A-1 はもう載せた——B-1 だけ。
	code, out = postRFQ(t, u, InvoiceNewAPIHandler, map[string]any{"client": "あけぼの精工", "close": "2026-10-31"})
	if code != 200 {
		t.Fatalf("2枚目を作れません: %d %v", code, out)
	}
	body2 := readPageBody(t, out["page_id"].(string))
	if strings.Contains(body2, "<td>ブラケット</td>") || !strings.Contains(body2, "<td>シャフト</td>") {
		t.Errorf("⚠ 2枚目に、もう請求した行が載っているか、B-1 がありません:\n%s", body2)
	}
	// もう残っていない。
	if code, _ := postRFQ(t, u, InvoiceNewAPIHandler, map[string]any{"client": "あけぼの精工", "close": "2026-10-31"}); code != 400 {
		t.Errorf("載せる行が無いのに作れてしまいます: %d", code)
	}
}

// TestInvoicePDFRoundTrips は請求書の紙に欄が刷られること（消費税は1枚に1回だけ丸める）と、
// 単価の決まっていない行があれば紙を作らないことを固定します。
func TestInvoicePDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	doc := func(rows string) string {
		return `<h1>【弊社】請求書　あけぼの精工</h1><dl data-type="tags"><dt>` + InvoiceNoTag + `</dt><dd>000960</dd>` +
			`<dt>` + DeliveryClientTag + `</dt><dd>あけぼの精工</dd><dt>` + InvoiceDateTag + `</dt><dd>2026-10-31</dd>` +
			`<dt>` + InvoicePeriodTag + `</dt><dd>2026年10月1日〜2026年10月31日</dd><dt>` + PayTermsTag + `</dt><dd>翌月末 手形</dd></dl>` +
			`<table><caption>請求明細表</caption><tbody><tr><th>納品日</th><th>納品書</th><th>受注</th><th>発注書番号</th><th>弊社品番</th>` +
			`<th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>` + rows + `</tbody></table>`
	}
	// 333円 × 3 = 999円 → 消費税 99.9 → 100円（1回だけ丸める）。
	priced := `<tr><td>2026-10-05</td><td>000952</td><td>000900</td><td>PO-1</td><td></td><td>A-1</td><td>ブラケット</td><td>3</td><td>個</td><td>333</td><td></td></tr>`
	pdf, err := buildInvoicePDF(doc(priced), nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"2026年10月31日", "000960", "あけぼの精工", "御中", "ご請求申し上げます", "請求期間", "2026年10月1日〜2026年10月31日",
		"1,099", "翌月末 手形", "10/5", "A-1", "ブラケット", "999", "100", "合計"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ PDFに %q が入っていません:\n%s", want, got)
		}
	}
	open := priced + `<tr><td>2026-10-06</td><td>000953</td><td>000900</td><td>PO-1</td><td></td><td>B-1</td><td>シャフト</td><td>1</td><td>個</td><td></td><td></td></tr>`
	if _, err := buildInvoicePDF(doc(open), nil); err == nil || !strings.Contains(err.Error(), "単価") {
		t.Errorf("⚠ 単価の決まっていない行があるのに紙を作ります: %v", err)
	}
}
