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
	// 「進み」に製造中のリンク、先頭のチェックに、まだ製造に入っていない数（2026-10-21 から）。
	if !strings.Contains(view, `<a class="backlog-mfg-link" href="/`+id+`">製造中</a>`) || !strings.Contains(view, `まだ製造に入っていない数 60`) {
		t.Errorf("⚠ 案件残表に「製造中」と、残りの 60 が出ていません:\n%s", view)
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

// TestMfgMadeAndDeliveryFromMfgPage は（2026-10-19・作る順の2番）、
//   - 製造ページの「製造完了」を /api/mfg/made で書ける（済・空のほかは断る・いまの値が違えば 409）
//   - 案件明細の製造完了は鏡——製造完了の製造に入った数が全部なら「済」、一部なら「N/M」（案件残表にも出る）
//   - 製造ページから作った納品書は、その製造ページの子にでき、製造明細表の「納品」の欄の残りが減る
//
// ことを固定します。
func TestMfgMadeAndDeliveryFromMfgPage(t *testing.T) {
	setupExtTest(t, "000980", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 981, -1, "案件", "alice", "302", true)
	seedOrder(t, 982, 981, "あけぼの精工", "2026-11-01", item("A-1", "ブラケット", "100", "2026-11-01", ""))

	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000982", "row": 0, "item": "A-1", "qty": "40"}}})
	if code != 200 {
		t.Fatalf("製造ページを作れません: %d %v", code, out)
	}
	mfg := out["page_id"].(string)

	// 製造完了を付ける。
	if code, out := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item_no": "A-1", "old": "", "value": "済"}); code != 200 {
		t.Fatalf("製造完了を書けません: %d %v", code, out)
	}
	if body := readPageBody(t, mfg); !strings.Contains(body, "<td>済</td>") {
		t.Errorf("⚠ 製造明細表に「済」が入っていません:\n%s", body)
	}
	if code, _ := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item_no": "A-1", "old": "", "value": "済"}); code != 409 {
		t.Errorf("⚠ いまの値が違うのに書けます: %d", code)
	}
	if code, _ := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item_no": "A-1", "old": "済", "value": "半分"}); code != 400 {
		t.Errorf("⚠ 済・空のほかを書けます: %d", code)
	}
	// 案件残表の製造完了は 40/100（製造完了の製造に入ったのは40）。
	if view := backlogViewHTML(u, 981); !strings.Contains(view, `製造完了 40/100`) {
		t.Errorf("⚠ 案件残表の製造完了が 40/100 になっていません:\n%s", view)
	}

	// 製造ページから納品書（行番号の代わりに品番で探す）——置き場は記録で、明細の「製造」にこの製造ページ（2026-10-21）。
	code, out = postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": mfg, "items": []map[string]any{{"order": "000982", "row": -1, "item": "A-1", "name": "ブラケット", "qty": "15"}}})
	if code != 200 {
		t.Fatalf("製造ページから納品書を作れません: %d %v", code, out)
	}
	dlv := out["page_id"].(string)
	if got, want := placeOf(dlv), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("納品書の置き場が %s です（%s のはず）", got, want)
	}
	if body := readPageBody(t, dlv); !strings.Contains(body, "<td>000982</td><td>"+mfg+"</td>") {
		t.Errorf("⚠ 納品明細表の「製造」に %s がありません:\n%s", mfg, body)
	}
	// 製造明細表の「納品」の欄は、残り 25（40 − 15）。
	got := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/"+mfg, nil), u), pageNum(mfg), readPageBody(t, mfg))
	if !strings.Contains(got, `data-left="25"`) || !strings.Contains(got, "製造完了: 1 / 1 行") {
		t.Errorf("⚠ 製造ページの「納品」の残りが 25 になっていないか、製造完了の数がありません:\n%s", got)
	}
}

// TestMfgPartLinesCapsToMfgQty は（2026-10-19・作る順の3番）、製造ページの部材が
//   - まだ手配していない分（発注済みの板は出ない）で、この製造で作る数ぶんまで（FB は残り3だが、製造は2個なので2）
//   - 案件（for-order）と弊社品番を運ぶ
//
// ことを固定します。
func TestMfgPartLinesCapsToMfgQty(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	addPage(t, 33, 0, "ブラケット", "root", "302", true)
	syncBody(t, 33, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`+
		`<table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>000030</td><td>K-1</td><td>ブラケット</td><td>2</td></tr></tbody></table>`)
	lines, skipped, err := mfgPartLines(&auth.User{Username: "root", IsAdmin: true}, 33)
	if err != nil || skipped != 0 {
		t.Fatalf("数えられません: %v（飛ばした %d）", err, skipped)
	}
	if len(lines) != 1 || lines[0].Shape != "FB" || lines[0].Quantity != "2" || lines[0].ForOrder != "000030" || lines[0].ProductID != "000031" {
		t.Errorf("⚠ 製造の部材が違います（FB を2・案件 000030・弊社品番 000031 だけのはず）: %+v", lines)
	}
}

// TestOurOrderFromMfgDraftGoesUnderMfgPage は、製造ページの発注部材表から作った部材発注書が、その製造ページの子にできることを固定します。
func TestOurOrderFromMfgDraftGoesUnderMfgPage(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 991, -1, "ブラケット", "alice", "302", true)
	syncBody(t, 991, `<h1>ブラケット</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindMfg+`</dd></dl>`)
	code, out := postRFQ(t, adminUser(), NewOurOrderAPIHandler, map[string]any{
		"supplier": "みなと商店", "draft_page": "000991", "draft_index": "1",
		"lines": []map[string]any{{"product_id": "000031", "for_order": "000030", "material": "鉄", "shape": "FB", "size": "t4.5*75*1090", "quantity": "2"}},
	})
	if code != 200 {
		t.Fatalf("部材発注書を作れません: %d %v", code, out)
	}
	// 案件（for-order）も部材発注明細表へ写す（2026-10-19）。
	if body := readPageBody(t, out["page_id"].(string)); !strings.Contains(body, "<td>000030</td>") {
		t.Errorf("⚠ 部材発注明細表に案件 000030 が写っていません:\n%s", body)
	}
	if got, want := placeOf(out["page_id"].(string)), recordPath(RecordWorkFolder, time.Now()); got != want {
		t.Errorf("⚠ 部材発注書の置き場が %s です（%s のはず）", got, want)
	}
	if body := readPageBody(t, out["page_id"].(string)); !strings.Contains(body, "<td>000030</td><td>000991</td>") {
		t.Errorf("⚠ 部材発注明細表の「製造」に 000991 がありません:\n%s", body)
	}
}
