package toho

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// 見積依頼の段1（2026-10-03・rfq.go・rfq_api.go）——再見積依頼で集める・重複の赤・見積依頼部材表へ移す・不要・↩ 戻す。

const rfqProductID = 51
const rfqBoxID = 50

// seedRFQ は加工製品（材料2行・見積計算表2枚——ロット20とロット40）と、見積依頼の置き場を作ります。
func seedRFQ(t *testing.T) *auth.User {
	t.Helper()
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, rfqProductID, 0, "カバー", "root", "302", true)
	addPage(t, rfqBoxID, 0, RFQBoxTitle, "root", "302", true)
	writeBodyFile(t, rfqProductID, `<h1>カバー</h1>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>2</td></tr>`+
		`<tr><td>SUS304</td><td>丸棒</td><td>φ20*50</td><td>1</td></tr>`+
		`</tbody></table>`+
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>`+
		`<tr><td>ロット</td><td>20</td><td>個</td><td></td></tr><tr><td>単価</td><td>500</td><td>円</td><td></td></tr></tbody></table>`+
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>`+
		`<tr><td>ロット</td><td>40</td><td>個</td><td></td></tr><tr><td>単価</td><td>450</td><td>円</td><td></td></tr></tbody></table>`)
	writeBodyFile(t, rfqBoxID, `<h1>`+RFQBoxTitle+`</h1>`+
		`<section data-mirror="再見積依頼"></section>`+
		tableOfLinesHTML(RFQTempPartsType, nil, false)+
		tableOfLinesHTML(RFQNeedsType, nil, true))
	return &auth.User{Username: "root", IsAdmin: true}
}

func postRFQ(t *testing.T, u *auth.User, handler http.HandlerFunc, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/rfq", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	handler(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

// rfqRows は本文の vocabType の n 枚目（1始まり）の表の、空でない行を返します。
func rfqRows(t *testing.T, body, vocabType string, n int) []ourOrderLine {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	tables := tablesOfType(nodes, vocabType)
	if n > len(tables) {
		return nil
	}
	rows := rowsOf(tables[n-1])
	var out []ourOrderLine
	for _, tr := range rows[1:] {
		if ln := lineOfRow(rows[0], tr); !lineIsEmpty(ln) {
			out = append(out, ln)
		}
	}
	return out
}

func boxBody(t *testing.T) string {
	t.Helper()
	body, err := cms.ReadPageBody("000050")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestRFQCollectUsesEstimateLots は、⚠ **ロットが空なら見積計算表のロットごとに行になる**（ロット × 部材の数量）こと、
// **ロットを書けばその数**であることを固定します。
func TestRFQCollectUsesEstimateLots(t *testing.T) {
	u := seedRFQ(t)
	if got := estimateLotsOf(rfqProductID); len(got) != 2 || got[0] != 20 || got[1] != 40 {
		t.Fatalf("見積計算表のロットが %v です（[20 40] のはず）", got)
	}
	code, out := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051"})
	if code != 200 || out["rows"] != float64(4) {
		t.Fatalf("集められません: %d %v", code, out)
	}
	got := rfqRows(t, boxBody(t), RFQNeedsType, 1)
	var qty []string
	for _, ln := range got {
		qty = append(qty, ln.Material+":"+ln.Quantity)
		if ln.ProductID != "000051" || ln.Kind == "" {
			t.Errorf("弊社品番・種類が入っていません: %+v", ln)
		}
	}
	if strings.Join(qty, ",") != "鉄:40,SUS304:20,鉄:80,SUS304:40" {
		t.Errorf("数が %v です（ロット20と40 × 個数2・1）", qty)
	}
	// ロットを書けばその数。
	if code, out := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "/000051", "lot": "5"}); code != 200 || out["rows"] != float64(2) {
		t.Fatalf("ロット5で集められません: %d %v", code, out)
	}
	if got := rfqRows(t, boxBody(t), RFQNeedsType, 1); len(got) != 6 || got[4].Quantity != "10" || got[5].Quantity != "5" {
		t.Errorf("ロット5の行が足されていません: %+v", got)
	}
	// 書いていない・読めない番号は断る。
	if code, _ := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "abc"}); code != 400 {
		t.Errorf("読めない弊社品番で %d", code)
	}
}

