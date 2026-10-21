package toho

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 製造ページから部材見積依頼・見積計算書を作る（2026-10-19・mfg_more.go）。

// TestMfgPartLinesCarryLot は、製造の部材を集めるとき、ロット＝製造明細の数量・案件・弊社品番を運ぶことを固定します（2026-10-21 夕——
// 必要部材表の「📨 見積もり」が見積依頼部材表へ写すのにロットが要る）。弊社品番の無い行は飛ばす。
func TestMfgPartLinesCarryLot(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 33, 0, "ブラケット", "root", "302", true)
	syncBody(t, 33, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`+
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td></tr>`+
		`<tr><td></td><td>000030</td><td>K-2</td><td>弊社品番の無い行</td><td>5</td></tr></tbody></table>`)
	lines, skipped, err := mfgPartLines(&auth.User{Username: "root", IsAdmin: true}, 33, nil)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("⚠ 飛ばした行が %d です（弊社品番の無い1行のはず）", skipped)
	}
	if len(lines) == 0 {
		t.Fatalf("⚠ 部材が集まりません")
	}
	for _, ln := range lines {
		if ln.Lot != "2" || ln.ForOrder != "000030" || ln.ProductID != "000031" {
			t.Errorf("⚠ 行が違います（ロット2・案件 000030・弊社品番 000031 のはず）: %+v", ln)
		}
	}
}

// TestRFQDocAndCalcUnderMfgPage は、
//   - 製造ページの見積依頼部材表から作った部材見積依頼書が、その製造ページの子にできる
//   - 製造ページの「見積計算」の欄から作った見積計算書が、その製造ページの子にでき、欄にリンクが出る
//   - 製造ページでないページを mfg に渡すと断る
//
// ことを固定します。
func TestRFQDocAndCalcUnderMfgPage(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 991, -1, "ブラケット", "alice", "302", true)
	seedLossProduct(t, 992, -1) // 加工製品（見積表あり）
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl>` +
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th></tr>` +
		`<tr><td>000992</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td></tr></tbody></table>` +
		tableOfLinesHTML(RFQDraftType, []ourOrderLine{{ProductID: "000992", ForOrder: "000030", Material: "鉄", Shape: "FB", Size: "t4.5*75*1090", Quantity: "2", Lot: "2"}}, true)
	writeBodyFile(t, 991, body)
	if err := cms.SyncIndex("000991", body); err != nil {
		t.Fatal(err)
	}

	code, out := postRFQ(t, u, RFQNewDocAPIHandler, map[string]any{"page_id": "000991", "table": 1, "supplier": "みなと商店"})
	if code != 200 {
		t.Fatalf("部材見積依頼書を作れません: %d %v", code, out)
	}
	// 2026-10-21 から、置き場は記録で、明細の「製造」の列にこの製造ページを書く。
	if got, want := placeOf(out["page_id"].(string)), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("⚠ 部材見積依頼書の置き場が %s です（%s のはず）", got, want)
	}
	if body := readPageBody(t, out["page_id"].(string)); !strings.Contains(body, "<td>000030</td><td>000991</td>") {
		t.Errorf("⚠ 部材見積依頼明細表の「製造」に 000991 がありません:\n%s", body)
	}

	if code, _ := postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": "000992", "mfg": "000992"}); code != 400 {
		t.Errorf("⚠ 製造ページでないページの子に見積計算書を作れます: %d", code)
	}
	code, out = postRFQ(t, u, EstimateCalcNewAPIHandler, map[string]any{"product": "000992", "mfg": "000991"})
	if code != 200 {
		t.Fatalf("見積計算書を作れません: %d %v", code, out)
	}
	calc := out["page_id"].(string)
	if got, want := placeOf(calc), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("⚠ 見積計算書の置き場が %s です（%s のはず）", got, want)
	}
	if got := cms.PageTagValue(database.DB, pageNum(calc), MfgRefTag); got != "000991" {
		t.Errorf("⚠ 見積計算書のタグ「製造」が %q です（000991 のはず）", got)
	}
	got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000991", nil), u), 991, readPageBody(t, "000991"))
	// 2026-10-21 夕から、表の中に操作は置かない——見積計算書は「この製造の書類」に並び、作るボタンは表の下の帯（選んだ行の加工製品）。
	for _, bad := range []string{"見積計算</th>", "mfg-rfq-go", "mfg-dlv-pick", "mfg-back-go"} {
		if strings.Contains(got, bad) {
			t.Errorf("⚠ 製造ページに、もう無い %q があります", bad)
		}
	}
	for _, want := range []string{`<a href="/` + calc + `">`, `class="chip-btn mfg-act-calc"`, `data-product="000992"`} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 製造ページに %q がありません:\n%s", want, got)
		}
	}
}
