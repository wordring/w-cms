package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 見積依頼の段3（2026-10-04）——受注フォルダの受注残表で選んだ行を見積依頼へ。【要求】見積依頼 §2「受注フォルダの受注残表から
// 集める——選んだものだけ」「数は受注残の数量そのまま（数量 − 出荷済み）× 部材の数量——手配済みは引かない」。

// TestRFQCollectFromBacklog は、⚠ **送り先が無ければ見積依頼の置き場へ入り**、数は受注残 × 部材の数量、行の「受注」の列に
// 受注ページが入ることを固定します。
func TestRFQCollectFromBacklog(t *testing.T) {
	u := seedRFQ(t)
	code, out := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{
		"items": []map[string]string{{"product": "000051", "lot": "3", "for_order": "000060"}},
	})
	if code != 200 || out["page_id"] != "000050" {
		t.Fatalf("見積依頼の置き場へ入っていません: %d %v", code, out)
	}
	rows := rfqRows(t, boxBody(t), RFQNeedsType, 1)
	if len(rows) != 2 {
		t.Fatalf("見積依頼必要部材表の行が %d です（材料2行）: %+v", len(rows), rows)
	}
	got := map[string]string{}
	for _, r := range rows {
		if r.ForOrder != "000060" {
			t.Errorf("⚠ 行の「受注」の列に受注ページが入っていません: %+v", r)
		}
		got[r.Material] = r.Quantity
	}
	if got["鉄"] != "6" || got["SUS304"] != "3" {
		t.Errorf("数が受注残 × 部材の数量ではありません（鉄 3×2=6・SUS304 3×1=3）: %v", got)
	}
	if code, _ := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{
		"items": []map[string]string{{"product": "000051", "lot": "3", "for_order": "受注"}},
	}); code != 409 && code != 400 {
		t.Errorf("読めない受注ページの番号なのに %d", code)
	}
}

// TestBacklogRFQPick は受注残表の「見積依頼」の欄を固定します——紙に出さない列・選ぶ欄（弊社品番・受注残・受注ページ）・
// 弊社品番が空か残が無い行は選べない・送るボタンも紙に出さない。
func TestBacklogRFQPick(t *testing.T) {
	setupExtTest(t, "000600", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 601, -1, "受注", "alice", "302", true)
	seedOrderUnder(t, 602, 601, [2]string{"000777", "K120-01-211"}, [2]string{"", "K120-99-999"})

	out := backlogViewHTML(adminUser(), 601)
	for _, want := range []string{`<th class="no-print">見積依頼</th>`,
		`class="backlog-rfq-pick" title="この行の部材を見積依頼へ" data-product="000777" data-lot="100" data-order="000602"`,
		`<input type="checkbox" disabled title="弊社品番が空なので見積依頼へ送れません">`,
		`<div class="backlog-rfq-bar no-print"><button type="button" class="backlog-rfq-go">`} {
		if !strings.Contains(out, want) {
			t.Errorf("受注残表に %q がありません:\n%s", want, out)
		}
	}
	if got := rfqPickHTML(backlogRow{OurItemNo: "000777", Remaining: 0, OrderPageID: "000602"}); !strings.Contains(got, "受注残がありません") {
		t.Errorf("残が無い行は選べないはず: %s", got)
	}
}
