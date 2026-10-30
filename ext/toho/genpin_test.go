package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

func genpinDeliveryBody() string {
	return `<h1>納品書</h1><dl data-type="tags">` +
		`<dt>` + DeliveryNoTag + `</dt><dd>D-7</dd>` +
		`<dt>` + DeliveryClientTag + `</dt><dd>みなと商店</dd>` +
		`<dt>` + DeliveredAtTag + `</dt><dd>2026-10-30</dd></dl>` +
		`<table data-type="` + DeliveryItemsType + `"><caption>納品明細表</caption><tbody>` +
		`<tr><th>` + OrderNoTag + `</th><th>` + ItemNoTag + `</th><th>品名</th><th>数量</th><th>単位</th></tr>` +
		`<tr><td>PO-1</td><td>A100-B01-001</td><td>カバー</td><td>40</td><td>個</td></tr>` +
		`<tr><td>PO-1</td><td>A100-B01-002</td><td>ステー</td><td>5</td><td>個</td></tr>` +
		`</tbody></table>`
}

func genpinOrderBody() string {
	return `<h1>部材発注</h1><dl data-type="tags">` +
		`<dt>` + PurchaseNoTag + `</dt><dd>002300</dd>` +
		`<dt>` + SupplierTag + `</dt><dd>ふじ鍍金</dd>` +
		`<dt>` + DueDateTag + `</dt><dd>2026-11-10</dd></dl>` +
		`<table data-type="our-order-items"><caption>部材発注明細表</caption><tbody>` +
		`<tr><th>種類</th><th>品番</th><th>品名</th><th>加工部品番号</th><th>表面</th><th>仕様</th><th>材質</th><th>数量</th><th>単位</th><th>状態</th></tr>` +
		`<tr><td>材料</td><td>K120-1</td><td>板</td><td></td><td></td><td></td><td>SS400</td><td>3</td><td>枚</td><td>未発注</td></tr>` +
		`<tr><td>外注加工</td><td>K120-2</td><td>ブラケット</td><td>K120-2-01</td><td>三価ユニクロ</td><td></td><td></td><td>20</td><td>個</td><td>発注済</td></tr>` +
		`<tr><td>外注加工</td><td>K120-3</td><td>アーム</td><td></td><td>黒</td><td>艶消し</td><td></td><td>8</td><td>個</td><td>取消</td></tr>` +
		`<tr><td></td><td>K120-4</td><td>脚</td><td></td><td>緑</td><td></td><td></td><td>6</td><td>個</td><td>未発注</td></tr>` +
		`</tbody></table>`
}

func TestReadGenpinDocDelivery(t *testing.T) {
	d, err := readGenpinDoc(genpinDeliveryBody())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != genpinKindDelivery || len(d.Rows) != 2 || d.Rows[1].Item != "A100-B01-002" || d.Rows[1].Row != 1 {
		t.Fatalf("納品書の行: %+v", d)
	}
}

// TestReadGenpinDocSurfaceRows は、表面処理の札が外注加工の行だけ（取り消した行を除く・種類が空でも表面があれば）から作られることを固定します。
func TestReadGenpinDocSurfaceRows(t *testing.T) {
	d, err := readGenpinDoc(genpinOrderBody())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != genpinKindSurface {
		t.Fatalf("種類: %s", d.Kind)
	}
	var items []string
	for _, r := range d.Rows {
		items = append(items, r.Item)
	}
	if strings.Join(items, ",") != "K120-2,K120-4" {
		t.Fatalf("札にできる行: %v（材料と取り消した行は除くはず）", items)
	}
	if _, err := readGenpinDoc(`<h1>メモ</h1><p>なにも</p>`); err == nil {
		t.Error("納品書でも部材発注書でもないページは断るはず")
	}
}