// TestRFQNeedsMarksDuplicates は、⚠ **同じもの・同じ数が表の中に2つ以上あれば赤（＋⚠ 重複）**、臨時部材表の行も並び、
// 同じロットでなければ重複でないことを固定します。
func TestRFQNeedsMarksDuplicates(t *testing.T) {
	u := seedRFQ(t)
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"})
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"}) // 二度目——重複
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "6"}) // 別のロット
	body := boxBody(t)
	// 臨時部材表に1行（鉄 板 t3.2*100*200 を10——ロット5の行と同じもの・同じ数）。
	next, _, ok := appendLinesToTable(body, RFQTempPartsType, 1, []ourOrderLine{{Kind: "材料", Material: "鉄", Shape: "板", Size: "t3.2*100*200", Quantity: "10"}})
	if !ok {
		t.Fatal("臨時部材表へ書けません")
	}
	writeBodyFile(t, rfqBoxID, next)
	req := auth.WithUser(httptest.NewRequest("GET", "/000050", nil), u)
	shown := cms.RenderComputedViews(req, rfqBoxID, next)
	// ⚠ 印（rfq-dup-mark）を数える——「⚠ 重複」の文字は操作の欄の説明にも在る（文字で数えると説明の分まで数えた）。
	if n := strings.Count(shown, `class="rfq-dup-mark"`); n != 5 { // ロット5の2行×2回 ＋ 臨時の1行
		t.Errorf("重複の印が %d です（5のはず）:\n%s", n, shown)
	}
	// ⚠ **表の行として**並ぶこと（tbody の中の tr・セルに値）——文字列の有る無しだけを見ると、`<tbody><tr><td>` の札が
	//    落ちてチェックの欄と文字だけが残った形（ブラウザが表の外へ追い出す・2026-10-03 に E2E で踏んだ）でも通ってしまう。
	if !strings.Contains(shown, `<tbody class="vocab-chrome rfq-temp-rows"><tr class="rfq-temp-row rfq-dup"><td>`) ||
		!strings.Contains(shown, `<td>t3.2*100*200</td>`) || !strings.Contains(shown, `data-rfq-temp-row="1"`) {
		t.Errorf("臨時部材表の行が表の行として並んでいません:\n%s", shown)
	}
	if !strings.Contains(shown, "見積依頼部材表へ入れる") || !strings.Contains(shown, "不要") {
		t.Errorf("操作の欄がありません")
	}
}

