package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 見積依頼必要部材表の重複は「同じもの・同じロット」（2026-10-14・【要求】部材見積依頼 §2）——rfq.go の rfqDupKey・ensureLotColumn。
//
// 利用者（2026-10-02）:「見積依頼必要部材表に同時に同じもので同じロットが出ていたらに赤くしておけば十分だと思います」。

// TestRFQDupKeyLooksAtLot は重複の鍵を固定します——同じもの・同じロットは重複、同じもの・違うロットは重複でない
// （数量が同じでも）、ロットの分からない行は数量と単位で見て、ロットの分かる行とは重ならない。
func TestRFQDupKeyLooksAtLot(t *testing.T) {
	iron := func(lot, qty string) ourOrderLine {
		return ourOrderLine{Kind: "材料", Material: "鉄", Shape: "板", Size: "t3.2*100*200", Lot: lot, Quantity: qty, Unit: "枚"}
	}
	cases := []struct {
		name string
		a, b ourOrderLine
		dup  bool
	}{
		{"同じもの・同じロット・同じ数", iron("5", "10"), iron("5", "10"), true},
		// 別の加工製品が同じ材料を使う——1個あたりの数が違えば、同じロットでも数量は違う。
		{"同じもの・同じロット・違う数（別の加工製品）", iron("5", "10"), func() ourOrderLine { l := iron("5", "5"); l.ProductID = "000099"; return l }(), true},
		{"同じもの・違うロット・同じ数", iron("5", "10"), iron("10", "10"), false},
		{"同じもの・違うロット・違う数", iron("5", "10"), iron("6", "12"), false},
		{"違うもの・同じロット", iron("5", "10"), ourOrderLine{Material: "SUS304", Shape: "板", Size: "t3.2*100*200", Lot: "5", Quantity: "10", Unit: "枚"}, false},
		{"ロットは全角半角を畳む", iron("5", "10"), iron("５", "10"), true},
		{"種類は見ない（臨時部材表には種類の列が無い）", iron("5", "10"), func() ourOrderLine { l := iron("5", "10"); l.Kind = ""; return l }(), true},
		// ロットの分からない行（臨時部材表・ロットの列が無かった頃の表）——数量と単位で見る（それまでの見方）。
		{"ロットなし同士・同じ数", iron("", "10"), iron("", "10"), true},
		{"ロットなし同士・違う数", iron("", "10"), iron("", "12"), false},
		{"ロットなしと、ロットあり（数は同じ）", iron("", "10"), iron("5", "10"), false},
		{"ロットなしと、数がロットと同じ行", iron("", "5"), iron("5", "10"), false},
	}
	for _, c := range cases {
		if got := rfqDupKey(c.a) == rfqDupKey(c.b); got != c.dup {
			t.Errorf("%s: 重複=%v（%v のはず）", c.name, got, c.dup)
		}
	}
}

// oldRFQNeedsHTML はロットの列が無かった頃（2026-10-13 まで）の見積依頼必要部材表です（節で包む・見出しは当時の宣言）。
func oldRFQNeedsHTML(rows ...string) string {
	var b strings.Builder
	b.WriteString(`<section><table><caption>見積依頼必要部材表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>受注</th><th>種類</th><th>番号</th><th>品番</th><th>品名</th><th>加工内容</th><th>材質</th>` +
		`<th>形状</th><th>寸法</th><th>表面</th><th>仕様</th><th>支給</th><th>数量</th><th>単位</th><th>備考</th></tr>`)
	for _, r := range rows {
		b.WriteString(r)
	}
	b.WriteString(`</tbody></table></section>`)
	return b.String()
}

// oldRow はロットの列の無い表の1行です（材質・形状・寸法・数量）。
func oldRow(material, shape, size, qty string) string {
	return "\n      <tr>\n        <td>000051</td><td></td><td>材料</td><td></td><td></td><td></td><td></td>" +
		"<td>" + material + "</td><td>" + shape + "</td><td>" + size + "</td><td></td><td></td><td></td>" +
		"<td>" + qty + "</td><td></td><td></td>\n      </tr>"
}

