package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 製造ページの「この製造の書類」（2026-10-21・mfg_docs.go）——記録に置いた書類を明細の「製造」の列で引き、
// それより前に製造ページの子に作った書類も並べる。ほかの製造の書類は並べない。
func TestMfgDocsListsDocsByColumnAndOldChildren(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 991, -1, "ブラケット", "root", "302", true)
	syncBody(t, 991, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)
	addPage(t, 992, -1, "カバー", "root", "302", true)
	syncBody(t, 992, `<h1>カバー</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)

	// それまでの形——製造ページの子の納品書（明細に「製造」の列が無い）。
	addPage(t, 993, 991, "【弊社】納品書　あけぼの精工", "root", "302", true)
	syncBody(t, 993, `<h1>【弊社】納品書　あけぼの精工</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurDelivery+
		`</dd><dt>`+DeliveryClientTag+`</dt><dd>あけぼの精工</dd></dl>`)

	order := func(draft string) string {
		code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
			"supplier": "みなと商店", "draft_page": draft, "draft_index": "1",
			"lines": []map[string]any{{"product_id": "000031", "for_order": "000030", "material": "鉄", "shape": "FB", "size": "t4.5*75*1090", "quantity": "2"}},
		})
		if code != 200 {
			t.Fatalf("部材発注書を作れません: %d %v", code, out)
		}
		return out["page_id"].(string)
	}
	mine := order("000991")
	other := order("000992")

	docs := mfgDocs(adminUser(), 991)
	var got []string
	for _, d := range docs {
		got = append(got, page.FormatID(d.PageID)+" "+d.Kind+" "+d.Party+" "+d.State)
	}
	want := []string{mine + " " + PageKindOurPurchase + " みなと商店 未送付", "000993 " + PageKindOurDelivery + " あけぼの精工 未発行"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("⚠ この製造の書類が違います:\n%s\n（のはず:\n%s）", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	html := mfgDocsHTML(docs)
	for _, w := range []string{`<a href="/` + mine + `">`, `<a href="/000993">`, "この製造の書類"} {
		if !strings.Contains(html, w) {
			t.Errorf("⚠ 欄に %q がありません:\n%s", w, html)
		}
	}
	if strings.Contains(html, `/`+other+`"`) {
		t.Errorf("⚠ ほかの製造の部材発注書 %s が並んでいます:\n%s", other, html)
	}
	if got := mfgDocsHTML(mfgDocs(adminUser(), 990)); !strings.Contains(got, "まだありません") {
		t.Errorf("⚠ 書類の無い製造で「まだありません」と言いません:\n%s", got)
	}
}

// TestMfgDocsUnderTheirHeadings は、製造ページの書類が、それを作る作業の見出しの節に出ることを固定します（2026-10-21 夜）。
//
// > 2026-10-21 利用者:「部材発注書は納品作業ではなく、発注作業です」
//
// 部材発注書は「発注作業」の節（次の「見積依頼作業」より前）、納品書は「納品作業」の節に出る。
func TestMfgDocsUnderTheirHeadings(t *testing.T) {
	setupExtTest(t, "000980", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl><h2>作業開始</h2>` +
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>製造完了</th></tr>` +
		`<tr><td>000031</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td><td></td></tr></tbody></table>` +
		`<h2>部材作業</h2><p>x</p><h2>発注作業</h2><p>x</p><h2>見積依頼作業</h2><p>x</p><h2>納品作業</h2><p>x</p>`
	addPage(t, 981, -1, "ブラケット", "root", "302", true)
	writeBodyFile(t, 981, body)
	syncBody(t, 981, body)
	addPage(t, 982, 981, "【弊社】納品書　あけぼの精工", "root", "302", true)
	syncBody(t, 982, `<h1>【弊社】納品書　あけぼの精工</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurDelivery+`</dd></dl>`)
	addPage(t, 983, 981, "【弊社】部材発注　みなと商店", "root", "302", true)
	syncBody(t, 983, `<h1>【弊社】部材発注　みなと商店</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurPurchase+`</dd></dl>`)

	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000981", nil), adminUser()), 981, body)
	order, quote, dlv := strings.Index(view, "<h2>発注作業</h2>"), strings.Index(view, "<h2>見積依頼作業</h2>"), strings.Index(view, "<h2>納品作業</h2>")
	po, dn := strings.Index(view, `href="/000983"`), strings.Index(view, `href="/000982"`)
	if !(order < po && po < quote) {
		t.Errorf("⚠ 部材発注書が「発注作業」の節にありません（発注作業 %d・部材発注書 %d・見積依頼作業 %d）:\n%s", order, po, quote, view)
	}
	if !(dlv < dn) {
		t.Errorf("⚠ 納品書が「納品作業」の節にありません:\n%s", view)
	}
}
