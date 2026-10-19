package toho

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// TestItemRowsOfDropsEmptyAndFootRows は、紙の明細の行を読む itemRowsOf の番人です（2026-10-19 のリファクタリングで足した）。
// 見積書・部材見積依頼書・納品書・請求書の4つの紙が、ここを通って明細を読みます。
//   - 空の行（どのセルも空）と、足元（tfoot）の行は落とす
//   - 見出しより多いセルは捨て、少ない行は在るセルだけ
//   - 見出しとセルの前後の空白は落とす
func TestItemRowsOfDropsEmptyAndFootRows(t *testing.T) {
	nodes, err := htmldoc.ParseFragment(`<table><tbody>
<tr><th> 品番 </th><th>数量</th></tr>
<tr><td> A1 </td><td>3</td><td>余り</td></tr>
<tr><td></td><td> </td></tr>
<tr><td>B2</td></tr>
</tbody><tfoot><tr><td>合計</td><td>3</td></tr></tfoot></table>`)
	if err != nil {
		t.Fatal(err)
	}
	table := findElement(nodes, func(n *html.Node) bool { return n.Data == "table" })
	got := itemRowsOf(table)
	want := []map[string]string{
		{"品番": "A1", "数量": "3"},
		{"品番": "B2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("itemRowsOf = %v, want %v", got, want)
	}
}

// TestNoteSectionLinesReadsOnlyThatHeading は、備考の節を見出しで探す noteSectionLines の番人です（同じとき）。
func TestNoteSectionLinesReadsOnlyThatHeading(t *testing.T) {
	nodes, err := htmldoc.ParseFragment(`<section><h2>ほか</h2><p>違う</p></section><section><h2>備考</h2><p>一行目</p><p>二行目</p></section>`)
	if err != nil {
		t.Fatal(err)
	}
	root := wrapInDiv(nodes)
	if got := noteSectionLines(root, "備考"); !reflect.DeepEqual(got, []string{"一行目", "二行目"}) {
		t.Fatalf("備考 = %v", got)
	}
	if got := noteSectionLines(root, "無い節"); got != nil {
		t.Fatalf("無い節 = %v, want nil", got)
	}
}

// TestStampIssuedTodayKeepsAnExistingDate は、紙を作ったときの `発行日` の番人です（同じとき・納品書と請求書が通る）。
// 空なら今日を書き、既に日付があれば上書きしない（作り直しても、初めに出した日のまま）。
func TestStampIssuedTodayKeepsAnExistingDate(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	doc := func(issued string) string {
		return `<h1>納品書</h1><dl data-type="tags"><dt>` + DeliveryIssuedTag + `</dt><dd>` + issued + `</dd></dl>`
	}
	issuedOf := func(body string) string {
		head, _, _ := readDeliveryDoc(body + `<table><caption>納品明細表</caption><tbody><tr><th>品名</th></tr></tbody></table>`)
		return head[DeliveryIssuedTag]
	}
	for _, c := range []struct{ before, want string }{
		{"2026-01-02", "2026-01-02"},
		{"", time.Now().Format("2006-01-02")},
	} {
		writeBodyFile(t, 500, doc(c.before))
		var made madeOrderPDF
		stampIssuedToday(&made, adminUser(), "000500", issuedOf)
		body, err := cms.ReadPageBody("000500")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(issuedOf(body)); got != c.want {
			t.Errorf("発行日 %q のあと = %q, want %q（本文 %s）", c.before, got, c.want, body)
		}
		// 2つ目の発行日を足してもいけない（最初の値だけ読む人には見えないが、紙と索引が食い違う）。
		if n := strings.Count(body, "<dt>"+DeliveryIssuedTag+"</dt>"); n != 1 {
			t.Errorf("発行日 %q のあと、発行日の欄が %d 個あります: %s", c.before, n, body)
		}
		if made.ViewNote != "" {
			t.Errorf("断りが出ました: %s", made.ViewNote)
		}
	}
}

// TestIssuedStateRowSaysIssued は、納品書・請求書の明細の足元の「状態」の行の番人です（同じとき）。
func TestIssuedStateRowSaysIssued(t *testing.T) {
	for _, c := range []struct{ issued, want string }{
		{"2026-01-02", "状態: 発行済（2026-01-02）"},
		{"", "状態: 未発行（紙をまだ作っていません）"},
	} {
		nodes, err := htmldoc.ParseFragment(`<dl data-type="tags"><dt>` + DeliveryIssuedTag + `</dt><dd>` + c.issued + `</dd></dl>` +
			`<table><tbody><tr><th>品名</th></tr></tbody></table>`)
		if err != nil {
			t.Fatal(err)
		}
		root := wrapInDiv(nodes)
		table := findElement([]*html.Node{root}, func(n *html.Node) bool { return n.Data == "table" })
		appendIssuedStateRow(table, 1)
		if got := htmldoc.Render(childrenOf(root)); !strings.Contains(got, c.want) {
			t.Errorf("発行日 %q の足元に %q がありません: %s", c.issued, c.want, got)
		}
	}
}

// TestRecordFolderOrPrefersParentAndExplains は、書類の置き場を決める recordFolderOr の番人です（同じとき）。
//   - parent があれば、記録には作らずそれを返す（製造ページの子に作る書類）
//   - 無ければ `記録／年／月／<sub>` を作って返す
//   - 用意できなければ、行き先を添えた断りを返す（画面にそのまま出る）
func TestRecordFolderOrPrefersParentAndExplains(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	when := time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local)
	if got, err := recordFolderOr("000500", "root", RecordWorkFolder, when); err != nil || got != "000500" {
		t.Fatalf("parent があるとき = %q, %v", got, err)
	}
	got, err := recordFolderOr("", "root", RecordWorkFolder, when)
	if err != nil || got == "" || got == "000500" {
		t.Fatalf("parent が無いとき = %q, %v", got, err)
	}
	if title := cms.PageTitleByID(pageNum(got)); title != RecordWorkFolder {
		t.Fatalf("作ったフォルダの題 = %q", title)
	}
	// 置き場を作るテンプレートが無ければ断る（行き先を添えて）。
	if err := cms.PurgePageIndex("000916"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(page.GetPageDir("000916")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{got} { // 作ったフォルダから上も消して、一から作り直させる
		for cur := id; cur != "" && cur != cms.TopPageID; {
			meta, _ := page.ReadSidecar(cur)
			_ = cms.PurgePageIndex(cur)
			_ = os.RemoveAll(page.GetPageDir(cur))
			cur = meta.ParentID
		}
	}
	_, err = recordFolderOr("", "root", RecordRequestFolder, when)
	if err == nil || !strings.Contains(err.Error(), "「"+recordPath(RecordRequestFolder, when)+"」を用意できません: ") {
		t.Fatalf("置き場を作れないときの断り = %v", err)
	}
}

// TestNewProductListRowFromTags は、加工製品の一覧の1行を組む newProductListRow の番人です（同じとき——置き場の下の一覧と
// 全社の一覧が通る）。図面番号・品番は全部を「・」で・移行中・題と違う名前・選択肢に無い区分もそのまま見せる。
func TestNewProductListRowFromTags(t *testing.T) {
	row := newProductListRow(7, "ブラケット", map[string][]string{
		DrawingNoTag:   {"A100-B01", "A100-B02"},
		"品番":           {"P-1"},
		MigratingTag:   {"移行中"},
		ItemNameTag:    {"ブラケット", "取付金具"},
		DrawingNameTag: {"取付金具"},
		ProductKindTag: {"消した区分"},
	})
	want := productListRow{PageID: 7, Title: "ブラケット", DrawingNo: "A100-B01・A100-B02", PartNo: "P-1", Migrating: true,
		Names: []string{"取付金具"}, Kinds: []string{"消した区分"}}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("newProductListRow =\n %+v\nwant\n %+v", row, want)
	}
}

// TestEstimateBlockAtRefusesOutOfRange は、見積の組を取り出す estimateBlockAt の番人です（同じとき）——負の番号・多すぎる番号は断る。
func TestEstimateBlockAtRefusesOutOfRange(t *testing.T) {
	for _, n := range []int{-1, 5} {
		if _, _, err := estimateBlockAt(`<p>組なし</p>`, n); err == nil || !strings.Contains(err.Error(), "見つかりません") {
			t.Errorf("block %d の断り = %v", n, err)
		}
	}
}

// TestQuoteCasesFromFindsCasesMadeFromRecord は、記録から作った見積の依頼の案件を引く quoteCasesFrom の番人です（同じとき——
// 受信元のタグで引く・種類が見積の依頼のものだけ）。
func TestQuoteCasesFromFindsCasesMadeFromRecord(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	mk := func(id int, kind string) {
		pid := page.FormatID(id)
		if err := page.WriteSidecar(pid, page.PageMeta{Owner: "root", Mode: "302", ParentID: cms.TopPageID}); err != nil {
			t.Fatal(err)
		}
		writeBodyFile(t, id, `<h1>案件</h1><dl data-type="tags"><dt>`+SourceRefTag+`</dt><dd>000500</dd><dt>`+PageKindTag+`</dt><dd>`+kind+`</dd></dl>`)
	}
	mk(501, PageKindCustomerQuote)
	mk(502, PageKindCustomerOrder)
	if got := quoteCasesFrom(adminUser(), "000500"); !reflect.DeepEqual(got, []string{"000501"}) {
		t.Fatalf("quoteCasesFrom = %v, want [000501]", got)
	}
}

// TestPickedOrderRowChecksPageKind は、案件残表で選んだ行を探す pickedOrderRow の番人です（同じとき）——案件ページの種類が
// 呼び手の許すものでなければ「見つかりません」（納品書は注文の案件だけ・製造は見積の依頼からも）。
func TestPickedOrderRowChecksPageKind(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	if err := page.WriteSidecar("000501", page.PageMeta{Owner: "root", Mode: "302", ParentID: cms.TopPageID}); err != nil {
		t.Fatal(err)
	}
	writeBodyFile(t, 501, `<h1>見積の依頼</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindCustomerQuote+`</dd></dl>`)
	p := deliveryPick{Order: "000501", Row: 0, Item: "A-1"}
	_, _, why := pickedOrderRow(adminUser(), p, func(k string) bool { return k == PageKindCustomerOrder }, false)
	if why != "案件ページが見つかりません: /000501" {
		t.Errorf("注文の案件だけを許すとき = %q", why)
	}
	_, _, why = pickedOrderRow(adminUser(), p, func(k string) bool { return k == PageKindCustomerQuote }, false)
	if why == "案件ページが見つかりません: /000501" {
		t.Errorf("見積の依頼を許すときに断られました: %q", why)
	}
}
