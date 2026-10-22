package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 赤字の警告（2026-10-17・order_loss.go）——利用者:「弊社利益を足した単価が発注書の単価を超えた場合赤字です。赤字は警告して
// 欲しいです」「注文の数量以下でいちばん大きいロットで比べたら良いと思います。」「2か所で良いと思います。」。

// seedLossProduct は見積表（ロット1: 1000円・ロット20: 600円）を持つ加工製品ページを作ります。
func seedLossProduct(t *testing.T, id, parent int) {
	t.Helper()
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		`<table><caption>見積表</caption><tbody><tr><th>個数</th><th>表面処理</th><th>原価</th><th>利益率</th><th>単価</th><th>備考</th></tr>` +
		`<tr><td>1</td><td></td><td>900</td><td>10</td><td>1000</td><td></td></tr>` +
		`<tr><td>20</td><td></td><td>540</td><td>10</td><td>600</td><td></td></tr></tbody></table>`
	addPage(t, id, parent, "ブラケット", "alice", "302", true)
	writeBodyFile(t, id, body)
	if err := cms.SyncIndex(page.FormatID(id), body); err != nil {
		t.Fatal(err)
	}
}

// TestLossOfUsesLargestLotNotOverQuantity は比べるロットと、赤字かどうかを固定します。
func TestLossOfUsesLargestLotNotOverQuantity(t *testing.T) {
	setupExtTest(t, "000880", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 881, -1, CustomerBoxTitle, "alice", "302", true)
	seedLossProduct(t, 882, 881)
	look := newLossLookup(database.DB, adminUser())
	for _, c := range []struct {
		qty, price float64
		loss       bool
		lot        float64
		why        string
	}{
		{30, 550, true, 20, "数量30 → ロット20（600円）より安い"},
		{30, 600, false, 0, "同じ値段は赤字ではない"},
		{10, 900, true, 1, "数量10 → ロット20は超えるのでロット1（1000円）"},
		{10, 1000, false, 0, "ロット1と同じ"},
		{0.5, 900, true, 1, "数量がいちばん小さいロットより少ない → いちばん小さいロット（添える）"},
	} {
		x, ok := look.lossOf("000882", c.qty, c.price)
		if ok != c.loss || (ok && x.Lot != c.lot) {
			t.Errorf("%s: ok=%v x=%+v", c.why, ok, x)
		}
		if ok && c.qty < 1 && (!x.Fallback || !strings.Contains(lossText(x), "いちばん小さい個数")) {
			t.Errorf("いちばん小さいロットと比べたことを添えていません: %s", lossText(x))
		}
	}
	if x, ok := look.lossOf("000882", 30, 550); !ok || lossText(x) != "⚠ 赤字 50円（見積 600円・20個）" {
		t.Errorf("文が違います: %q", lossText(x))
	}
	if _, ok := look.lossOf("", 30, 1); ok {
		t.Errorf("弊社品番の無い行で赤字と言っています")
	}
}

// TestLossShowsOnOrderPageAndBacklog は2か所に出ることを固定します——受注ページの受注明細表（行の末尾に「見積との差」）と、
// 受注残表（行に薄赤の印と ⚠ の文）。赤字の行が無ければ列を足さない。
func TestLossShowsOnOrderPageAndBacklog(t *testing.T) {
	setupExtTest(t, "000890", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 891, -1, CustomerBoxTitle, "alice", "302", true)
	seedLossProduct(t, 892, 891)
	addPage(t, 893, -1, "受注", "alice", "302", true)
	row := func(qty, price string) string {
		return `<tr><td>000892</td><td>K-1</td><td>ブラケット</td><td>` + qty + `</td><td>個</td><td>` + price +
			`</td><td>2026-11-01</td><td></td><td></td><td>未着手</td></tr>`
	}
	seedOrder(t, 894, 893, "あけぼの精工", "2026-11-01", row("30", "550")) // 赤字
	seedOrder(t, 895, 893, "やまと工作所", "2026-11-02", row("30", "700")) // 黒字

	// 受注ページ——行の末尾に「見積との差」（赤字の行だけ文）。
	orderBody := func(rows string) string {
		return `<h1>受注</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerOrder + `</dd><dt>発注元</dt><dd>あけぼの精工</dd></dl>` +
			`<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>` +
			`<th>単価</th><th>納期</th><th>出荷済数量</th><th>備考</th><th>状態</th></tr>` + rows + `</tbody></table>`
	}
	body := orderBody(row("30", "550"))
	req := auth.WithUser(httptest.NewRequest("GET", "/000894", nil), adminUser())
	view := cms.RenderComputedViews(req, 894, body)
	if !strings.Contains(view, "見積との差") || !strings.Contains(view, "⚠ 赤字 50円（見積 600円・20個）") {
		t.Errorf("案件ページの明細に赤字の文がありません:\n%s", view)
	}
	body2 := orderBody(row("30", "700"))
	if v := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000895", nil), adminUser()), 895, body2); strings.Contains(v, "見積との差") {
		t.Errorf("赤字の無い案件ページに列を足しています")
	}
	gs := backlogGroups(adminUser())
	var lossRows, okRows int
	for _, g := range gs {
		for _, r := range g.Rows {
			if r.Loss != "" {
				lossRows++
				if !strings.Contains(r.Loss, "赤字 50円") {
					t.Errorf("案件残の赤字の文が違います: %q", r.Loss)
				}
			} else {
				okRows++
			}
		}
	}
	if lossRows != 1 || okRows != 1 {
		t.Errorf("案件残の赤字の行が %d・そうでない行が %d です（1と1のはず）", lossRows, okRows)
	}
	out := backlogViewHTML(adminUser(), 893)
	if !strings.Contains(out, `class="backlog-loss"`) || !strings.Contains(out, "backlog-loss-note") {
		t.Errorf("案件残表に赤字の印がありません")
	}
}
