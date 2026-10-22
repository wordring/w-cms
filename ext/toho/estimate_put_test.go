package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// 見積回答の単価を見積計算表へ写す（2026-10-05・estimate_put.go）——【要求】部材見積依頼 §4 の未決5「見積計算表へ写すボタン」。

// estimatePutBody は見積計算表2枚の加工製品ページです（組の見積表のロットは20と40）。
func estimatePutBody() string {
	table := func(lot, matNote string) string {
		return `<h3>ロット` + lot + `</h3><table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr>` +
			`<tr><td>材料</td><td>1200</td><td>` + matNote + `</td></tr>` +
			`<tr><td>塗装</td><td></td><td></td></tr></tbody></table>` + recordTable(lot)
	}
	return `<h1>ブラケット</h1>` + table("20", "見積 かなめ商会 2026-09-01／板取り4") + table("40", "")
}

// estimateRowsOf は index 枚目の表の「工程|価格|備考」を並べます。
func estimateRowsOf(t *testing.T, body string, index int) []string {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	tables := tablesOfType(nodes, estimateType)
	var out []string
	for _, tr := range rowsOf(tables[index])[1:] {
		out = append(out, strings.Join(cellTexts(tr), "|"))
	}
	return out
}

func TestWithEstimateCost(t *testing.T) {
	body := estimatePutBody()
	tables, err := estimateTablesOf(body)
	if err != nil || len(tables) != 2 || tables[0].Lot != "20" || tables[1].Lot != "40" {
		t.Fatalf("見積計算表の読みが違います: %+v %v", tables, err)
	}
	var steps []string
	for _, r := range tables[0].Rows {
		steps = append(steps, r.Step)
	}
	if strings.Join(steps, ",") != "材料,塗装" {
		t.Errorf("写せる行が %v です", steps)
	}

	// 既にある行へ——価格を置き換え、前に写した「見積 …」は置き換え、人の備考は残す。
	out, err := withEstimateCost(body, 0, "", "材料", false, "１，５００円", "見積 わかば鋼業 2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if got := estimateRowsOf(t, out, 0)[0]; got != "材料|1500|板取り4／見積 わかば鋼業 2026-10-05" {
		t.Errorf("材料の行が %q です", got)
	}
	out, err = withEstimateCost(out, 0, "", "塗装", false, "298", "見積 ふじ鍍金 2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if got := estimateRowsOf(t, out, 0)[1]; got != "塗装|298|見積 ふじ鍍金 2026-10-05" {
		t.Errorf("塗装の行が %q です", got)
	}
	if got := estimateRowsOf(t, out, 1)[0]; got != "材料|1200|" {
		t.Errorf("⚠ 2枚目の表まで変わっています: %q", got)
	}

	// 新しい行——表の末尾へ。
	out, err = withEstimateCost(out, 1, "", "メッキ", true, "300", "見積 ふじ鍍金 2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(estimateRowsOf(t, out, 1), " / "); got != "材料|1200| / 塗装|| / メッキ|300|見積 ふじ鍍金 2026-10-05" {
		t.Errorf("新しい行の場所が違います: %s", got)
	}

	// 断る——無い行（もう無いロット・単価の行も）・もうある行・数でない単価・無い表。
	for _, c := range []struct {
		index int
		step  string
		add   bool
		value string
	}{{0, "単価", false, "1"}, {0, "ロット", false, "1"}, {0, "曲げ", false, "1"}, {0, "材料", true, "1"}, {0, "材料", false, "要相談"}, {5, "材料", false, "1"}} {
		if _, err := withEstimateCost(body, c.index, "", c.step, c.add, c.value, "見積 x"); err == nil {
			t.Errorf("%+v は断るはず", c)
		}
	}
}