// TestRFQMoveRemoveAndBack は、⚠ **選んだ行（臨時部材表の行も）を見積依頼部材表へ移し、元から消える**こと・**不要は消すだけ**・
// **↩ 戻すで見積依頼必要部材表へ戻り、最後の1行なら表ごと消える**ことを固定します。
func TestRFQMoveRemoveAndBack(t *testing.T) {
	u := seedRFQ(t)
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"})
	body := boxBody(t)
	next, _, _ := appendLinesToTable(body, RFQTempPartsType, 1, []ourOrderLine{{Material: "真鍮", Shape: "板", Size: "t1", Quantity: "3"}})
	writeBodyFile(t, rfqBoxID, next)

	code, out := postRFQ(t, u, RFQNeedsMoveAPIHandler, map[string]any{"page_id": "000050", "rows": []int{1}, "temp_rows": []int{1}})
	if code != 200 || out["rows"] != float64(2) {
		t.Fatalf("移せません: %d %v", code, out)
	}
	body = boxBody(t)
	if got := rfqRows(t, body, RFQDraftType, 1); len(got) != 2 || got[0].Material != "鉄" || got[1].Material != "真鍮" {
		t.Fatalf("見積依頼部材表に入っていません: %+v", got)
	}
	if got := rfqRows(t, body, RFQNeedsType, 1); len(got) != 1 || got[0].Material != "SUS304" {
		t.Errorf("移した行が見積依頼必要部材表に残っています: %+v", got)
	}
	if got := rfqRows(t, body, RFQTempPartsType, 1); len(got) != 0 {
		t.Errorf("移した臨時部材が臨時部材表に残っています: %+v", got)
	}
	if !strings.Contains(body, "<caption>見積依頼の臨時部材表</caption>") || !strings.Contains(body, "<caption>見積依頼必要部材表</caption>") {
		t.Errorf("空になっても表は残るはず")
	}
	// 2枚目へ足す・1枚目へ足す。
	if code, out := postRFQ(t, u, RFQNeedsMoveAPIHandler, map[string]any{"page_id": "000050", "rows": []int{1}, "into": "1"}); code != 200 || out["rows"] != float64(1) {
		t.Fatalf("1枚目へ足せません: %d %v", code, out)
	}
	if got := rfqRows(t, boxBody(t), RFQDraftType, 1); len(got) != 3 {
		t.Errorf("1枚目に3行のはず: %+v", got)
	}
	if code, _ := postRFQ(t, u, RFQNeedsMoveAPIHandler, map[string]any{"page_id": "000050", "rows": []int{}, "into": "9"}); code != 409 {
		t.Errorf("無い表へ足して %d", code)
	}
	// ↩ 戻す——3行とも戻すと表ごと消える。
	for i := 0; i < 3; i++ {
		if code, out := postRFQ(t, u, RFQDraftBackAPIHandler, map[string]any{"page_id": "000050", "table": 1, "row": 1}); code != 200 {
			t.Fatalf("戻せません: %d %v", code, out)
		}
	}
	body = boxBody(t)
	if strings.Contains(body, "<caption>見積依頼部材表</caption>") {
		t.Errorf("空になった見積依頼部材表が残っています")
	}
	if got := rfqRows(t, body, RFQNeedsType, 1); len(got) != 3 {
		t.Errorf("戻した行が見積依頼必要部材表に無い: %+v", got)
	}
	// 不要——消すだけ。
	if code, out := postRFQ(t, u, RFQNeedsRemoveAPIHandler, map[string]any{"page_id": "000050", "rows": []int{1, 2}}); code != 200 || out["rows"] != float64(2) {
		t.Fatalf("消せません: %d %v", code, out)
	}
	if got := rfqRows(t, boxBody(t), RFQNeedsType, 1); len(got) != 1 {
		t.Errorf("不要で消えていません: %+v", got)
	}
	if code, _ := postRFQ(t, u, RFQNeedsRemoveAPIHandler, map[string]any{"page_id": "000050"}); code != 400 {
		t.Errorf("何も選ばずに消して %d", code)
	}
}

