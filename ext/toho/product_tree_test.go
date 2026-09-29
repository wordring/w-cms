package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// TestWithTagValues は区分のタグを本文へ書く規則を固定します（2026-09-29）。
func TestWithTagValues(t *testing.T) {
	const page = `<h1>取付ベース</h1><dl data-type="tags"><dt>品番</dt><dd>K120-1</dd></dl>` +
		`<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>K120-1</dd></dl></section>`
	got := withTagValues(page, "区分", []string{"試作", "見積もり"}, false)
	// ページの並び（h1 の下）の最後へ足す——図面ブロックの中の並びへは混ぜない。
	want := `<dt>品番</dt><dd>K120-1</dd><dt>区分</dt><dd>試作</dd><dt>区分</dt><dd>見積もり</dd></dl><section>`
	if !strings.Contains(got, want) {
		t.Errorf("ページの並びの最後に足していません:\n%s", got)
	}
	// 同じものは2つにしない（間に空白があっても同じ組と見る）。
	spaced := strings.Replace(got, "<dt>区分</dt><dd>試作</dd>", "<dt>区分</dt>\n  <dd>試作</dd>", 1)
	if again := withTagValues(spaced, "区分", []string{"試作"}, false); again != spaced {
		t.Errorf("在る区分をもう一度足しました:\n%s", again)
	}
	// replace は印の外れた区分を消す（空欄の欄は残す）。
	if cut := withTagValues(got, "区分", []string{"見積もり"}, true); strings.Contains(cut, "<dd>試作</dd>") ||
		!strings.Contains(cut, "<dd>見積もり</dd>") {
		t.Errorf("外した区分が残っています:\n%s", cut)
	}
	// テンプレートが置いた空の欄へ入れる（欄が2つに増えない）。
	slot := `<h1>x</h1><dl data-type="tags"><dt>区分</dt><dd><br/></dd></dl>`
	if filled := withTagValues(slot, "区分", []string{"旧型"}, false); filled !=
		`<h1>x</h1><dl data-type="tags"><dt>区分</dt><dd>旧型</dd></dl>` {
		t.Errorf("空の欄へ入れていません: %s", filled)
	}
	// ページの並びが無ければ h1 の下に作る（図面ブロックの並びは使わない）。
	bare := `<h1>x</h1><section><dl data-type="tags"><dt>図面番号</dt><dd>1</dd></dl></section>`
	if made := withTagValues(bare, "区分", []string{"試作"}, false); !strings.HasPrefix(made,
		`<h1>x</h1><dl data-type="tags"><dt>区分</dt><dd>試作</dd></dl><section>`) {
		t.Errorf("h1 の下に並びを作っていません: %s", made)
	}
}

// TestFileDrawingsRejectsUnknownKind は、選択肢に無い区分で**動かさない**ことを固定します
// （「試作」と「試作品」が混ざると、一覧で絞ったときに静かに取りこぼすため）。
func TestFileDrawingsRejectsUnknownKind(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ", MachineName: "標準2輪", DrawingName: "取付ベース",
		Kinds: []string{"試作品"},
	}})
	if len(res) != 1 || res[0].Outcome != "skipped" || !strings.Contains(res[0].Message, "試作品") {
		t.Fatalf("選択肢に無い区分で動いています: %+v", res)
	}
}

