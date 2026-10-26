package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 製造ページの作業（2026-10-21 夕・mfg_work.go）。

// TestPlaceUnderHeading は、表を h2 の節の末尾（次の h1・h2 の前）へ置き、見出しが無ければ末尾に見出しを足して置くことを固定します。
func TestPlaceUnderHeading(t *testing.T) {
	body := `<h1>製造</h1><h2>作業開始</h2><p>明細</p><h2>発注作業</h2><p>前からの表</p><h2>納品作業</h2><p>納品</p>`
	got := placeUnderHeading(body, MfgHeadOrder, `<p>新しい表</p>`)
	if !strings.Contains(got, `<p>前からの表</p><p>新しい表</p><h2>納品作業</h2>`) {
		t.Errorf("⚠ 発注作業の節の末尾に置いていません:\n%s", got)
	}
	got = placeUnderHeading(`<h1>製造</h1><h2>作業開始</h2><p>明細</p>`, MfgHeadQuote, `<p>見積の表</p>`)
	if !strings.HasSuffix(got, `<h2>見積依頼作業</h2><p>見積の表</p>`) {
		t.Errorf("⚠ 見出しが無いとき、末尾に見出しを足して置いていません:\n%s", got)
	}
	if got := ensureStartHeading(`<h1>製造</h1><dl></dl><table><caption>製造明細表</caption><tbody><tr><th>案件</th></tr></tbody></table>`); !strings.Contains(got, `</dl><h2>作業開始</h2><table>`) {
		t.Errorf("⚠ 製造明細表の前に「作業開始」がありません:\n%s", got)
	}
}

// seedMfgWork は製造ページ 33（製造明細 2行——ブラケット2台・弊社品番の無い行）を置きます（材料は seedProcurement の加工製品 000031: FB）。
func seedMfgWork(t *testing.T) string {
	t.Helper()
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 33, 0, "ブラケット", "root", "302", true)
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl><h2>作業開始</h2>` +
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>製造完了</th></tr>` +
		`<tr><td>000031</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td><td></td></tr>` +
		`<tr><td></td><td>000030</td><td>K-2</td><td>弊社品番の無い行</td><td>5</td><td></td></tr></tbody></table>`
	writeBodyFile(t, 33, body)
	syncBody(t, 33, body)
	return "000033"
}

// TestMfgWorkPartsOrderQuoteSkip は、
//   - 製造明細表の中に操作は無く、行の先頭にチェック・表の下に帯・ページの末尾に「この製造の書類」
//   - 「部材」で、選んだ行のぶんの必要部材表が「部材作業」の下にでき、押し直しても1枚（作り直す）
//   - 「見積もり」は見積依頼部材表へ写し（ロットつき）必要部材表に残す、「発注」は発注部材表へ移す
//
// ことを固定します。
func TestMfgWorkPartsOrderQuoteSkip(t *testing.T) {
	mfg := seedMfgWork(t)
	u := adminUser()
	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+mfg, nil), u), 33, readPageBody(t, mfg))
	for _, want := range []string{`class="mfg-pick" aria-label="この行を選ぶ" data-row="0" data-item="K-1"`, `class="chip-btn mfg-act-parts"`, `class="chip-btn mfg-act-dlv"`, `mfg-page-end`, "この製造の書類"} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 製造ページに %q がありません:\n%s", want, view)
		}
	}
	if i, j := strings.Index(view, "mfg-act-parts"), strings.Index(view, "</table>"); i < j {
		t.Errorf("⚠ ボタンが表の中にあります")
	}

	for i := 0; i < 2; i++ { // 押し直しても必要部材表は1枚
		if code, out := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": []map[string]any{{"row": 0, "item": "K-1"}}}); code != 200 {
			t.Fatalf("部材を集められません: %d %v", code, out)
		}
	}
	body := shapeOf(readPageBody(t, mfg))
	if strings.Count(body, "<caption>必要部材表</caption>") != 1 || !strings.Contains(body, "<h2>部材作業</h2><table><caption>必要部材表</caption>") {
		t.Fatalf("⚠ 必要部材表が「部材作業」の下に1枚ありません:\n%s", body)
	}
	if code, _ := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": []map[string]any{{"row": 0, "item": "違う品番"}}}); code != 400 {
		t.Errorf("⚠ 品番の合わない行で部材を集めています")
	}

	if code, out := postRFQ(t, u, MfgNeedsAPIHandler, map[string]any{"page_id": mfg, "rows": []int{1}, "action": "quote"}); code != 200 {
		t.Fatalf("見積もりへ写せません: %d %v", code, out)
	}
	body = readPageBody(t, mfg)
	if !strings.Contains(body, "<h2>見積依頼作業</h2>") || !strings.Contains(body, "<caption>見積依頼部材表</caption>") || !strings.Contains(body, "<caption>必要部材表</caption>") {
		t.Fatalf("⚠ 見積依頼部材表が「見積依頼作業」の下にできていないか、必要部材表が消えました:\n%s", body)
	}
	if code, out := postRFQ(t, u, MfgNeedsAPIHandler, map[string]any{"page_id": mfg, "rows": []int{1}, "action": "order"}); code != 200 {
		t.Fatalf("発注へ移せません: %d %v", code, out)
	}
	body = readPageBody(t, mfg)
	if !strings.Contains(body, "<h2>発注作業</h2>") || !strings.Contains(body, "<caption>発注部材表</caption>") || strings.Contains(body, "<caption>必要部材表</caption>") {
		t.Errorf("⚠ 発注部材表が「発注作業」の下にできていないか、必要部材表に行が残っています:\n%s", body)
	}
	// 発注部材表に入れたぶんは手配済み——もう一度「部材」を押しても集まらない。
	if code, _ := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": []map[string]any{{"row": 0, "item": "K-1"}}}); code != 400 {
		t.Errorf("⚠ 発注部材表に入れた部材を、また集めています")
	}
}


