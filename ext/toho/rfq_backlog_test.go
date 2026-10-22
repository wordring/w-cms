package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 見積依頼の段3（2026-10-04）——受注フォルダの受注残表で選んだ行を部材見積依頼へ。【要求】部材見積依頼 §2「受注フォルダの受注残表から
// 集める——選んだものだけ」「数は受注残の数量そのまま（数量 − 出荷済数量）× 部材の数量——手配済みは引かない」。

// TestRFQCollectFromBacklog は、送り先（page_id）が要ること（2026-10-19 から——それまでの「空なら部材見積依頼の置き場」は置き場ごと
// なくした）と、数はロット × 部材の数量、行の「案件」の列に案件ページが入ることを固定します。
func TestRFQCollectFromBacklog(t *testing.T) {
	u := seedRFQ(t)
	if code, _ := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{
		"items": []map[string]string{{"product": "000051", "lot": "3", "for_order": "000060"}},
	}); code != 400 {
		t.Errorf("⚠ 送り先が無いのに集めます（置き場を作り直していないか）: %d", code)
	}
	code, out := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{
		"page_id": "000050", "items": []map[string]string{{"product": "000051", "lot": "3", "for_order": "000060"}},
	})
	if code != 200 || out["page_id"] != "000050" {
		t.Fatalf("送り先へ入っていません: %d %v", code, out)
	}
	rows := rfqRows(t, boxBody(t), RFQNeedsType, 1)
	if len(rows) != 2 {
		t.Fatalf("見積依頼必要部材表の行が %d です（材料2行）: %+v", len(rows), rows)
	}
	got := map[string]string{}
	for _, r := range rows {
		if r.ForOrder != "000060" {
			t.Errorf("⚠ 行の「受注」の列に案件ページが入っていません: %+v", r)
		}
		got[r.Material] = r.Quantity
	}
	if got["鉄"] != "6" || got["SUS304"] != "3" {
		t.Errorf("数が案件残 × 部材の数量ではありません（鉄 3×2=6・SUS304 3×1=3）: %v", got)
	}
	if code, _ := postRFQ(t, u, RFQCollectAPIHandler, map[string]any{
		"page_id": "000050", "items": []map[string]string{{"product": "000051", "lot": "3", "for_order": "受注"}},
	}); code != 409 && code != 400 {
		t.Errorf("読めない案件ページの番号なのに %d", code)
	}
}

// TestBacklogRFQPick は、案件残表に「部材見積依頼」の欄と「☑ 選んだ行を部材見積依頼へ」が無いことを固定します（2026-10-19 に外した——
// 部材見積依頼は製造ページから・mfg_more.go）。2026-10-19 利用者:「案件残表の「部材見積依頼」の欄を外して、部材見積依頼の置き場を消してください」
func TestBacklogRFQPick(t *testing.T) {
	setupExtTest(t, "000600", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 601, -1, "受注", "alice", "302", true)
	seedOrderUnder(t, 602, 601, [2]string{"000777", "K120-01-211"}, [2]string{"", "K120-99-999"})

	out := backlogViewHTML(adminUser(), 601)
	// 「🚚 選んだ行で納品書を作る」と「納品」の列も 2026-10-22 に外した（納品書は製造ページから）。
	for _, gone := range []string{"部材見積依頼</th>", "backlog-rfq-pick", "backlog-rfq-go", "backlog-dlv-go", "backlog-dlv-pick", "納品</th>"} {
		if strings.Contains(out, gone) {
			t.Errorf("⚠ 案件残表に %q が残っています:\n%s", gone, out)
		}
	}
	for _, want := range []string{"backlog-wait-go", ">🏭 製造開始</button>"} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ 案件残表の帯から %q まで消えています:\n%s", want, out)
		}
	}
}
