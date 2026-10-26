package toho

import (
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms/page"
)

// 納品書（2026-10-17・delivery.go）——受注明細の行を選んで作る。2026-10-22 からは製造ページの納品作業からだけ（mfg が要る）。

// TestDeliveryNewFromBacklogRows は、
//   - 選んだ行の品番・品名・単位・単価・発注書番号を受注明細から写し、数量は今回納める数
//   - 別々の受注の行を1枚に載せられる（同じ客先なら）・客先が違えば断る（1枚に1社）
//   - 行の品番が変わっていたら断る（取り違えない）
//   - 置き場は 記録／年／月／製造記録・題は【弊社】納品書　<客先>・納品書番号はページ番号・ページ種類は弊社納品書・発行日は空（未発行）
//   - 明細の「製造」の列に製造ページ・製造ページを渡さなければ断る（2026-10-22——案件残表の「🚚 選んだ行で納品書を作る」を外した）
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
	addPage(t, 869, 861, "製造", "alice", "302", true)
	syncBody(t, 869, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)

	// 製造ページを渡さなければ断る（納品書は製造ページの納品作業から作る）。
	if code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000862", "row": 0, "item": "A-1", "qty": "1"},
	}}); code != 400 || !strings.Contains(out["message"].(string), "製造ページ") {
		t.Errorf("⚠ 製造ページ無しで納品書を作れてしまいます: %d %v", code, out)
	}

	code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": "000869", "items": []map[string]any{
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
		"<td>000862</td><td>000869</td><td>PO-000862</td><td>A-1</td><td>ブラケット</td><td>10</td><td>個</td><td>1500</td>",
		"<td>000863</td><td>000869</td><td>PO-000863</td><td>B-1</td><td>シャフト</td><td>10</td><td>個</td><td>800</td>",
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
	if code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": "000869", "items": []map[string]any{
		{"order": "000862", "row": 0, "item": "A-1", "qty": "1"}, {"order": "000864", "row": 0, "item": "C-1", "qty": "1"},
	}}); code != 400 || !strings.Contains(out["message"].(string), "1枚に1社") {
		t.Errorf("客先の違う行を混ぜています: %d %v", code, out)
	}
	// 品番が変わった行（取り違え）は断る。
	if code, _ := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": "000869", "items": []map[string]any{
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

// TestDeliveryNewCapsAtRemaining は、⚠ **納品書に、注文の数からもう納めた数を引いた残りより多くは載せない**ことを固定します
// （2026-10-26——それまでは見ておらず、押し直しや数え違いで注文より多く納め、出荷済数量と請求が多すぎた）。
// ほかの納品書で納めた数も、同じ納品書のほかの行で納める数も数える。
func TestDeliveryNewCapsAtRemaining(t *testing.T) {
	setupExtTest(t, "000870", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 871, -1, "受注", "alice", "302", true)
	seedOrder(t, 872, 871, "あけぼの精工", "2026-11-01", `<tr><td>000777</td><td>A-1</td><td>ブラケット</td><td>20</td><td>個</td><td>1500</td>`+
		`<td>2026-11-01</td><td></td><td></td><td>未着手</td></tr>`)
	addPage(t, 879, 871, "製造", "alice", "302", true)
	syncBody(t, 879, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)
	deliver := func(qtys ...string) (int, map[string]any) {
		var items []map[string]any
		for _, q := range qtys {
			items = append(items, map[string]any{"order": "000872", "row": 0, "item": "A-1", "qty": q})
		}
		return postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": "000879", "items": items})
	}
	if code, out := deliver("21"); code != 409 || !strings.Contains(out["message"].(string), "20 までです") {
		t.Errorf("⚠ 注文の20より多い21で作れました: %d %v", code, out)
	}
	if code, out := deliver("15"); code != 200 {
		t.Fatalf("15 で作れません: %d %v", code, out)
	}
	// 残りは5——1枚の中で 3＋3 は断る。
	if code, out := deliver("3", "3"); code != 409 || !strings.Contains(out["message"].(string), "2 までです") {
		t.Errorf("⚠ 同じ納品書の行の合計で残りを超えました: %d %v", code, out)
	}
	if code, out := deliver("5"); code != 200 {
		t.Fatalf("残りの5で作れません: %d %v", code, out)
	}
	if code, out := deliver("1"); code != 409 {
		t.Errorf("⚠ 全部納めたあとにまた作れました: %d %v", code, out)
	}
}

// TestDeliveryNewCapCountsBothRowsOfAnItem は、同じ案件に同じ品物の行が2つ（納期を分けた）あるとき、
// 上限を2行の数量の合計で比べることを固定します（納めた数は案件と品物で数えるので、1行の数量で比べると、2行目を納められない）。
func TestDeliveryNewCapCountsBothRowsOfAnItem(t *testing.T) {
	setupExtTest(t, "000880", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 881, -1, "受注", "alice", "302", true)
	line := func(qty, due string) string {
		return `<tr><td>000777</td><td>A-1</td><td>ブラケット</td><td>` + qty + `</td><td>個</td><td>1500</td><td>` + due + `</td><td></td><td></td><td>未着手</td>`
	}
	seedOrder(t, 882, 881, "あけぼの精工", "2026-11-01", line("10", "2026-11-01")+line("20", "2026-11-20"))
	addPage(t, 889, 881, "製造", "alice", "302", true)
	syncBody(t, 889, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)
	deliver := func(row int, qty string) (int, map[string]any) {
		return postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": "000889", "items": []map[string]any{{"order": "000882", "row": row, "item": "A-1", "qty": qty}}})
	}
	if code, out := deliver(0, "10"); code != 200 {
		t.Fatalf("1行目の10を納められません: %d %v", code, out)
	}
	if code, out := deliver(1, "20"); code != 200 {
		t.Errorf("⚠ 2行目の20を納められません（1行目の数量だけで比べている）: %d %v", code, out)
	}
	if code, out := deliver(1, "1"); code != 409 || !strings.Contains(out["message"].(string), "30 のうち 30") {
		t.Errorf("⚠ 2行の合計 30 を納めたあとにまた作れました: %d %v", code, out)
	}
}