// TestGrossQuoteLinesDoNotSubtractArranged は、見積もりへ写す数が手配済みを引かない数になることを固定します（2026-10-21 夜）。
//
// > 2026-10-21 利用者:「手配済みをひきません」
//
//   - 必要部材表の数が手配済みを引いた 1 でも、見積依頼部材表へは「作る数 2 × 1個あたり 1」の 2 を写す
//   - 内訳のある行は、内訳の数も直す
//   - 部材が読めない行（人が寸法を直した行）は、表の数のまま
//   - 備考の「追加」は外す
func TestGrossQuoteLinesDoNotSubtractArranged(t *testing.T) {
	mfg := seedMfgWork(t)
	u := adminUser()
	fb := ourOrderLine{ProductID: "000031", ForOrder: "000030", Kind: "材料", Material: "鉄", Shape: "FB", Size: "t4.5*75*1090", Quantity: "1", Note: ExtraNote}
	withBreakdown := fb
	withBreakdown.Note = ""
	withBreakdown.Breakdown = "000030/000031:1"
	edited := fb
	edited.Size = "t4.5*75*999"
	got := grossQuoteLines(u, pageNum(mfg), []ourOrderLine{fb, edited})
	if len(got) != 2 || got[0].Quantity != "2" || got[0].Note != "" {
		t.Fatalf("⚠ 見積もりの数が手配済みを引いたままか、備考の「追加」が残っています: %+v", got)
	}
	if got[1].Quantity != "1" {
		t.Errorf("⚠ 部材の読めない行の数を変えています: %q", got[1].Quantity)
	}
	if got := grossQuoteLines(u, pageNum(mfg), []ourOrderLine{withBreakdown}); got[0].Quantity != "2" || got[0].Breakdown != "000030/000031:2" {
		t.Errorf("⚠ 内訳のある行の数・内訳が直っていません: %q %q", got[0].Quantity, got[0].Breakdown)
	}
	// ふだんの行と「追加」の行を両方選んでも、同じ部材×案件×加工製品は1行だけ（どちらも作る数の全部になり、倍に写っていた・2026-10-22 の見直し）。
	if got := grossQuoteLines(u, pageNum(mfg), []ourOrderLine{fb, withBreakdown}); len(got) != 1 || got[0].Quantity != "2" {
		t.Errorf("⚠ 同じ部材を2行写しています: %+v", got)
	}
	// 材料の行に品名が入っていても（移した古いページ）、加工製品の材料表の行と合わせる。
	named := fb
	named.ItemName = "鉄板"
	if got := grossQuoteLines(u, pageNum(mfg), []ourOrderLine{named}); got[0].Quantity != "2" {
		t.Errorf("⚠ 品名のある材料の行を、加工製品の材料表と合わせられません: %q", got[0].Quantity)
	}
}

