package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// TestWorkHoursOnProductPage は、製造明細表の「加工時間」が加工製品ページの「⏱ 加工時間」に出ることを固定します（2026-10-21 夜）。
//
//   - 製造ページ（ページ種類：製造）の行で、弊社品番がこの加工製品・加工時間のある行だけ（新しい順）
//   - 1個あたりは 加工時間 ÷ 数量（3時間 ÷ 2個 = 1.5 時間）
//   - 製造ページでないページの同じ形の表は数えない
func TestWorkHoursOnProductPage(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	mfg := func(id int, started, hours string, kind string) {
		addPage(t, id, 0, "製造", "root", "302", true)
		body := `<h1>製造 ` + started + `</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + kind + `</dd><dt>` + MfgStartTag + `</dt><dd>` + started + `</dd></dl>` +
			`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>数量</th><th>` + WorkHoursLabel + `</th></tr>` +
			`<tr><td>000031</td><td>000030</td><td>K-1</td><td>2</td><td>` + hours + `</td></tr></tbody></table>`
		writeBodyFile(t, id, body)
		syncBody(t, id, body)
	}
	mfg(40, "2026-10-01", "3", PageKindMfg)
	mfg(41, "2026-10-15", "5", PageKindMfg)
	mfg(42, "2026-10-20", "", PageKindMfg)     // 書いていない行は出ない
	mfg(43, "2026-10-21", "9", PageKindProduct) // 製造ページでないものは数えない

	list := workHoursForProduct(adminUser(), 31)
	if len(list) != 2 || list[0].MfgPage != 41 || list[1].MfgPage != 40 {
		t.Fatalf("⚠ 加工時間の行が違います（41・40 の順を期待）: %+v", list)
	}
	got := workHoursHTML(list)
	for _, want := range []string{"⏱ 加工時間", "2026-10-15", "5 時間", "2.5 時間", "1.5 時間", `href="/000030"`} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 「⏱ 加工時間」に %q がありません:\n%s", want, got)
		}
	}
	if !strings.Contains(workHoursHTML(nil), "まだありません") {
		t.Errorf("行の無いときの断りがありません")
	}

	// 加工製品ページの表示に出る（受注・見積の単価の下）。
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		`<table><caption>支給部品表</caption><tbody><tr><th>品名</th><th>個数</th></tr><tr><td>x</td><td>1</td></tr></tbody></table>`
	writeBodyFile(t, 31, body)
	syncBody(t, 31, body)
	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000031", nil), adminUser()), 31, body)
	if i, j := strings.Index(view, "受注・見積の単価"), strings.Index(view, "⏱ 加工時間"); i < 0 || j < i {
		t.Errorf("⚠ 加工製品ページで「⏱ 加工時間」が受注・見積の単価の下に出ていません:\n%s", view)
	}
}