// TestEstimatePutCostAPIAndButton は口（書ける人・ページに書く）と、見積回答の「計算表へ」の出る条件を固定します。
func TestEstimatePutCostAPIAndButton(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 31, 0, "ブラケット", "root", "302", true)
	writeBodyFile(t, 31, estimatePutBody())
	root := &auth.User{Username: "root"}

	req := auth.WithUser(httptest.NewRequest("GET", "/api/estimate/rows?page_id=000031", nil), root)
	rr := httptest.NewRecorder()
	EstimateRowsAPIHandler(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"lot":"20"`) || !strings.Contains(rr.Body.String(), `"step":"塗装"`) {
		t.Errorf("見積計算表の行を返していません: %d %s", rr.Code, rr.Body.String())
	}
	code, out := postRFQ(t, root, EstimatePutCostAPIHandler, map[string]any{
		"page_id": "000031", "index": 0, "step": "塗装", "value": "298", "note": "見積 ふじ鍍金 2026-10-05"})
	if code != 200 {
		t.Fatalf("写せません: %d %v", code, out)
	}
	if got := estimateRowsOf(t, readPageBody(t, "000031"), 0)[1]; got != "塗装|298|見積 ふじ鍍金 2026-10-05" {
		t.Errorf("ページの塗装の行が %q です", got)
	}
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, EstimatePutCostAPIHandler, map[string]any{
		"page_id": "000031", "index": 0, "step": "材料", "value": "1"}); code != 403 {
		t.Errorf("書けない人が写せてしまう: %d", code)
	}

	list := []quoteRow{
		{RFQPage: 50, Date: "2026-10-05", Vendor: "ふじ鍍金", Kind: "外注加工", What: "塗装", Price: "298", Status: rfqLineAnswered},
		{RFQPage: 51, Date: "2026-10-04", Vendor: "わかば鋼業", Kind: "材料", What: "SS400 板", Status: rfqLineUnanswered},
	}
	html := quotesListHTML(list, "000031")
	if strings.Count(html, "rfq-quote-put\"") != 1 || !strings.Contains(html, `data-price="298"`) || !strings.Contains(html, `data-note="見積 ふじ鍍金 2026-10-05"`) {
		t.Errorf("単価のある行にだけ「計算表へ」（単価と出所つき）が出ていません:\n%s", html)
	}
	if strings.Contains(quotesListHTML(list, ""), "rfq-quote-put") {
		t.Errorf("書けない・見積計算表の無いページに「計算表へ」が出ています")
	}
	// 鏡から: このページの品物の見積（回答あり）があり、書ける人・見積計算表のあるページだけに「計算表へ」。
	addPage(t, 50, 0, "部材見積依頼　ふじ鍍金", "root", "302", true)
	writeBodyFile(t, 50, `<h1>部材見積依頼　ふじ鍍金</h1><dl data-type="tags"><dt>`+RFQNoTag+`</dt><dd>000050</dd><dt>`+SupplierTag+
		`</dt><dd>ふじ鍍金</dd><dt>`+RFQAnsweredTag+`</dt><dd>2026-10-05</dd></dl><table><caption>部材見積依頼明細表</caption><tbody>`+
		`<tr><th>弊社品番</th><th>種類</th><th>加工内容</th><th>数量</th><th>単価</th><th>状態</th></tr>`+
		`<tr><td>000031</td><td>外注加工</td><td>塗装</td><td>20</td><td>298</td><td>`+rfqLineAnswered+`</td></tr></tbody></table>`)
	show := func(u *auth.User) string {
		return cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000031", nil), u), 31,
			readPageBody(t, "000031")+`<section><h2>支給部品</h2><table><caption>支給部品表</caption><tbody><tr><th>品名</th><th>仕様</th><th>個数</th><th>備考</th></tr>`+
				`<tr><td></td><td></td><td></td><td></td></tr></tbody></table></section>`)
	}
	if got := show(root); !strings.Contains(got, `class="chip-btn rfq-quote-put"`) || !strings.Contains(got, "rfq-quote-put-panel") {
		t.Errorf("書ける人の見積回答に「計算表へ」と写す欄がありません:\n%s", got)
	}
	if got := show(&auth.User{Username: "bob"}); !strings.Contains(got, "298円") || strings.Contains(got, "rfq-quote-put") {
		t.Errorf("書けない人の見積回答に「計算表へ」が出ています（一覧は出る）:\n%s", got)
	}
}

// TestWithEstimateCostFollowsSig は、一覧を出したあとにほかの人が見積計算表を前へ足しても、写す先が一覧で選んだ表（指紋の
// 合う表）になることを固定します（2026-10-22 の見直し）。それまでは番号だけで選んでいて、別のロットの表に写していた。
// 指紋の合う表が無ければ断る。
func TestWithEstimateCostFollowsSig(t *testing.T) {
	body := estimatePutBody()
	tables, _ := estimateTablesOf(body)
	sig40 := tables[1].Sig
	// ほかの人が、いちばん前にロット10の組を足した（ロット40の表は 2 枚目→3 枚目）。
	moved := strings.Replace(body, `<h1>ブラケット</h1>`, `<h1>ブラケット</h1><h3>ロット10</h3><table><caption>見積計算表</caption><tbody>`+
		`<tr><th>工程</th><th>価格</th><th>備考</th></tr><tr><td>材料</td><td>900</td><td></td></tr></tbody></table>`+recordTable("10"), 1)
	out, err := withEstimateCost(moved, 1, sig40, "塗装", false, "298", "見積 ふじ鍍金 2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if got := estimateRowsOf(t, out, 2)[1]; !strings.HasPrefix(got, "塗装|298|") {
		t.Errorf("⚠ ロット40の表に写っていません: %q", got)
	}
	if got := estimateRowsOf(t, out, 1)[1]; strings.HasPrefix(got, "塗装|298") {
		t.Errorf("⚠ 番号だけでロット20の表に写しました: %q", got)
	}
	if _, err := withEstimateCost(moved, 1, "0123456789abcdef", "塗装", false, "298", "x"); err != errTableMoved {
		t.Errorf("指紋の合う表が無いのに断りません: %v", err)
	}
}

// TestPickBySig は、番号と指紋から表を選ぶ決まりを固定します。
func TestPickBySig(t *testing.T) {
	sigs := []string{"a", "b", "c", "b"}
	at := func(i int) string { return sigs[i] }
	cases := []struct {
		index int
		sig   string
		want  int
	}{
		{2, "c", 2},  // 合っている
		{0, "c", 2},  // ずれた——同じ指紋の1つへ
		{0, "b", -1}, // 同じ指紋が2つ——決められない
		{0, "z", -1}, // 無い
		{1, "", 1},   // 指紋を送らない古い画面——番号のまま
		{9, "", -1},
	}
	for _, c := range cases {
		if got := pickBySig(len(sigs), at, c.index, c.sig); got != c.want {
			t.Errorf("pickBySig(%d, %q) = %d（%d のはず）", c.index, c.sig, got, c.want)
		}
	}
}
