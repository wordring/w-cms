package toho

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 見積計算書（2026-10-16・estimate_calc_doc.go）——利用者:「新しい見積計算書を作るボタンから始めてみましょう」
// 「見積もり計算の記録は製造記録傘下になると思います」。

// TestEstimateCalcNewCopiesLatest は、
//   - 1枚目は加工製品ページの「見積もり計算」の節（次の H2 の手前まで）を写す・加工製品ページは変えない
//   - 置き場は 記録／年／月／製造記録（作った日）・題は【弊社】見積計算書　<品物>・タグは ページ種類・弊社品番・見積計算日
//   - 2枚目はいちばん新しい見積計算書を写す
//   - 加工製品ページの「🧮 見積計算書」の鏡は、新しい順に並べ、ロットと単価を出す
//
// ことを固定します。
func TestEstimateCalcNewCopiesLatest(t *testing.T) {
	setupExtTest(t, "000800", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 801, -1, CustomerBoxTitle, "alice", "302", true)
	productBody := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		`<h2>部材</h2><p>材料の話</p>` +
		`<h2>` + estimateSectionHeading + `</h2><h3>1個</h3>` +
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr><tr><td>材料</td><td>620</td><td></td></tr></tbody></table>` +
		`<table><caption>見積表</caption><tbody><tr><th>個数</th><th>表面処理</th><th>原価</th><th>利益率</th><th>単価</th><th>備考</th></tr>` +
		`<tr><td>1</td><td></td><td>620</td><td>10</td><td>682</td><td></td></tr></tbody></table>` +
		`<h2>写真</h2><p>写真の話</p>`
	addPage(t, 802, 801, "ブラケット", "alice", "302", true)
	writeBodyFile(t, 802, productBody)
	if err := cms.SyncIndex("000802", productBody); err != nil {
		t.Fatal(err)
	}

	code, out := postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": "000802"})
	if code != 200 || out["page_id"] == nil {
		t.Fatalf("作れません: %d %v", code, out)
	}
	first := out["page_id"].(string)
	body := readPageBody(t, first)
	for _, want := range []string{
		"<h1>【弊社】見積計算書　ブラケット</h1>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindOurEstimateCalc + "</dd>",
		"<dt>" + OurItemNoTag + "</dt><dd>000802</dd>",
		"<dt>" + EstimateCalcDateTag + "</dt><dd>" + time.Now().Format("2006-01-02") + "</dd>",
		"<h2>" + estimateSectionHeading + "</h2>", "<caption>見積計算表</caption>", "<td>682</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("見積計算書に %q がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "材料の話") || strings.Contains(body, "写真の話") {
		t.Errorf("「見積もり計算」の節の外まで写しています:\n%s", body)
	}
	if got, want := placeOf(first), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("置き場が %s です（%s のはず）", got, want)
	}
	if after := readPageBody(t, "000802"); after != productBody {
		t.Errorf("加工製品ページを変えています")
	}

	// 1枚目の単価を直して、2枚目はそれを写す（いちばん新しい見積計算書から）。
	edited := strings.Replace(body, "<td>682</td>", "<td>700</td>", 1)
	writeBodyFile(t, pageNum(first), edited)
	if err := cms.SyncIndex(first, edited); err != nil {
		t.Fatal(err)
	}
	_, out2 := postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": "000802"})
	second, _ := out2["page_id"].(string)
	if second == "" || out2["from"] != first || !strings.Contains(readPageBody(t, second), "<td>700</td>") {
		t.Errorf("2枚目がいちばん新しい見積計算書を写していません: %v", out2)
	}

	// 鏡——新しい順（同じ日ならページ番号の大きい方が先）・ロットと単価。
	list := estimateCalcsOf(u, 802)
	if len(list) != 2 || page.FormatID(list[0].PageID) != second || page.FormatID(list[1].PageID) != first {
		t.Fatalf("見積計算書の並びが違います: %+v", list)
	}
	html := estimateCalcsHTML(802, list)
	for _, want := range []string{"🧮 見積計算書", "最新:", `href="/` + second + `"`, `href="/` + first + `"`, "1個: <strong>700円</strong>", `data-ecalc-new="000802"`} {
		if !strings.Contains(html, want) {
			t.Errorf("鏡に %q がありません:\n%s", want, html)
		}
	}

	// 読む先（2026-10-17）——いちばん新しい見積計算書。
	if src := estimateSourceOf(u, 802); page.FormatID(src) != second {
		t.Errorf("読む先がいちばん新しい見積計算書ではありません: %d", src)
	}
	// 見積計算書の見積表から組む見積明細表の行は、弊社品番が加工製品ページ（見積計算書ではない）。
	opts, err := estimateOptionsOf(pageNum(second))
	if err != nil || len(opts) != 1 || opts[0].Line.ProductID != "000802" || opts[0].Line.Price != "700" {
		t.Errorf("見積計算書の見積表の行が違います: %+v %v", opts, err)
	}
	if lots := estimateLotsOf(estimateSourceOf(u, 802)); len(lots) != 1 || lots[0] != 1 {
		t.Errorf("見積計算書のロットが読めません: %v", lots)
	}
	// 「計算表へ」の読む口は、加工製品ページを渡されても見積計算書を返す（写す先）。
	rr := httptest.NewRecorder()
	EstimateRowsAPIHandler(rr, auth.WithUser(httptest.NewRequest("GET", "/api/estimate/rows?page_id=000802", nil), u))
	if !strings.Contains(rr.Body.String(), `"page_id":"`+second+`"`) {
		t.Errorf("計算表への写す先が見積計算書ではありません: %s", rr.Body.String())
	}

	// 加工製品ページでないページからは作らない。
	if code, _ := postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": first}); code != 400 {
		t.Errorf("見積計算書から見積計算書を作れてしまいます: %d", code)
	}
}
