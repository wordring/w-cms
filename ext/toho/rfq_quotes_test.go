package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 加工製品ページの「貰った見積」（2026-10-04・rfq_quotes.go）。

// productWithSupplied は支給部品の表を持つ加工製品ページの本文です（テンプレートと同じ「見出しの節＋キャプション」の形）。
const productWithSupplied = `<h1>カバー</h1>` +
	`<section><h2>支給部品</h2><table><caption>支給部品</caption><tbody>` +
	`<tr><th>品名</th><th>仕様</th><th>個数</th><th>備考</th></tr>` +
	`<tr><td></td><td></td><td></td><td></td></tr></tbody></table></section>`

// TestProductPageListsReceivedQuotes は、⚠ **引けたときに業者・日付・ロット・単価・見積依頼書が出る**こと、新しい順に並ぶこと、
// 別の加工製品の行は出ないこと、出すのは節の中・表の後（表の中へ div を入れない）であることを固定します。
func TestProductPageListsReceivedQuotes(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	root := &auth.User{Username: "root", IsAdmin: true}
	older := map[string]any{"supplier": "ふじ鍍金", "date": "2025-05-14", "lines": []map[string]string{
		{"product_id": "000041", "kind": "外注加工", "work": "塗装", "color": "緑", "quantity": "20", "cost": "298"},
		{"product_id": "000042", "kind": "外注加工", "work": "塗装", "color": "黒", "quantity": "10", "cost": "777"},
	}}
	newer := map[string]any{"supplier": "みなと商店", "date": "2026-01-20", "lines": []map[string]string{
		{"product_id": "000041", "kind": "材料", "material": "SS400", "shape": "板", "size": "t3.2", "quantity": "", "cost": "1500"},
	}}
	var ids []string
	for _, in := range []map[string]any{older, newer} {
		code, out := postRFQ(t, root, RFQImportAPIHandler, in)
		id, _ := out["page_id"].(string)
		if code != 200 || id == "" {
			t.Fatalf("見積依頼書を移せません: %d %v", code, out)
		}
		ids = append(ids, id)
	}

	show := func(productID int) string {
		req := auth.WithUser(httptest.NewRequest("GET", "/"+page.FormatID(productID), nil), root)
		return cms.RenderComputedViews(req, productID, productWithSupplied)
	}
	got := show(41)
	for _, want := range []string{"💴 貰った見積", "ふじ鍍金", "298円", ">20<", ">緑<", "塗装", `<a href="/` + ids[0] + `">2025-05-14</a>`,
		"みなと商店", "1500円", "SS400 板 t3.2", `<a href="/` + ids[1] + `">2026-01-20</a>`, `class="vocab-chrome rfq-quotes"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("⚠ 鏡が走っていないか、%q が出ていません:\n%s", want, got)
		}
	}
	if strings.Contains(got, "777円") {
		t.Errorf("⚠ 別の加工製品（000042）の見積が出ています:\n%s", got)
	}
	if strings.Index(got, "みなと商店") > strings.Index(got, "ふじ鍍金") {
		t.Errorf("⚠ 新しい見積（2026-01-20）が先に来ていません:\n%s", got)
	}
	if i, j, k := strings.Index(got, "</table>"), strings.Index(got, "rfq-quotes"), strings.Index(got, "</section>"); !(i < j && j < k) {
		t.Errorf("⚠ 貰った見積が支給部品の表の後・節の中にありません（表 %d・一覧 %d・節の終わり %d）:\n%s", i, j, k, got)
	}
	if n := strings.Count(got, "💴 貰った見積"); n != 1 {
		t.Errorf("貰った見積が %d 回出ています（1ページに1回）", n)
	}

	// 見積の無い加工製品は短い断りだけ（表は出さない）。
	empty := show(43)
	if !strings.Contains(empty, "💴 貰った見積") || !strings.Contains(empty, "まだありません") || strings.Contains(empty, "rfq-quotes-table") {
		t.Errorf("見積の無い加工製品の出方が違います:\n%s", empty)
	}
}

// TestReceivedQuotesHideUnreadableRFQ は、⚠ **読めない見積依頼書の業者と単価を出さない**ことを固定します（見せ分け）。
func TestReceivedQuotesHideUnreadableRFQ(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 41, 0, "カバー", "root", "302", true)
	addPage(t, 60, 0, "見積依頼　ふじ鍍金", "alice", "300", false)
	syncBody(t, 60, `<h1>見積依頼　ふじ鍍金</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>ふじ鍍金</dd><dt>`+RFQAnsweredTag+`</dt><dd>2025-05-14</dd></dl>`+
		`<table><caption>見積依頼明細</caption><tbody>`+
		`<tr><th>弊社品番</th><th>種類</th><th>加工内容</th><th>数量</th><th>単価</th><th>状態</th></tr>`+
		`<tr><td>000041</td><td>外注加工</td><td>塗装</td><td>20</td><td>298</td><td>`+rfqLineAnswered+`</td></tr>`+
		`</tbody></table>`)
	render := func(u *auth.User) string {
		return cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000041", nil), u), 41, productWithSupplied)
	}
	if got := render(&auth.User{Username: "alice"}); !strings.Contains(got, "298円") || !strings.Contains(got, "ふじ鍍金") {
		t.Fatalf("前提が崩れています: 持ち主には見えるはず（鏡が走っていない）:\n%s", got)
	}
	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(60).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は見積依頼書を読めてはいけません")
	}
	if got := render(mallory); strings.Contains(got, "298円") || strings.Contains(got, "ふじ鍍金") {
		t.Errorf("⚠ 読めない見積依頼書の業者と単価が出ています:\n%s", got)
	}
}
