package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// TestCaseProfit は原価と利益の数え方を固定します（2026-10-21 夜・【要求】案件 §5 の下書き）。
//
//   - 売上 ＝ 案件明細表の 単価 × 数量（完了の行は入れない・単価の無い行は数を添える）
//   - 原価 ＝ この案件に当たる部材発注明細表の 単価 × 数量（内訳で分ける・取消は入れない・発注済と未発注を分ける・「追加」も入れる）
//   - 粗利 ＝ 売上 − 原価、利益率 ＝ 粗利 ÷ 売上
//   - 案件ページの表示に「💹 原価と利益」、ビュー「原価と利益の一覧」に1行
func TestCaseProfit(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 60, 0, "案件", "root", "302", true)
	addPage(t, 61, 0, "部材発注", "root", "302", true)
	caseBody := `<h1>案件</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerOrder + `</dd><dt>` + OrderedAtTag + `</dt><dd>2026-10-01</dd></dl>` +
		`<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>10</td><td>1,000</td><td></td></tr>` +
		`<tr><td>000032</td><td>K-2</td><td>差し替えた行</td><td>5</td><td>9,999</td><td>完了</td></tr>` +
		`<tr><td>000033</td><td>K-3</td><td>単価の無い行</td><td>2</td><td></td><td></td></tr></tbody></table>`
	writeBodyFile(t, 60, caseBody)
	syncBody(t, 60, caseBody)
	po := `<h1>部材発注</h1><table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>内訳</th><th>材質</th><th>数量</th><th>単価</th><th>備考</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>000060</td><td></td><td>鉄</td><td>4</td><td>500</td><td></td><td>発注済</td></tr>` + // 発注済 2,000
		`<tr><td>000031</td><td></td><td>000060/000031:2、000099/000031:3</td><td>SUS</td><td>5</td><td>100</td><td></td><td>未発注</td></tr>` + // 未発注 200（2個ぶん）
		`<tr><td>000031</td><td>000060</td><td></td><td>銅</td><td>1</td><td>300</td><td>追加 作り直し</td><td>納品済</td></tr>` + // 発注済 300（追加も原価）
		`<tr><td>000031</td><td>000060</td><td></td><td>鉛</td><td>9</td><td>1000</td><td></td><td>取消</td></tr>` + // 入れない
		`<tr><td>000031</td><td>000060</td><td></td><td>錫</td><td>1</td><td></td><td></td><td>発注済</td></tr>` + // 単価の無い行
		`</tbody></table>`
	writeBodyFile(t, 61, po)
	syncBody(t, 61, po)

	u := adminUser()
	all, _ := caseProfits(database.DB, func(int) bool { return true })
	p := all[60]
	if p == nil {
		t.Fatalf("案件 60 が数えられていません: %+v", all)
	}
	if p.Sales != 10000 || p.SalesNoPrice != 1 {
		t.Errorf("⚠ 売上 %v（単価の無い行 %d）——10000（1）を期待", p.Sales, p.SalesNoPrice)
	}
	if p.CostSent != 2300 || p.CostUnsent != 200 || p.CostNoPrice != 1 {
		t.Errorf("⚠ 原価 発注済 %v・未発注 %v・単価の無い行 %d——2300・200・1 を期待", p.CostSent, p.CostUnsent, p.CostNoPrice)
	}
	if r, ok := p.rate(); !ok || r != 75 {
		t.Errorf("⚠ 利益率 %v——75 を期待（粗利 7,500 ÷ 売上 10,000）", r)
	}

	view := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000060", nil), u), 60, caseBody)
	for _, want := range []string{"💹 原価と利益", "10,000円", "2,500円", "7,500円", "75%"} {
		if !strings.Contains(view, want) {
			t.Errorf("⚠ 案件ページに %q がありません:\n%s", want, view)
		}
	}
	if i, j := strings.Index(view, "💹 原価と利益"), strings.Index(view, "</table>"); i < j {
		t.Errorf("⚠ 「💹 原価と利益」が表の中にあります")
	}
	list := caseProfitListHTML(u, 1)
	if !strings.Contains(list, `href="/000060"`) || !strings.Contains(list, "2026-10-01") || !strings.Contains(list, "75%") {
		t.Errorf("⚠ 原価と利益の一覧に案件の行がありません:\n%s", list)
	}
	if signedYen(-1234.4) != "−1,234円" {
		t.Errorf("負の金額: %q", signedYen(-1234.4))
	}
}

// TestCaseProfitDeliveredAtDeliveryPrice は、もう納めた分の売上を納品書の行の単価で数えることを固定します（2026-10-28）。
// 100 個のうち 80 個をタップ無しで単価を下げて先に納めたら、売上は 80 × 下げた単価 ＋ 残り 20 × 案件の単価。
func TestCaseProfitDeliveredAtDeliveryPrice(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 60, 0, "案件", "root", "302", true)
	caseBody := `<h1>案件</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindCustomerOrder + `</dd></dl>` +
		`<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>K-1</td><td>プーリー</td><td>100</td><td>1,000</td><td></td></tr></tbody></table>`
	writeBodyFile(t, 60, caseBody)
	syncBody(t, 60, caseBody)
	dn := func(id int, qty, price string) {
		t.Helper()
		body := `<h1>【弊社】納品書</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindOurDelivery + `</dd></dl>` +
			`<table><caption>納品明細表</caption><tbody><tr><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th><th>備考</th></tr>` +
			`<tr><td>000060</td><td>K-1</td><td>プーリー</td><td>` + qty + `</td><td>` + price + `</td><td>タップ無</td></tr></tbody></table>`
		addPage(t, id, 0, "納品書", "root", "302", false)
		syncBody(t, id, body)
	}
	dn(62, "30", "600")
	dn(63, "50", "600")
	all, _ := caseProfits(database.DB, func(int) bool { return true })
	if p := all[60]; p == nil || p.Sales != 80*600+20*1000 || p.SalesNoPrice != 0 {
		t.Fatalf("⚠ 売上 %+v——80×600＋20×1000＝68000 を期待", p)
	}
	// 単価の無い納品書の行は、数は引いて「単価の無い行」に数える（案件の単価で数え直さない）。
	dn(64, "20", "")
	all, _ = caseProfits(database.DB, func(int) bool { return true })
	if p := all[60]; p == nil || p.Sales != 80*600 || p.SalesNoPrice != 1 {
		t.Errorf("⚠ 売上 %+v——48000（単価の無い行 1）を期待", p)
	}
}