func TestGenpinLabelsBoxesAndCopies(t *testing.T) {
	setupExtTest(t, "000001", page.PageMeta{Owner: "a", Mode: "302"})
	d, _ := readGenpinDoc(genpinDeliveryBody())
	ls, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "A100-B01-001", Copies: 2, Boxes: "２０、20", Note: "上下注意"}}, nil, "https://example.test/002300")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 4 {
		t.Fatalf("2箱 × 2枚で4枚のはず: %d", len(ls))
	}
	if ls[0].Box != "1/2 箱" || ls[3].Box != "2/2 箱" || ls[0].Qty != "20 個" || ls[0].Note != "上下注意" {
		t.Errorf("札の中身: %+v", ls[0])
	}
	if !strings.Contains(strings.Join(ls[0].Foot, " "), "注文番号 PO-1") || !strings.Contains(strings.Join(ls[0].Foot, " "), "納品日 2026-10-30") {
		t.Errorf("足元: %v", ls[0].Foot)
	}
	if _, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "A100-B01-001", Boxes: "20,10"}}, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "合いません") {
		t.Errorf("箱の合計が数量と違えば断るはず: %v", err)
	}
	if _, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "A100-B01-002"}}, nil, ""); err == nil ||
		!strings.Contains(err.Error(), "表が変わりました") {
		t.Errorf("行の品番が違えば断るはず: %v", err)
	}
	if _, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "A100-B01-001", Copies: 51}}, nil, ""); err == nil {
		t.Error("枚数が多すぎれば断るはず")
	}
}

func TestGenpinSurfaceLabel(t *testing.T) {
	setupExtTest(t, "000001", page.PageMeta{Owner: "a", Mode: "302"})
	d, _ := readGenpinDoc(genpinOrderBody())
	ls, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "K120-2"}}, nil, "")
	if err != nil || len(ls) != 1 {
		t.Fatalf("1枚のはず: %v %d", err, len(ls))
	}
	l := ls[0]
	if l.Big != "K120-2" || l.Sub != "K120-2-01" || l.Work != "三価ユニクロ" || l.Qty != "20 個" {
		t.Errorf("表面処理の札: %+v", l)
	}
	foot := strings.Join(l.Foot, " ")
	if !strings.Contains(foot, "納期 2026-11-10") || !strings.Contains(foot, "発注書 002300") || !strings.Contains(foot, "戻り先") {
		t.Errorf("足元: %v", l.Foot)
	}
	if !strings.HasPrefix(genpinTop(d, nil), "表面処理　現品票　") {
		t.Errorf("上の言葉: %s", genpinTop(d, nil))
	}
}

// TestGenpinPDFRoundTrips は、札の PDF に字が入り、札の数だけページがあることを固定します。
func TestGenpinPDFRoundTrips(t *testing.T) {
	setupExtTest(t, "000001", page.PageMeta{Owner: "a", Mode: "302"})
	withPDFFont(t, systemJPFont(t))
	d, _ := readGenpinDoc(genpinDeliveryBody())
	ls, err := genpinLabelsFor(d, []genpinPick{{Row: 0, Item: "A100-B01-001", Boxes: "20,20"}}, nil, "https://example.test/002300")
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := buildGenpinPDF(ls)
	if err != nil {
		t.Fatalf("PDF を作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"現品票", "A100-B01-001", "カバー", "数量　20 個", "1/2 箱", "2/2 箱", "注文番号 PO-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("PDF に %q が入っていません:\n%s", want, got)
		}
	}
	if n := strings.Count(string(pdf), "/Type /Page\n") + strings.Count(string(pdf), "/Type /Page\r") + strings.Count(string(pdf), "/Type /Page "); n != 0 && n != 2 {
		t.Errorf("札は2枚のはず: %d", n)
	}
}

func TestSplitBoxes(t *testing.T) {
	for in, want := range map[string]string{"": "", "20,20": "20|20", "２０　２０": "20|20", "10、5,5": "10|5|5", "2.5,2.5": "2.5|2.5"} {
		got, err := splitBoxes(in)
		if err != nil || strings.Join(got, "|") != want {
			t.Errorf("%q → %v %v（%q のはず）", in, got, err, want)
		}
	}
	for _, bad := range []string{"a", "0", "-1"} {
		if _, err := splitBoxes(bad); err == nil {
			t.Errorf("%q は断るはず", bad)
		}
	}
}