// TestRFQOldNeedsTableStillReads は、⚠ **ロットの列が無かった頃の見積依頼必要部材表**（職場の置き場に在る）が壊れずに読め、
// 重複は数で見て、集めた行を足すと `数量` の前にロットの列が足されて（古い行は空）、ロットが落ちないことを固定します。
func TestRFQOldNeedsTableStillReads(t *testing.T) {
	u := seedRFQ(t)
	old := `<h1>` + RFQBoxTitle + `</h1>` + `<section data-mirror="再部材見積依頼"></section>` +
		tableOfLinesHTML(RFQTempPartsType, nil, false) +
		oldRFQNeedsHTML(oldRow("鉄", "板", "t3.2*100*200", "10"), oldRow("鉄", "板", "t3.2*100*200", "10"), oldRow("SUS304", "丸棒", "φ20*50", "5"))
	writeBodyFile(t, rfqBoxID, old)

	// ① 読める——古い行の数量はそのまま・ロットは空。重複は数で見る（鉄の2行）。
	got := rfqRows(t, old, RFQNeedsType, 1)
	if len(got) != 3 || got[0].Quantity != "10" || got[2].Material != "SUS304" || got[0].Lot != "" {
		t.Fatalf("古い表が読めません: %+v", got)
	}
	req := auth.WithUser(httptest.NewRequest("GET", "/000050", nil), u)
	shown := cms.RenderComputedViews(req, rfqBoxID, old)
	if n := strings.Count(shown, `class="rfq-dup-mark"`); n != 2 {
		t.Errorf("古い表の重複の印が %d です（数の同じ鉄の2行で2のはず）:\n%s", n, shown)
	}

	// ② 集めて足す——ロットの列が `数量` の前にでき、古い行は空・新しい行にロット。
	if code, out := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"}); code != 200 {
		t.Fatalf("古い表へ集められません: %d %v", code, out)
	}
	body := boxBody(t)
	if !strings.Contains(body, `<th>支給</th><th>ロット</th><th>数量</th>`) {
		t.Errorf("ロットの列が数量の前に足されていません:\n%s", body)
	}
	got = rfqRows(t, body, RFQNeedsType, 1)
	if len(got) != 5 {
		t.Fatalf("行の数が %d です（古い3行＋集めた2行）: %+v", len(got), got)
	}
	for i, want := range []struct{ material, lot, qty string }{
		{"鉄", "", "10"}, {"鉄", "", "10"}, {"SUS304", "", "5"}, {"鉄", "5", "10"}, {"SUS304", "5", "5"},
	} {
		if got[i].Material != want.material || got[i].Lot != want.lot || got[i].Quantity != want.qty {
			t.Errorf("%d行目が %+v です（材質 %s・ロット %q・数量 %s のはず——列がずれていないか）", i+1, got[i], want.material, want.lot, want.qty)
		}
	}
	// 古い行（ロットなし）と集めた行（ロット5）は、数が同じでも重ならない。古い鉄の2行は重なったまま。
	shown = cms.RenderComputedViews(req, rfqBoxID, body)
	if n := strings.Count(shown, `class="rfq-dup-mark"`); n != 2 {
		t.Errorf("重複の印が %d です（古い鉄の2行だけで2のはず）:\n%s", n, shown)
	}

	// ③ もう一度足しても、列は1つのまま。
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"})
	if body := boxBody(t); strings.Count(body, `<th>ロット</th>`) != 1 {
		t.Errorf("ロットの列が2つできました:\n%s", body)
	}
}

// TestRFQOldDraftKeepsLot は、ロットの列が無かった頃の見積依頼部材表へ「入れる先」で足しても、ロットが落ちないこと
// （列が足される）と、そこから ↩ 戻した行がロットを持って戻ることを固定します。
func TestRFQOldDraftKeepsLot(t *testing.T) {
	u := seedRFQ(t)
	postRFQ(t, u, RFQCollectAPIHandler, map[string]any{"page_id": "000050", "product": "000051", "lot": "5"})
	// ロットの列の無い見積依頼部材表を1枚置く（当時の宣言の見出し）。
	oldDraft := `<section><table><caption>見積依頼部材表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>受注</th><th>種類</th><th>番号</th><th>品番</th><th>品名</th><th>加工内容</th><th>材質</th>` +
		`<th>形状</th><th>寸法</th><th>表面</th><th>仕様</th><th>支給</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>` +
		`<tr><td></td><td></td><td>購入部品</td><td></td><td>M8-20</td><td>六角ボルト</td><td></td><td></td><td></td><td></td>` +
		`<td></td><td></td><td></td><td>10</td><td>本</td><td></td><td></td><td></td></tr></tbody></table></section>`
	writeBodyFile(t, rfqBoxID, boxBody(t)+oldDraft)

	if code, out := postRFQ(t, u, RFQNeedsMoveAPIHandler, map[string]any{"page_id": "000050", "rows": []int{1}, "into": "1"}); code != 200 || out["rows"] != float64(1) {
		t.Fatalf("古い見積依頼部材表へ足せません: %d %v", code, out)
	}
	got := rfqRows(t, boxBody(t), RFQDraftType, 1)
	if len(got) != 2 || got[0].ItemName != "六角ボルト" || got[0].Quantity != "10" || got[0].Lot != "" ||
		got[1].Material != "鉄" || got[1].Lot != "5" || got[1].Quantity != "10" {
		t.Fatalf("古い見積依頼部材表の行が違います（古い行はそのまま・足した行にロット5）: %+v", got)
	}
	if code, out := postRFQ(t, u, RFQDraftBackAPIHandler, map[string]any{"page_id": "000050", "table": 1, "row": 2}); code != 200 {
		t.Fatalf("戻せません: %d %v", code, out)
	}
	needs := rfqRows(t, boxBody(t), RFQNeedsType, 1)
	if len(needs) != 2 || needs[1].Material != "鉄" || needs[1].Lot != "5" {
		t.Errorf("戻した行がロットを持っていません: %+v", needs)
	}
}
