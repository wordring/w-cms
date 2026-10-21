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

// 製造ページを記録へ移す（2026-10-19・mfg_close.go・作る順の5番）。

func mfgBody(rows string) string {
	return `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindMfg + `</dd></dl>` +
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>製造完了</th></tr>` +
		rows + `</tbody></table>`
}

func invoiceBodyOf(deliveries ...[2]string) string {
	b := `<h1>【弊社】請求書　あけぼの精工</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindOurInvoice + `</dd>` +
		`<dt>` + InvoiceDateTag + `</dt><dd>2026-10-31</dd></dl>` +
		`<table><caption>請求明細表</caption><tbody><tr><th>納品日</th><th>納品書</th><th>案件</th><th>発注書番号</th><th>弊社品番</th>` +
		`<th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>`
	for _, d := range deliveries {
		b += `<tr><td>2026-10-05</td><td>` + d[0] + `</td><td>000900</td><td>PO-1</td><td></td><td>A-1</td><td>ブラケット</td><td>` + d[1] + `</td><td>個</td><td>100</td><td></td></tr>`
	}
	return b + `</tbody></table>`
}

// TestCloseFinishedMfgPagesOnDelivery は、納品書の PDF を作ったとき（2026-10-21——納品までは製造の管理下）、
//   - 納め終えていない製造ページは動かさない
//   - 納め終えても、発行していない（`発行日` の空い）納品書があれば動かさない
//   - 全部納めて全部発行したら `記録／年／月／製造記録`（年月はその日）へ移す・2回目は動かさない
//   - 請求書の足元には、記録へ移したことが出る（請求書とは切り離したが、見せるのは続ける）
//
// ことを固定します。
func TestCloseFinishedMfgPagesOnDelivery(t *testing.T) {
	setupExtTest(t, "000940", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	box, err := cms.EnsureTopLevelBox(MfgBoxTitle, "alice")
	if err != nil {
		t.Fatal(err)
	}
	addPage(t, 951, pageNum(box), "ブラケット", "alice", "302", true)
	syncBody(t, 951, mfgBody(`<tr><td></td><td>000900</td><td>A-1</td><td>ブラケット</td><td>10</td><td></td></tr>`))
	seedIssuedDelivery(t, 952, 951, "あけぼの精工", "2026-10-05", "2026-10-05", [5]string{"000900", "A-1", "ブラケット", "6", "100"})
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)

	if moved := closeFinishedMfgPages(u, 952, now); len(moved) != 0 {
		t.Errorf("⚠ 納め終えていない製造ページを移しました: %v", moved)
	}
	// 残りの4を納めた——でも、その納品書はまだ発行していない。
	seedIssuedDelivery(t, 954, 951, "あけぼの精工", "2026-10-20", "", [5]string{"000900", "A-1", "ブラケット", "4", "100"})
	if moved := closeFinishedMfgPages(u, 954, now); len(moved) != 0 {
		t.Errorf("⚠ 発行していない納品書があるのに移しました: %v", moved)
	}
	syncBody(t, 954, issuedDeliveryBody("あけぼの精工", "2026-10-20", "2026-11-02", [5]string{"000900", "A-1", "ブラケット", "4", "100"}))
	moved := closeFinishedMfgPages(u, 954, now)
	if len(moved) != 1 || moved[0] != 951 {
		t.Fatalf("⚠ 済んだ製造ページを移しません: %v", moved)
	}
	if got := placeOf("000951"); !strings.HasSuffix(got, "2026年／11月／"+RecordWorkFolder) {
		t.Errorf("⚠ 移した先が %s です（記録／2026年／11月／製造記録——年月はその日のはず）", got)
	}
	if mfgIsOpen(951) {
		t.Errorf("⚠ 移したのに、まだ「製造」の下と言います")
	}
	if again := closeFinishedMfgPages(u, 954, now); len(again) != 0 {
		t.Errorf("⚠ 閉じた製造ページをもう一度動かしました: %v", again)
	}
	// 請求明細表の足元に「記録へ移しました」。
	addPage(t, 953, -1, "請求書", "alice", "302", true)
	inv := invoiceBodyOf([2]string{"000952", "6"}, [2]string{"000954", "4"})
	syncBody(t, 953, inv)
	got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000953", nil), u), 953, inv)
	if !strings.Contains(got, "は記録へ移しました") {
		t.Errorf("⚠ 請求明細表の足元に、製造ページを移したことが出ていません:\n%s", got)
	}
}

// TestMfgCloseByHand は「この製造を閉じる」（/api/mfg/close）を固定します——済んでいなくても閉じられ、2回目は断り、製造ページでなければ断る。
func TestMfgCloseByHand(t *testing.T) {
	setupExtTest(t, "000940", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	box, err := cms.EnsureTopLevelBox(MfgBoxTitle, "alice")
	if err != nil {
		t.Fatal(err)
	}
	addPage(t, 955, pageNum(box), "ブラケット", "alice", "302", true)
	body := mfgBody(`<tr><td></td><td>000900</td><td>A-1</td><td>ブラケット</td><td>10</td><td></td></tr>`)
	writeBodyFile(t, 955, body)
	syncBody(t, 955, body)
	// 先に置く（閉じると記録の年月のフォルダが番号を取る）。
	addPage(t, 956, -1, "製造でないページ", "alice", "302", true)
	syncBody(t, 956, `<h1>製造でないページ</h1>`)
	req := auth.WithUser(httptest.NewRequest("GET", "/000955", nil), u)
	if got := cms.RenderComputedViews(req, 955, body); !strings.Contains(got, "mfg-close-go") {
		t.Errorf("⚠ 製造ページに「この製造を閉じる」がありません:\n%s", got)
	}
	if code, out := postRFQ(t, u, MfgCloseAPIHandler, map[string]any{"page_id": "000955"}); code != 200 {
		t.Fatalf("閉じられません: %d %v", code, out)
	}
	if !strings.HasSuffix(placeOf("000955"), RecordWorkFolder) {
		t.Errorf("⚠ 閉じた製造ページの置き場が %s です（記録／年／月／製造記録のはず）", placeOf("000955"))
	}
	if got := cms.RenderComputedViews(req, 955, body); !strings.Contains(got, "この製造は閉じています") || strings.Contains(got, "mfg-close-go") {
		t.Errorf("⚠ 閉じた製造ページの足元が違います:\n%s", got)
	}
	if code, _ := postRFQ(t, u, MfgCloseAPIHandler, map[string]any{"page_id": "000955"}); code != 409 {
		t.Errorf("⚠ 閉じた製造をもう一度閉じられます: %d", code)
	}
	if code, _ := postRFQ(t, u, MfgCloseAPIHandler, map[string]any{"page_id": "000956"}); code != 400 {
		t.Errorf("⚠ 製造ページでないページを閉じられます: %d", code)
	}
}
