package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 製造ページ（2026-10-19・mfg.go）。

// TestMfgTitle は製造ページの題（装置名、装置名…・無ければ品名・長ければ「ほか N」）を固定します。
func TestMfgTitle(t *testing.T) {
	for _, c := range []struct {
		lines []mfgLine
		want  string
	}{
		{[]mfgLine{{Machine: "ボール回転機", ItemName: "ブラケット"}, {Machine: "ボール回転機", ItemName: "カバー"}, {ItemName: "シャフト"}}, "ボール回転機、シャフト"},
		{[]mfgLine{{ItemNo: "A-1"}}, "A-1"},
		{[]mfgLine{{Machine: "とても長い装置の名前その一"}, {Machine: "とても長い装置の名前その二"}, {Machine: "とても長い装置の名前その三"}}, "とても長い装置の名前その一、とても長い装置の名前その二、ほか 1"},
	} {
		if got := mfgTitle(c.lines); got != c.want {
			t.Errorf("題が %q です（%q を期待）", got, c.want)
		}
	}
}

// TestMfgNewFromBacklogRows は、
//   - 受注残表で選んだ行から製造ページを作る（置き場は「製造」・題・製造番号・ページ種類・製造明細表に受注明細の写しと今回作る数）
//   - 一部だけで始められ、受注残表の行に「製造中」のリンクと、まだ製造に入っていない数（残りの選ぶ欄）が出る
//   - まだ製造に入っていない数より多くは入れられない・入れ切ったら選ぶ欄が消える
//   - 1つの製造は1社
//
// ことを固定します。
func TestMfgNewFromBacklogRows(t *testing.T) {
	setupExtTest(t, "000970", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 971, -1, "受注", "alice", "302", true)
	seedOrder(t, 972, 971, "あけぼの精工", "2026-11-01", item("A-1", "ブラケット", "100", "2026-11-01", "")+item("A-2", "カバー", "5", "2026-11-01", "")+item("A-3", "台", "3", "2026-11-01", ""))
	seedOrder(t, 973, 971, "やまと工作所", "2026-11-05", item("B-1", "シャフト", "10", "2026-11-05", ""))

	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000972", "row": 0, "item": "A-1", "qty": "40"}, // 一部だけ（100個のうち40個）
		{"order": "000972", "row": 1, "item": "A-2", "qty": "5"},
	}})
	if code != 200 {
		t.Fatalf("作れません: %d %v", code, out)
	}
	id := out["page_id"].(string)
	body := readPageBody(t, id)
	for _, want := range []string{
		"<h1>ブラケット、カバー</h1>",
		"<dt>" + MfgNoTag + "</dt><dd>" + id + "</dd>",
		"<dt>" + DeliveryClientTag + "</dt><dd>あけぼの精工</dd>",
		"<dt>" + PageKindTag + "</dt><dd>" + PageKindMfg + "</dd>",
		"<caption>製造明細表</caption>",
		"<td>000972</td><td>A-1</td><td>ブラケット</td><td></td><td>40</td><td>個</td><td>2026-11-01</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("製造ページに %q がありません:\n%s", want, body)
		}
	}
	if got := placeOf(id); got != MfgBoxTitle {
		t.Errorf("置き場が %s です（%s のはず）", got, MfgBoxTitle)
	}

	view := backlogViewHTML(u, 971)
	if !strings.Contains(view, `<a class="backlog-mfg-link" href="/`+id+`">製造中</a>`) || !strings.Contains(view, `data-free="60"`) {
		t.Errorf("⚠ 受注残表に「製造中」と、残りの 60 が出ていません:\n%s", view)
	}

	// まだ製造に入っていない数（60）より多くは入れられない。
	if code, _ := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000972", "row": 0, "item": "A-1", "qty": "61"}}}); code != 409 {
		t.Errorf("⚠ 製造に入っていない数より多く入れられます: %d", code)
	}
	if code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000972", "row": 0, "item": "A-1", "qty": "60"}}}); code != 200 {
		t.Errorf("残りの 60 で始められません: %d %v", code, out)
	}
	view = backlogViewHTML(u, 971)
	if strings.Contains(view, `data-order="000972" data-row="0" data-item="A-1" data-name="ブラケット" data-free=`) {
		t.Errorf("⚠ 入れ切った行に、まだ選ぶ欄があります:\n%s", view)
	}
	// 1つの製造は1社。
	if code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{
		{"order": "000972", "row": 2, "item": "A-3", "qty": "1"}, {"order": "000973", "row": 0, "item": "B-1", "qty": "1"},
	}}); code != 400 || !strings.Contains(out["message"].(string), "1社") {
		t.Errorf("⚠ 客先の違う行を1つの製造に混ぜています: %d %v", code, out)
	}
}