// TestRFQFolderProductsAndCollectMany は装置フォルダから選ぶ口（2026-10-03）を固定します——
// ① フォルダの下の加工製品が並ぶ（⚠ 改定で子ページへ移った旧版は出さない——同じ品物が版の数だけ並ぶ）
// ② 何枚か選んで送ると、集められるものだけ入れ、集められないもの（部材の表が無い）は理由を返す
// ③ 1枚も集められなければ断り、本文は変わらない
func TestRFQFolderProductsAndCollectMany(t *testing.T) {
	u := seedRFQ(t)
	addPage(t, 52, 0, "標準2輪", "root", "302", true)
	addPage(t, 53, 52, "取付ベース", "root", "302", true)
	addPage(t, 54, 52, "ステー", "root", "302", true)
	addPage(t, 55, 53, "取付ベース", "root", "302", true) // 改定で子へ移った旧版
	tag := func(no string) string { return `<dl data-type="tags"><dt>品番</dt><dd>` + no + `</dd></dl>` }
	writeBodyFile(t, 53, `<h1>取付ベース</h1>`+tag("A100-B01-1")+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>SS400</td><td>板</td><td>t6*80*120</td><td>3</td></tr>`+
		`</tbody></table>`)
	writeBodyFile(t, 54, `<h1>ステー</h1>`+tag("A100-B01-2"))
	writeBodyFile(t, 55, `<h1>取付ベース</h1>`+tag("A100-B01-1"))

	list := func(folder string) (int, map[string]any) {
		t.Helper()
		req := auth.WithUser(httptest.NewRequest("GET", "/api/rfq/folder-products?folder="+folder, nil), u)
		rr := httptest.NewRecorder()
		RFQFolderProductsAPIHandler(rr, req)
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	// ①
	code, out := list("000052")
	if code != 200 || out["title"] != "標準2輪" {
		t.Fatalf("一覧が出ません: %d %v", code, out)
	}
	var ids []string
	for _, p := range out["products"].([]any) {
		m := p.(map[string]any)
		ids = append(ids, m["id"].(string)+":"+m["part_no"].(string))
	}
	if strings.Join(ids, ",") != "000054:A100-B01-2,000053:A100-B01-1" {
		t.Errorf("並んだ加工製品が %v です（旧版 000055 は出ない・題の順）", ids)
	}
	if code, _ := list("999999"); code != 404 {
		t.Errorf("無いページの番号で %d", code)
	}

	// ②
	code, res := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "items": []map[string]string{
		{"product": "000053", "lot": "2"}, {"product": "000054"}, {"product": "000051"}}})
	if code != 200 || res["rows"] != float64(5) {
		t.Fatalf("何枚かを集められません: %d %v", code, res)
	}
	if sk, _ := res["skipped"].([]any); len(sk) != 1 || !strings.Contains(sk[0].(string), "000054") {
		t.Errorf("部材の無い 000054 の理由が返りません: %v", res["skipped"])
	}
	got := rfqRows(t, boxBody(t), RFQNeedsType, 1)
	if len(got) != 5 || got[0].ProductID != "000053" || got[0].Quantity != "6" || got[1].ProductID != "000051" {
		t.Errorf("入った行が違います（000053 をロット2で6・続いて 000051 の4行）: %+v", got)
	}

	// ③
	before := boxBody(t)
	if code, res := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "items": []map[string]string{
		{"product": "000054"}, {"product": "000053", "lot": "0"}}}); code != 409 {
		t.Errorf("1枚も集められないのに %d %v", code, res)
	}
	if boxBody(t) != before {
		t.Error("断ったのに本文が変わりました")
	}
}

// TestRFQNewDoc は見積依頼書ページを作る（2026-10-03・段2・rfq_doc.go）を固定します——見積依頼部材表の行が見積依頼明細へ
// （状態は未回答・⚠ 単価は空——業者に聞く前の値を紙に持ち込まない）、タグ（見積依頼番号＝ページ番号・仕入先・見積依頼日）と
// 備考の節が入り、置き場は 見積依頼／年／月、元の見積依頼部材表は消える。仕入先が無ければ作らない。
func TestRFQNewDoc(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, box, `<h1>`+RFQBoxTitle+`</h1>`+tableOfLinesHTML(RFQDraftType, []ourOrderLine{
		{ProductID: "000041", Kind: "材料", Material: "SS400", Shape: "板", Size: "t6*80*120", Quantity: "6", Unit: "枚", Cost: "1200"},
		{Kind: "購入部品", ItemID: "M8-20", ItemName: "六角ボルト", Quantity: "10", Unit: "本"},
	}, true))
	root := &auth.User{Username: "root", IsAdmin: true}

	if code, _ := postRFQ(t, root, RFQNewDocAPIHandler, map[string]any{"page_id": box, "table": 1}); code != 400 {
		t.Errorf("仕入先が無いのに %d", code)
	}
	if code, _ := postRFQ(t, root, RFQNewDocAPIHandler, map[string]any{"page_id": box, "table": 2, "supplier": "わかば鋼業"}); code != 409 {
		t.Errorf("無い見積依頼部材表で %d", code)
	}
	code, out := postRFQ(t, root, RFQNewDocAPIHandler, map[string]any{"page_id": box, "table": 1,
		"supplier": "わかば鋼業", "date": "2026-10-05", "note": "標準2輪用"})
	id, _ := out["page_id"].(string)
	if code != 200 || id == "" || out["rows"] != float64(2) {
		t.Fatalf("見積依頼書ページを作れません: %d %v", code, out)
	}
	body := readPageBody(t, id)
	for _, want := range []string{
		"<h1>見積依頼　わかば鋼業</h1>",
		"<dt>" + RFQNoTag + "</dt><dd>" + id + "</dd>",
		"<dt>" + SupplierTag + "</dt><dd>わかば鋼業</dd>",
		"<dt>" + RFQDateTag + "</dt><dd>2026-10-05</dd>",
		"<p>標準2輪用</p>",
		">SS400<", ">六角ボルト<", ">" + rfqLineUnanswered + "<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("見積依頼書ページに %q がありません:\n%s", want, body)
		}
	}
	if strings.Contains(body, "1200") {
		t.Errorf("単価が写っています（業者に聞く前の値）:\n%s", body)
	}
	if got := rfqRows(t, body, RFQItemsType, 1); len(got) != 2 || strings.Count(body, ">"+rfqLineUnanswered+"<") != 2 {
		t.Errorf("見積依頼明細が2行・未回答ではありません: %+v", got)
	}
	// 置き場は 見積依頼／年／月。
	meta, _ := page.ReadSidecar(id)
	month, _ := page.ReadSidecar(meta.ParentID)
	if cms.PageTitleByID(pageNum(meta.ParentID)) != "10月" || cms.PageTitleByID(pageNum(month.ParentID)) != "2026年" {
		t.Errorf("置き場が 見積依頼／2026年／10月 ではありません: %s ← %s", cms.PageTitleByID(pageNum(meta.ParentID)), cms.PageTitleByID(pageNum(month.ParentID)))
	}
	if after := readPageBody(t, box); strings.Contains(after, "<caption>見積依頼部材表</caption>") {
		t.Errorf("元の見積依頼部材表が残っています:\n%s", after)
	}
}

// TestRFQPDFRoundTrips は見積依頼書の紙（2026-10-03・rfq_pdf.go）を読み返して確かめます——題・日付・№・仕入先 御中・明細・
// 「よろしくお願いします。」・備考が刷られ、⚠ **単価は空欄**（本文に値があっても刷らない——業者が書き込む欄）、辞退の行は刷らない。
func TestRFQPDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	body := `<h1>見積依頼　わかば鋼業</h1><dl data-type="tags"><dt>` + RFQNoTag + `</dt><dd>000310</dd><dt>` + SupplierTag +
		`</dt><dd>わかば鋼業</dd><dt>` + RFQDateTag + `</dt><dd>2026-10-05</dd></dl>` +
		`<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000080</td><td>材料</td><td>SS400</td><td>板</td><td>t6*80*120</td><td>6</td><td>枚</td><td>999</td><td>未回答</td></tr>` +
		`<tr><td>000081</td><td>材料</td><td>A5052</td><td>丸棒</td><td>φ30*50</td><td>2</td><td>本</td><td></td><td>辞退</td></tr>` +
		`</tbody></table><section><h2>備考</h2><p>標準2輪用</p></section>`
	pdf, err := buildRFQPDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"2026年10月5日", "000310", "わかば鋼業", "御中", "御見積をお願いいたします", "SS400", "t6*80*120", "単価",
		"よろしくお願いします。", "標準2輪用"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ PDFに %q が入っていません。読み返した中身:\n%s", want, got)
		}
	}
	for _, not := range []string{"999", "A5052", "金額"} {
		if strings.Contains(got, not) {
			t.Errorf("⚠ PDFに %q が刷られています（単価は空欄・辞退の行と金額は刷らない）:\n%s", not, got)
		}
	}
}

// TestRFQDocFooterAndSend は見積依頼書ページの足元（備考の欄・PDF を作る）と末尾の「見積依頼を送る」、送る用件の初期値
// （送るときに作る PDF・件名）、備考の保存、「送った（FAX・手渡し）」の送付日を固定します（2026-10-03）。
func TestRFQDocFooterAndSend(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, box, `<h1>`+RFQBoxTitle+`</h1>`+tableOfLinesHTML(RFQDraftType, []ourOrderLine{
		{ProductID: "000041", Kind: "材料", Material: "SS400", Shape: "板", Size: "t6*80*120", Quantity: "6", Unit: "枚"},
	}, true))
	root := &auth.User{Username: "root", IsAdmin: true}
	_, out := postRFQ(t, root, RFQNewDocAPIHandler, map[string]any{"page_id": box, "table": 1, "supplier": "わかば鋼業"})
	id, _ := out["page_id"].(string)
	if id == "" {
		t.Fatalf("見積依頼書ページを作れません: %v", out)
	}
	body := readPageBody(t, id)
	req := auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), root)
	shown := cms.RenderComputedViews(req, pageNum(id), body)
	for _, want := range []string{`data-note-url="/api/rfq/note"`, `class="chip-btn rfq-pdf-go"`, `data-mail-compose="` + RFQMailPurpose + `"`, `rfq-sent-go`} {
		if !strings.Contains(shown, want) {
			t.Errorf("見積依頼書ページに %q がありません:\n%s", want, shown)
		}
	}
	// 送る欄は PDF の下——ファイル表示は明細の直後、「見積依頼を送る」は末尾（テンプレートの最後）。
	if strings.Index(shown, "見積依頼を送る") < strings.Index(shown, "<caption>見積依頼明細</caption>") {
		t.Errorf("「見積依頼を送る」が明細より上にあります")
	}
	d, err := rfqMailDefaults(root, id)
	if err != nil || d.Generated != "見積依頼書 "+id+".pdf" || d.Subject != "見積依頼（№ "+id+"）" {
		t.Errorf("送る欄の初期値が違います: %+v %v", d, err)
	}
	if code, _ := postRFQ(t, root, RFQNoteAPIHandler, map[string]any{"page_id": id, "note": "標準2輪用"}); code != 200 {
		t.Fatalf("備考を保存できません: %d", code)
	}
	if _, _, note, _ := readRFQDoc(readPageBody(t, id)); strings.Join(note, "／") != "標準2輪用" {
		t.Errorf("紙が読む備考が %v", note)
	}
	if code, _ := postRFQ(t, root, RFQSentAPIHandler, map[string]any{"page_id": id}); code != 200 {
		t.Fatalf("送ったにできません: %d", code)
	}
	if !strings.Contains(readPageBody(t, id), "<dt>"+EstimateSentTag+"</dt><dd>"+time.Now().Format("2006-01-02")+"</dd>") {
		t.Errorf("送付日が入っていません:\n%s", readPageBody(t, id))
	}
	if code, _ := postRFQ(t, root, RFQSentAPIHandler, map[string]any{"page_id": box}); code != 400 {
		t.Errorf("見積依頼書でないページに送付日を書こうとして %d", code)
	}
}

// TestRFQImportAnsweredQuote は過去の見積もりを移す口（2026-10-04・RFQImportAPIHandler）を固定します——管理者だけ・
// 見積依頼明細に単価と「回答あり」・回答日と見積依頼日はその日・置き場は 見積依頼／その年／その月・備考の節。
func TestRFQImportAnsweredQuote(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	in := map[string]any{"supplier": "ふじ鍍金", "date": "2025-05-14", "note": "ワンノートから移した過去の見積",
		"lines": []map[string]string{
			{"product_id": "000041", "kind": "外注加工", "work": "塗装", "color": "緑", "quantity": "20", "cost": "298", "note": "元: 2025-05-14 ふじ鍍金 ロット20 298"},
		}}
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, RFQImportAPIHandler, in); code != 403 {
		t.Errorf("管理者でない人が移せてしまう: %d", code)
	}
	root := &auth.User{Username: "root", IsAdmin: true}
	code, out := postRFQ(t, root, RFQImportAPIHandler, in)
	id, _ := out["page_id"].(string)
	if code != 200 || id == "" {
		t.Fatalf("移せません: %d %v", code, out)
	}
	body := readPageBody(t, id)
	for _, want := range []string{"<h1>見積依頼　ふじ鍍金</h1>", "<dt>" + RFQAnsweredTag + "</dt><dd>2025-05-14</dd>",
		"<dt>" + RFQDateTag + "</dt><dd>2025-05-14</dd>", ">298<", ">" + rfqLineAnswered + "<", ">塗装<", ">緑<",
		"<p>ワンノートから移した過去の見積</p>"} {
		if !strings.Contains(body, want) {
			t.Errorf("移した見積依頼書ページに %q がありません:\n%s", want, body)
		}
	}
	meta, _ := page.ReadSidecar(id)
	month, _ := page.ReadSidecar(meta.ParentID)
	if cms.PageTitleByID(pageNum(meta.ParentID)) != "05月" || cms.PageTitleByID(pageNum(month.ParentID)) != "2025年" {
		t.Errorf("置き場がその日の年月ではありません: %s ← %s", cms.PageTitleByID(pageNum(meta.ParentID)), cms.PageTitleByID(pageNum(month.ParentID)))
	}
	// 返事の来た見積なので、送る欄は閉じておき回答日を出す（まだのものだけ開く）。
	shown := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), root), pageNum(id), body)
	if !strings.Contains(shown, `class="estimate-mail rfq-mail"><summary>`) || !strings.Contains(shown, "回答日: 2025-05-14") {
		t.Errorf("返事の来た見積の送る欄が閉じていません:\n%s", shown)
	}
	if code, _ := postRFQ(t, root, RFQImportAPIHandler, map[string]any{"supplier": "ふじ鍍金", "date": "2025/05/14", "lines": in["lines"]}); code != 400 {
		t.Errorf("日付の形が違うのに %d", code)
	}
}

// TestRFQFillItemNames は見積依頼明細の空の品番・品名を加工製品ページから埋めること（2026-10-04・fillRFQItemNames）を固定します——
// 移す口（import）は自動で埋め、既に書いてある品名は残す。後始末の口（fill-names）は管理者だけ。
// 利用者:「品番と品名の欄が埋まらない事には、人間には見てわかりません」。
func TestRFQFillItemNames(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, "000041", `<h1>取付ベース</h1><dl data-type="tags"><dt>品番</dt><dd>A100-B01-1</dd><dt>品名</dt><dd>取付ベース</dd></dl>`)
	root := &auth.User{Username: "root", IsAdmin: true}
	_, out := postRFQ(t, root, RFQImportAPIHandler, map[string]any{"supplier": "ふじ鍍金", "date": "2025-05-14",
		"lines": []map[string]string{
			{"product_id": "000041", "kind": "外注加工", "work": "塗装", "quantity": "20", "cost": "298"},
			{"product_id": "000041", "kind": "外注加工", "work": "塗装", "item_name": "カバー（上）", "quantity": "20", "cost": "300"},
		}})
	id, _ := out["page_id"].(string)
	body := readPageBody(t, id)
	if !strings.Contains(body, "<td>外注加工</td><td></td><td>A100-B01-1</td><td>取付ベース</td>") {
		t.Errorf("空の品番・品名が加工製品ページから埋まっていません:\n%s", body)
	}
	if !strings.Contains(body, "<td>A100-B01-1</td><td>カバー（上）</td>") {
		t.Errorf("書いてあった品名が残っていません（品番だけ埋めるはず）:\n%s", body)
	}
	// 後始末の口: 品番・品名を消した本文を書き戻してから、管理者だけが埋められる。
	emptied := strings.ReplaceAll(strings.ReplaceAll(body, "<td>A100-B01-1</td>", "<td></td>"), "<td>取付ベース</td>", "<td></td>")
	seedBody(t, id, emptied)
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, RFQFillNamesAPIHandler, map[string]any{"page_id": id}); code != 403 {
		t.Errorf("管理者でない人が埋められてしまう: %d", code)
	}
	code, res := postRFQ(t, root, RFQFillNamesAPIHandler, map[string]any{"page_id": id})
	if code != 200 || res["filled"] != float64(3) {
		t.Errorf("後始末で埋めたセルが %v（3のはず——1行目の品番・品名と2行目の品番）: %d", res["filled"], code)
	}
	if b := readPageBody(t, id); !strings.Contains(b, "<td>A100-B01-1</td><td>取付ベース</td>") || !strings.Contains(b, "<td>A100-B01-1</td><td>カバー（上）</td>") {
		t.Errorf("後始末で埋まっていません:\n%s", b)
	}
}

// TestRFQSetCellsChecksFirst は見積依頼明細のセルを確かめてから書き換える口（2026-10-04・RFQSetCellsAPIHandler）を固定します——
// いまの値が合えば書き、1つでも合わなければ何も書かない（409）・管理者だけ。移した行の「数の無い行に入れた 1」を空欄に戻すのに使う。
func TestRFQSetCellsChecksFirst(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	root := &auth.User{Username: "root", IsAdmin: true}
	_, out := postRFQ(t, root, RFQImportAPIHandler, map[string]any{"supplier": "ふじ鍍金", "date": "2025-05-14",
		"lines": []map[string]string{
			{"product_id": "000041", "kind": "外注加工", "work": "塗装", "quantity": "1", "cost": "298"},
			{"product_id": "000041", "kind": "外注加工", "work": "塗装", "quantity": "20", "cost": "250"},
		}})
	id, _ := out["page_id"].(string)
	edit := func(u *auth.User, expect map[string]string) (int, map[string]any) {
		return postRFQ(t, u, RFQSetCellsAPIHandler, map[string]any{"page_id": id, "edits": []map[string]any{
			{"row": 2, "expect": map[string]string{"数量": "20", "単価": "250"}, "set": map[string]string{}},
			{"row": 1, "expect": expect, "set": map[string]string{"数量": ""}},
		}})
	}
	if code, _ := edit(&auth.User{Username: "bob"}, map[string]string{"数量": "1"}); code != 403 {
		t.Errorf("管理者でない人が書き換えられてしまう: %d", code)
	}
	before := readPageBody(t, id)
	if code, res := edit(root, map[string]string{"数量": "1", "単価": "999"}); code != 409 || !strings.Contains(res["message"].(string), "単価") {
		t.Errorf("いまの値が合わないのに %d %v", code, res)
	}
	if readPageBody(t, id) != before {
		t.Error("合わなかったのに本文が変わりました")
	}
	if code, res := edit(root, map[string]string{"数量": "1", "単価": "298"}); code != 200 || res["changed"] != float64(1) {
		t.Fatalf("書き換えられません: %d %v", code, res)
	}
	got := rfqRows(t, readPageBody(t, id), RFQItemsType, 1)
	if len(got) != 2 || got[0].Quantity != "" || got[1].Quantity != "20" {
		t.Errorf("1行目の数量だけ空欄のはず: %+v", got)
	}
}
