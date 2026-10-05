package toho

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 見積依頼の記録が無い返事から見積依頼書ページを作る（2026-10-05・移行期——rfq_reply_make.go）。Gemini は呼びません（読んだままの表を読み替えるだけ）。

func TestReplyColumnOf(t *testing.T) {
	for h, want := range map[string]string{
		"詳 細": "item_name", "品　名": "item_name", "数 量": "quantity", "単位": "unit", "単 価": "cost", "金 額": "", "合計": "",
		"品番": "item_id", "図番": "item_id", "備考": "note", "材質": "material", "寸法": "size", "何か": "",
		"金額（単価×数量）": "", "合計単価": "",
	} {
		if got := replyColumnOf(h); got != want {
			t.Errorf("%q → %q（%q のはず）", h, got, want)
		}
	}
}

// seedReplyWithoutRFQ は、加工製品ページ2枚（品番 A100-B01-01・K120-77-3_rev0）と、見積依頼の記録の無い返事ページを作ります。
func seedReplyWithoutRFQ(t *testing.T) (reply, p1, p2 string) {
	t.Helper()
	product := func(title, code string) string {
		id, err := cms.CreateChildPage(rfqReplyBox, "root", `<h1>`+title+`</h1><dl data-type="tags"><dt>品番</dt><dd>`+code+`</dd><dt>`+
			DrawingNoTag+`</dt><dd>`+code+`</dd></dl>`)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	p1, p2 = product("ローラー", "A100-B01-01"), product("アーム", "K120-77-3_rev0")
	mail := newReplyMail(t, "御見積書", "")
	row := func(cells ...string) string { return "<tr><td>" + strings.Join(cells, "</td><td>") + "</td></tr>" }
	var err error
	reply, err = cms.CreateChildPage(mail, "root", `<h1>見積依頼の返事　わかば鋼業</h1><dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>わかば鋼業</dd><dt>`+
		RFQAnsweredTag+`</dt><dd>2026-08-25</dd><dt>`+RFQReplyReadNoTag+`</dt><dd>1104</dd><dt>`+RFQReplyLinkTag+`</dt><dd><br/></dd><dt>`+
		SourceRefTag+`</dt><dd>`+mail+`-rp01</dd></dl>`+cms.ViewMarkerHTML(RFQReplyMatchViewType)+
		`<details open><summary>`+rfqReplySourceCaption+`</summary><table><tbody><tr><th>詳 細</th><th>数 量</th><th>単位</th><th>単 価</th><th>金 額</th></tr>`+
		row("ローラー<br>A100-B01-01/φ12*47", "5", "", "6,800", "34,000")+row("アーム K120-77-3_rev0/SS400", "1", "", "43,000", "43,000")+
		row("", "20", "", "6,400", "128,000")+row("合計", "", "", "", "205,000")+row("不明の品 ZZ-9999", "2", "", "100", "200")+
		`</tbody></table></details>`)
	if err != nil {
		t.Fatal(err)
	}
	return reply, p1, p2
}

func getDraft(t *testing.T, reply string) (int, []rfqReplyDraftRow) {
	t.Helper()
	req := auth.WithUser(httptest.NewRequest("GET", "/api/rfq-reply/draft?page_id="+reply, nil), rfqReplyRoot)
	rr := httptest.NewRecorder()
	RFQReplyDraftAPIHandler(rr, req)
	var out struct {
		Rows []rfqReplyDraftRow `json:"rows"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.Rows
}

// TestRFQReplyDraftAndMake は、読んだままの表を見積依頼明細の列に読み替えた案（弊社品番は図面番号で当てる・品名の無い行は上の行の
// 数量違い・合計の行は入れない）と、その案で見積依頼書ページを作り（回答あり・単価・回答日と見積依頼日・見積依頼／年／月）、返事を
// 結んでその子へ移し、加工製品ページの「💴 見積回答」に並ぶ——を固定します。
func TestRFQReplyDraftAndMake(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	reply, p1, p2 := seedReplyWithoutRFQ(t)

	code, rows := getDraft(t, reply)
	if code != 200 || len(rows) != 4 {
		t.Fatalf("案が %d・%d 行です（合計の行を除いて4行）: %+v", code, len(rows), rows)
	}
	want := []struct{ pid, itemID, qty, cost string }{
		{p1, "A100-B01-01", "5", "6800"}, {p2, "K120-77-3_rev0", "1", "43000"}, {p2, "K120-77-3_rev0", "20", "6400"}, {"", "", "2", "100"},
	}
	for i, w := range want {
		r := rows[i]
		if r.ProductID != w.pid || r.ItemID != w.itemID || r.Quantity != w.qty || r.Cost != w.cost {
			t.Errorf("%d行目が %+v です（弊社品番 %s・品番 %s・数量 %s・単価 %s のはず）", i+1, r.ourOrderLine, w.pid, w.itemID, w.qty, w.cost)
		}
	}
	if !strings.Contains(rows[2].Note, "上の行と同じ品物") || rows[2].ItemName != rows[1].ItemName {
		t.Errorf("品名の無い行が上の行の数量違いになっていません: %+v", rows[2])
	}
	if rows[0].ProductTitle != "ローラー" {
		t.Errorf("当てた加工製品の題が %q です", rows[0].ProductTitle)
	}
	if rows[0].ItemName != "ローラー A100-B01-01/φ12*47" {
		t.Errorf("セルの中の改行を空白1つに詰めていません: %q", rows[0].ItemName)
	}

	lines := make([]ourOrderLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, r.ourOrderLine)
	}
	code, out := postRFQ(t, rfqReplyRoot, RFQReplyMakeRFQAPIHandler, map[string]any{"page_id": reply, "rows": lines})
	rfq, _ := out["page_id"].(string)
	if code != 200 || rfq == "" {
		t.Fatalf("見積依頼書ページを作れません: %d %v", code, out)
	}
	body := readPageBody(t, rfq)
	for _, s := range []string{"<dt>" + SupplierTag + "</dt><dd>わかば鋼業</dd>", "<dt>" + RFQAnsweredTag + "</dt><dd>2026-08-25</dd>",
		"<dt>" + RFQDateTag + "</dt><dd>2026-08-25</dd>", ">6800<", ">43000<", ">6400<", ">" + rfqLineAnswered + "<", "/" + reply} {
		if !strings.Contains(body, s) {
			t.Errorf("見積依頼書ページに %q がありません:\n%s", s, body)
		}
	}
	meta, _ := page.ReadSidecar(rfq)
	month, _ := page.ReadSidecar(meta.ParentID)
	if cms.PageTitleByID(pageNum(meta.ParentID)) != "08月" || cms.PageTitleByID(pageNum(month.ParentID)) != "2026年" {
		t.Errorf("置き場が 見積依頼／2026年／08月 ではありません")
	}
	if parentOf(t, reply) != rfq || !strings.Contains(readPageBody(t, reply), "<dt>"+RFQReplyLinkTag+"</dt><dd>"+rfq+"</dd>") {
		t.Errorf("返事が見積依頼書に結ばれていません（親 %s）", parentOf(t, reply))
	}
	if q := quotesForProduct(database.DB, rfqReplyRoot, pageNum(p2)); len(q) != 2 {
		t.Errorf("アームの見積回答が %d 行です（1個と20個の2行）: %+v", len(q), q)
	}

	// もう結んだ返事からは作らない・案も出さない。
	if code, _ := postRFQ(t, rfqReplyRoot, RFQReplyMakeRFQAPIHandler, map[string]any{"page_id": reply, "rows": lines}); code != 409 {
		t.Errorf("結んだ返事からもう一度作ろうとして %d（409 のはず）", code)
	}
	if code, _ := getDraft(t, reply); code != 409 {
		t.Errorf("結んだ返事の案を出そうとして %d（409 のはず）", code)
	}
}

// TestRFQReplyMakeRefuses は、単価の入った行が無い・弊社品番がページ番号でない・返事ページでない、では作らないことを固定します。
func TestRFQReplyMakeRefuses(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	reply, _, _ := seedReplyWithoutRFQ(t)
	for _, c := range []struct {
		page string
		rows []ourOrderLine
		want int
	}{
		{reply, []ourOrderLine{{ItemName: "ローラー", Quantity: "5"}}, 400},
		{reply, []ourOrderLine{{ProductID: "ローラー", ItemName: "ローラー", Cost: "100"}}, 400},
		{rfqReplyBox, []ourOrderLine{{ItemName: "ローラー", Cost: "100"}}, 400},
	} {
		if code, out := postRFQ(t, rfqReplyRoot, RFQReplyMakeRFQAPIHandler, map[string]any{"page_id": c.page, "rows": c.rows}); code != c.want {
			t.Errorf("%+v で %d %v（%d のはず）", c.rows, code, out, c.want)
		}
	}
	parent := parentOf(t, reply)
	if !strings.Contains(readPageBody(t, reply), "<dt>"+RFQReplyLinkTag+"</dt><dd><br/></dd>") || cms.PageTagValue(database.DB, pageNum(parent), SupplierTag) != "" {
		t.Errorf("断ったのに返事が結ばれたか、見積依頼書の下へ動きました（親 %s）", parent)
	}
	if v := rfqReplyMatchViewHTML(rfqReplyRoot, pageNum(reply)); !strings.Contains(v, `class="chip-btn rfq-reply-make"`) {
		t.Errorf("結ばれていない返事ページに「この返事から見積依頼書ページを作る」がありません:\n%s", v)
	}
}

// TestProductByCodeInTextNeedsOne は、同じ番号の加工製品ページが2枚あれば弊社品番を当てない（人が選ぶ）ことを固定します。
func TestProductByCodeInTextNeedsOne(t *testing.T) {
	setupExtTest(t, rfqReplyBox, page.PageMeta{Owner: "root", Mode: "330"})
	for _, title := range []string{"台A", "台B"} {
		if _, err := cms.CreateChildPage(rfqReplyBox, "root", `<h1>`+title+`</h1><dl data-type="tags"><dt>品番</dt><dd>D200-11</dd></dl>`); err != nil {
			t.Fatal(err)
		}
	}
	one, err := cms.CreateChildPage(rfqReplyBox, "root", `<h1>台C</h1><dl data-type="tags"><dt>`+DrawingNoTag+`</dt><dd>D200-12</dd></dl>`)
	if err != nil {
		t.Fatal(err)
	}
	if pid, _ := productByCodeInText(rfqReplyRoot, "台 D200-11/SS400"); pid != "" {
		t.Errorf("同じ品番が2枚あるのに %s を当てました", pid)
	}
	if pid, code := productByCodeInText(rfqReplyRoot, "台 D200-12/SS400"); pid != one || code != "D200-12" {
		t.Errorf("図面番号の1枚を当てていません: %s %s（%s のはず）", pid, code, one)
	}
}