// TestProductListShowsProductsAndKinds は「加工製品の一覧」が、箱の下の加工製品を
// **装置名称つきで1枚1行**に並べ、区分を絞り込み用の印に載せることを固定します。
func TestProductListShowsProductsAndKinds(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	file := func(r filingRequest) {
		t.Helper()
		res := postFiling(t, u, []filingRequest{r})
		if len(res) != 1 || (res[0].Outcome != "moved" && res[0].Outcome != "revision") {
			t.Fatalf("下ごしらえが失敗しました: %+v", res)
		}
	}
	a := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	file(filingRequest{PageID: a, Customer: "南北スポーツ", MachineName: "標準2輪", DrawingName: "取付ベース"})
	b := makeDrawingPage(t, inbox, "K120-2", "補強板", "φ320 共通台座", "南北スポーツ")
	file(filingRequest{PageID: b, Customer: "南北スポーツ", MachineName: "φ320 共通台座", DrawingName: "補強板",
		Kinds: []string{"試作", "見積もり"}})
	// 取付ベースの改定——旧版は最新版の子ページになる。**一覧には1行だけ**のはず。
	a2 := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1A", "取付ベース", "標準2輪", "南北スポーツ")
	file(filingRequest{PageID: a2, Customer: "南北スポーツ", MachineName: "標準2輪", DrawingName: "取付ベース",
		Merge: "revision"})

	boxID, _ := CustomerBoxPageID()
	custID, _ := findChildByTitle(boxID, "南北スポーツ")
	productsID, ok := findChildByTitle(custID, ProductsBoxTitle)
	if !ok {
		t.Fatal("加工製品の箱がありません")
	}
	host := mustAtoi(t, productsID)
	rows, err := productListRows(u, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("加工製品が2行ではありません（旧版が混ざっている？）: %+v", rows)
	}
	// 装置名称の順（φ320… が先）。
	if rows[0].Title != "補強板" || rows[0].Machine != "φ320 共通台座" ||
		strings.Join(rows[0].Kinds, "・") != "試作・見積もり" {
		t.Errorf("1行目が違います: %+v", rows[0])
	}
	if rows[1].Title != "取付ベース" || rows[1].Machine != "標準2輪" || len(rows[1].Kinds) != 0 ||
		rows[1].DrawingNo != "K120-1A" {
		t.Errorf("2行目が違います（改定後の図面番号のはず）: %+v", rows[1])
	}

	html := productListViewHTML(u, host)
	for _, want := range []string{
		`data-plist-form="1"`, `data-plist-kind="" checked`, `data-plist-kind="試作" checked`,
		`data-kinds="試作	見積もり"`, `<option value="標準2輪">`, `2 件`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("一覧に %q がありません:\n%s", want, html)
		}
	}
	// 社名の上（取引先の根）に置いても、その下を全部集める。
	if all, _ := productListRows(u, mustAtoi(t, boxID)); len(all) != 2 {
		t.Errorf("取引先の根から集めると %d 行です（2のはず）", len(all))
	}
}

// TestProductFolderAPI は、ワンノートの移植の道具が使う口が**整理と同じ木**を作り、
// 2回呼んでも増えないことを固定します。
func TestProductFolderAPI(t *testing.T) {
	setupFilingTest(t, "000012")
	u := &auth.User{Username: "alice"}
	call := func(customer, machine string) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"customer": customer, "machine": machine})
		req := auth.WithUser(httptest.NewRequest("POST", "/api/product-folder", bytes.NewReader(b)), u)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		ProductFolderAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	code, first := call("みなと商店", "標準2輪")
	if code != 200 || first["page_id"] == "" {
		t.Fatalf("作れません: %d %v", code, first)
	}
	if _, again := call("みなと商店", "標準2輪"); again["page_id"] != first["page_id"] {
		t.Errorf("2回目に別のページができました: %v / %v", first, again)
	}
	boxID, _ := CustomerBoxPageID()
	custID, _ := findChildByTitle(boxID, "みなと商店")
	productsID, _ := findChildByTitle(custID, ProductsBoxTitle)
	machID, ok := findChildByTitle(productsID, "標準2輪")
	if !ok || machID != first["page_id"] {
		t.Errorf("取引先／みなと商店／加工製品／標準2輪 になっていません: %v", first)
	}
	// 装置名称が空なら箱を返す（まとめを書く先などに使う）。
	if _, box := call("みなと商店", ""); box["page_id"] != productsID {
		t.Errorf("装置名称が空なのに箱を返しません: %v", box)
	}
	if code, _ := call("", "標準2輪"); code != 400 {
		t.Errorf("社名が空なのに %d です", code)
	}
}