// TestMfgWorkDeliveryDraft は、
//   - 製造ページを作ると、製造明細表は見出し「作業開始」の下
//   - 「納品」で、選んだ行のまだ納めていない数の納品表が「納品作業」の下にできる
//   - 納品表から作った納品書（from_draft）のあとで、納品表が消える
//
// ことを固定します。
func TestMfgWorkDeliveryDraft(t *testing.T) {
	setupExtTest(t, "000980", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 981, -1, "案件", "alice", "302", true)
	seedOrder(t, 982, 981, "あけぼの精工", "2026-11-01", item("A-1", "ブラケット", "100", "2026-11-01", ""))
	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000982", "row": 0, "item": "A-1", "qty": "40"}}})
	if code != 200 {
		t.Fatalf("製造ページを作れません: %d %v", code, out)
	}
	mfg := out["page_id"].(string)
	if body := readPageBody(t, mfg); !strings.Contains(body, "<h2>作業開始</h2><table><caption>製造明細表</caption>") {
		t.Errorf("⚠ 製造明細表が見出し「作業開始」の下にありません:\n%s", body)
	}
	if code, out := postRFQ(t, u, MfgDeliveryDraftAPIHandler, map[string]any{"page_id": mfg, "items": []map[string]any{{"row": 0, "item": "A-1"}}}); code != 200 {
		t.Fatalf("納品表を作れません: %d %v", code, out)
	}
	body := readPageBody(t, mfg)
	if !strings.Contains(body, "<h2>納品作業</h2><table><caption>納品表</caption>") || !strings.Contains(body, "<td>A-1</td><td>ブラケット</td><td>40</td>") {
		t.Fatalf("⚠ 納品表（A-1 を 40）が「納品作業」の下にありません:\n%s", body)
	}
	// 人が納品表の数量を 40 から 15 に直す（画面は「納品書を作る」を送る前に保存する）。
	if err := cms.RewriteBody(mfg, "alice", func(cur string) string {
		return strings.Replace(cur, "<td>ブラケット</td><td>40</td>", "<td>ブラケット</td><td>15</td>", 1)
	}); err != nil {
		t.Fatal(err)
	}
	pick15 := []map[string]any{{"order": "000982", "row": -1, "item": "A-1", "name": "ブラケット", "qty": "15"}}
	// 画面の表と本文の納品表が違えば（ほかの人が作り直した・直した）作らない。
	if code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": mfg, "from_draft": true,
		"items": []map[string]any{{"order": "000982", "row": -1, "item": "A-1", "name": "ブラケット", "qty": "40"}}}); code != 409 {
		t.Errorf("⚠ 本文の納品表（15）と違う画面の表（40）で納品書を作りました: %d %v", code, out)
	}
	code, out = postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": mfg, "from_draft": true, "items": pick15})
	if code != 200 {
		t.Fatalf("納品書を作れません: %d %v", code, out)
	}
	if body := readPageBody(t, mfg); strings.Contains(body, "<caption>納品表</caption>") {
		t.Errorf("⚠ 納品書を作ったのに納品表が残っています:\n%s", body)
	}
}

// TestMfgPartsWarnsWhenStarted は、「🧾 部材作業」が、もう部材を手配した行を確かめで返し（check）、
// 「追加の発注」を選んだときは、その行だけ手配済みを引かずに全部を備考「追加」で集めることを固定します（2026-10-23）。
//
// > 2026-10-23 利用者:「製造明細表の下にある「追加の発注」ぼ異端は無くし、開始済みの部材を作業開始すると警告するようにします。」
func TestMfgPartsWarnsWhenStarted(t *testing.T) {
	mfg := seedMfgWork(t)
	syncBody(t, 32, `<h1>発注 みなと商店</h1>`) // seedProcurement の前からの部材発注（板 6）を外す——何も手配していない形から始める
	u := adminUser()
	pick := []map[string]any{{"row": 0, "item": "K-1"}}
	startedOf := func() []any {
		t.Helper()
		before := readPageBody(t, mfg)
		code, out := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": pick, "check": true})
		if code != 200 {
			t.Fatalf("確かめられません: %d %v", code, out)
		}
		if readPageBody(t, mfg) != before {
			t.Fatalf("⚠ 確かめだけなのに本文を書き換えています")
		}
		l, _ := out["started"].([]any)
		return l
	}
	if l := startedOf(); len(l) != 0 {
		t.Fatalf("⚠ まだ何も手配していない行を「始めた」と言っています: %v", l)
	}
	// 部材を集めて発注部材表へ入れる（手配済みになる）。
	if code, out := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": pick}); code != 200 {
		t.Fatalf("部材を集められません: %d %v", code, out)
	}
	if code, out := postRFQ(t, u, MfgNeedsAPIHandler, map[string]any{"page_id": mfg, "rows": []int{1}, "action": "order"}); code != 200 {
		t.Fatalf("発注へ移せません: %d %v", code, out)
	}
	l := startedOf()
	if len(l) != 1 || !strings.Contains(stringOf(l[0]), "ブラケット") {
		t.Fatalf("⚠ もう手配した行を返していません: %v", l)
	}
	// 追加の発注を選ぶと、手配済みを引かずに全部（2台 × FB）を備考「追加」で。
	if code, out := postRFQ(t, u, MfgPartsAPIHandler, map[string]any{"page_id": mfg, "items": pick, "extra": true}); code != 200 {
		t.Fatalf("追加の発注で集められません: %d %v", code, out)
	}
	body := readPageBody(t, mfg)
	at := strings.Index(body, "<caption>必要部材表</caption>")
	if at < 0 || !strings.Contains(body[at:], ">"+ExtraNote+"<") {
		t.Errorf("⚠ 追加の発注の行（備考「%s」）が必要部材表にありません:\n%s", ExtraNote, body)
	}
}

