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

// 見積の依頼の案件は、見積を出して完了（2026-10-19・quote_done.go）。
// 利用者:「見積依頼の案件は見積提出で完了です。その品が注文された場合、DBが検索され販売価格が表示されます。」

func caseRow(our, itemNo, name, qty string) string {
	return `<tr><td>` + our + `</td><td>` + itemNo + `</td><td>` + name + `</td><td>` + qty +
		`</td><td>個</td><td></td><td></td><td></td><td></td><td>未着手</td></tr>`
}

// TestPickEstimateOption は数量に使う見積表の選び方を固定します。
func TestPickEstimateOption(t *testing.T) {
	opts := []estimateOption{
		{Line: estimateLine{Quantity: "1", Price: "1000"}},
		{Line: estimateLine{Quantity: "20", Price: "600"}},
		{Line: estimateLine{Quantity: "50", Price: "500"}},
		{Why: "単価がまだ記録されていません"},
	}
	for _, c := range []struct {
		qty  float64
		want string
		why  string
	}{
		{20, "600", "ロットが同じ表"},
		{30, "600", "数量以下でいちばん大きいロット"},
		{0.5, "1000", "数量より小さいロットが無ければ、いちばん小さいロット"},
		{100, "500", "数量以下でいちばん大きいロット（50）"},
	} {
		got, ok := pickEstimateOption(opts, c.qty)
		if !ok || got.Price != c.want {
			t.Errorf("%s: 数量 %v → %+v（%v）・期待 %s", c.why, c.qty, got, ok, c.want)
		}
	}
	if _, ok := pickEstimateOption([]estimateOption{{Why: "無い"}}, 1); ok {
		t.Errorf("⚠ 単価の無い表を選びました")
	}
}

// TestMfgEstimateCompletesQuoteCase は、
//   - 製造ページの「この製造の見積書を作る」で、見積の依頼の行から見積書が製造ページの子にでき、行に案件が写る
//     （単価は数量以下でいちばん大きいロット・弊社品番の無い行は単価を空にして断る）
//   - 見積書を出す（送付日）と、その案件は案件残表から外れ、案件ページの足元に「完了」と出る
//   - 注文の案件の案件明細表の単価の欄に、出した見積の単価と見積書へのリンクが出る
//
// ことを固定します。
func TestMfgEstimateCompletesQuoteCase(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 991, -1, "案件", "alice", "302", true)
	// 加工製品: 見積表はロットごとに1枚（ロット1 1000円・ロット20 600円）。
	prod := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindProduct + `</dd></dl>` +
		recordTable("1", "", "900", "10", "1000", "") + recordTable("20", "", "540", "10", "600", "")
	addPage(t, 992, 991, "ブラケット", "alice", "302", true)
	writeBodyFile(t, 992, prod)
	if err := cms.SyncIndex("000992", prod); err != nil {
		t.Fatal(err)
	}
	// 同じ品の注文の案件（あとで単価の欄を見る）——ページ番号は自動で振られる前に置く。
	seedOrder(t, 994, 991, "あけぼの精工", "2026-12-01", caseRow("000992", "Q-1", "ブラケット", "30"))
	seedQuoteCase(t, 993, 991, "あけぼの精工", "2026-11-01",
		caseRow("000992", "Q-1", "ブラケット", "30")+caseRow("", "Q-2", "カバー", "5"))

	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000993", "row": 0, "item": "Q-1", "qty": "30"}, {"order": "000993", "row": 1, "item": "Q-2", "qty": "5"}}})
	if code != 200 {
		t.Fatalf("製造ページを作れません: %d %v", code, out)
	}
	mfg := out["page_id"].(string)
	req := auth.WithUser(httptest.NewRequest("GET", "/"+mfg, nil), u)
	if got := cms.RenderComputedViews(req, pageNum(mfg), readPageBody(t, mfg)); !strings.Contains(got, "mfg-act-estimate") {
		t.Errorf("⚠ 製造ページに「この製造の見積書を作る」がありません:\n%s", got)
	}

	code, out = postRFQ(t, u, MfgEstimateAPIHandler, map[string]any{"page_id": mfg})
	if code != 200 {
		t.Fatalf("見積書を作れません: %d %v", code, out)
	}
	est := out["page_id"].(string)
	// 2026-10-21 から、置き場は記録で、明細の「製造」の列にこの製造ページを書く。
	if got, want := placeOf(est), recordPath(RecordRequestFolder, time.Now()); got != want {
		t.Errorf("⚠ 見積書の置き場が %s です（%s のはず）", got, want)
	}
	if body := readPageBody(t, est); !strings.Contains(body, "<td>"+mfg+"</td>") {
		t.Errorf("⚠ 見積明細表の「製造」に %s がありません:\n%s", mfg, body)
	}
	if m, _ := out["missing"].(float64); m != 1 {
		t.Errorf("⚠ 単価の無い行の数が %v です（1 のはず）", out["missing"])
	}
	body := readPageBody(t, est)
	for _, want := range []string{"<td>600</td>", "20 個の単価", "<td>000993</td>", "<td>30</td>", "⚠ 弊社品番がありません", "<dd>あけぼの精工</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 見積書に %q がありません:\n%s", want, body)
		}
	}

	// 出す前は、案件残表にまだ並ぶ。
	if view := backlogViewHTML(u, 991); !strings.Contains(view, `>見積 000993</a>`) {
		t.Fatalf("⚠ 見積を出す前なのに、案件残表に見積の依頼がありません:\n%s", view)
	}
	if code, out := postRFQ(t, u, EstimateMarkAPIHandler, map[string]any{"page_id": est, "mark": "sent"}); code != 200 {
		t.Fatalf("送付日を付けられません: %d %v", code, out)
	}
	if view := backlogViewHTML(u, 991); strings.Contains(view, `000993`) {
		t.Errorf("⚠ 見積を出したのに、案件残表に見積の依頼が残っています:\n%s", view)
	}
	quoteBody := `<h1>【顧客】見積依頼</h1><table data-type="` + clientOrderItemsType + `"><caption>案件明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>納期</th><th>出荷済数量</th><th>備考</th><th>状態</th></tr>` +
		caseRow("000992", "Q-1", "ブラケット", "30") + caseRow("", "Q-2", "カバー", "5") + `</tbody></table>`
	if got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000993", nil), u), 993, quoteBody); !strings.Contains(got, "見積書 /"+est+" を出したので完了です") {
		t.Errorf("⚠ 見積の依頼の案件ページの足元に「完了」がありません:\n%s", got)
	}

	// 同じ品の注文の案件——単価の欄に、出した見積の単価と見積書へのリンク。
	orderBody := strings.Replace(quoteBody, caseRow("", "Q-2", "カバー", "5"), "", 1)
	got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000994", nil), u), 994, orderBody)
	if !strings.Contains(got, `<a href="/`+est+`">見積 600円（`) {
		t.Errorf("⚠ 注文の案件明細に、前の見積の単価と見積書へのリンクがありません:\n%s", got)
	}
}