// stringOf は確かめの答えの1件（{row, name}）を文字にします。
func stringOf(v any) string {
	if m, ok := v.(map[string]any); ok {
		s, _ := m["name"].(string)
		return s
	}
	return ""
}

// TestEnsureCalcHeading は、見出し「見積もり計算作業」が無ければ末尾に足し、あれば（data-id つきでも）足さないことを固定します（2026-10-23）。
func TestEnsureCalcHeading(t *testing.T) {
	got := ensureCalcHeading(`<h1>製造</h1><h2>作業開始</h2><h2>納品作業</h2>`)
	if !strings.HasSuffix(got, `<h2>納品作業</h2><h2>見積もり計算作業</h2>`) {
		t.Errorf("⚠ 納品作業の下に「見積もり計算作業」がありません:\n%s", got)
	}
	in := `<h1>製造</h1><h2 data-id="ab12">見積もり計算作業</h2>`
	if got := ensureCalcHeading(in); got != in {
		t.Errorf("⚠ もうある見出しを2つにしています:\n%s", got)
	}
}

// TestEnsureStartHeadingSeesHeadingWithID は、見出し「作業開始」に data-id が付いていても（人が保存した・機械が ID を入れた）
// もう1つ足さないことを固定します（2026-10-26）。
func TestEnsureStartHeadingSeesHeadingWithID(t *testing.T) {
	body := `<h1 data-id="aaaa">製造</h1><h2 data-id="bbbb">` + MfgHeadStart + `</h2>` + "\n" +
		`<table data-id="cccc"><caption>製造明細表</caption><tbody><tr><th>品番</th></tr></tbody></table>`
	if got := ensureStartHeading(body); strings.Count(got, MfgHeadStart) != 1 {
		t.Errorf("⚠ ID の付いた見出しを見落として、もう1つ足しました:\n%s", got)
	}
}

// TestDropOnlyTheDraftThatWasDelivered は、納品書を作ったあとに消す納品表が、送った行と同じ中身のものだけであることを固定します
// （2026-10-26 夕——それまでは納品表を全部消していたので、作っているあいだにほかの人が作り直した納品表まで消えた）。
func TestDropOnlyTheDraftThatWasDelivered(t *testing.T) {
	draft := func(qty string) string {
		return deliveryDraftHTML([]map[string]string{{"our-item-id": "000777", "order": "000982", "item-id": "A-1", "item-name": "ブラケット", "quantity": qty, "unit": "個"}})
	}
	picks := []deliveryPick{{Order: "/000982", Item: "A-1", Name: "ブラケット", Qty: "15"}}
	match := func(t *html.Node) bool { return draftMatchesPicks(t, picks) }
	body := "<h2>" + MfgHeadDelivery + "</h2>" + draft("15")
	if out, n := dropTablesWhere(body, MfgDeliveryDraftType, match); n != 1 || strings.Contains(out, "<caption>納品表</caption>") {
		t.Errorf("⚠ 送った行と同じ納品表を消していません（%d）:\n%s", n, out)
	}
	rebuilt := "<h2>" + MfgHeadDelivery + "</h2>" + draft("25")
	if out, n := dropTablesWhere(rebuilt, MfgDeliveryDraftType, match); n != 0 || !strings.Contains(out, "<td>25</td>") {
		t.Errorf("⚠ ほかの人が作り直した納品表（25）を消しました（%d）:\n%s", n, out)
	}
	// 数量の空いた行は、画面も送らない——数えない。
	withBlank := deliveryDraftHTML([]map[string]string{{"order": "000982", "item-id": "A-1", "item-name": "ブラケット", "quantity": "15"}, {"order": "000982", "item-id": "A-2", "item-name": "カバー", "quantity": ""}})
	if _, n := dropTablesWhere(withBlank, MfgDeliveryDraftType, match); n != 1 {
		t.Errorf("⚠ 数量の空いた行があると同じ納品表と見ません")
	}
}
