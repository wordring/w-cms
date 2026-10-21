package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// seedWaitOrder は「製造待ち」の列を持つ案件ページを、本文のファイルごと置きます（製造待ちの API は本文を書き換えるため）。
func seedWaitOrder(t *testing.T, id, root int, client, due, rows string) {
	t.Helper()
	addPage(t, id, root, "案件", "alice", "302", true)
	writeBodyFile(t, id, `<h1>案件</h1><dl data-type="tags">`+
		`<dt>`+PageKindTag+`</dt><dd>`+PageKindCustomerOrder+`</dd>`+
		`<dt>発注書番号</dt><dd>PO-`+page.FormatID(id)+`</dd>`+
		`<dt>発注元</dt><dd>`+client+`</dd><dt>納期</dt><dd>`+due+`</dd></dl>`+
		`<table data-type="`+clientOrderItemsType+`"><caption>案件明細表</caption><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>納期</th>`+
		`<th>出荷済数量</th><th>備考</th><th>状態</th><th>`+MfgWaitLabel+`</th></tr>`+rows+`</tbody></table>`)
}

func waitItem(itemNo, name, qty, due string) string {
	return `<tr><td></td><td>` + itemNo + `</td><td>` + name + `</td><td>` + qty + `</td><td>個</td><td>100</td><td>` + due +
		`</td><td></td><td></td><td>未着手</td><td></td></tr>`
}

// waitsOf は案件ページの案件明細の行ごとの「製造待ち」です（品番 → 数）。
func waitsOf(t *testing.T, id int) map[string]float64 {
	t.Helper()
	rows, err := cms.VocabTableRowsOf(database.DB, id, clientOrderItemsType)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, r := range rows {
		out[r.Values["item-id"]] = waitOf(r.Values)
	}
	return out
}

// TestMfgWaitFlow は、案件残表 →（🏭 製造）→ 製造待ち →（▶ 開始）→ 製造ページ と、どの段からも1つ前へ戻せることを固定します
// （2026-10-21・【考察】案件残から製造へ）。
//   - 「🏭 製造」は、まだ製造に入っていない数を、案件明細の「製造待ち」へ書く。案件残表では打ち消し線・「進み」に製造待ち。
//   - 「製造」の置き場の製造待ちのビューに、客先ごとに並ぶ。開始は1社。
//   - 開始すると製造ページができ、製造待ちは空に戻る。製造明細の「↩ 製造待ちへ」で1行ずつ戻せる（納めた分がある行は戻せない）。
//   - 「↩ 案件へ戻す」で製造待ちが空になり、案件残表で選べる形に戻る。
//   - 案件ページに書けない人は、製造待ちへ入れられない。
func TestMfgWaitFlow(t *testing.T) {
	setupExtTest(t, "000970", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 971, -1, "案件", "alice", "302", true)
	seedWaitOrder(t, 972, 971, "あけぼの精工", "2026-11-01", waitItem("A-1", "ブラケット", "100", "2026-11-01")+waitItem("A-2", "カバー", "5", "2026-11-01"))
	seedWaitOrder(t, 973, 971, "やまと工作所", "2026-11-05", waitItem("B-1", "シャフト", "10", "2026-11-05"))
	pick := func(order string, row int, item string) map[string]any {
		return map[string]any{"order": order, "row": row, "item": item}
	}

	// 書けない人は入れられない。
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, MfgWaitAPIHandler, map[string]any{"items": []any{pick("000972", 0, "A-1")}}); code != 403 {
		t.Errorf("⚠ 案件ページに書けない人が製造待ちへ入れられます: %d", code)
	}

	// 🏭 製造。
	if code, out := postRFQ(t, u, MfgWaitAPIHandler, map[string]any{"items": []any{pick("000972", 0, "A-1"), pick("000972", 1, "A-2")}}); code != 200 {
		t.Fatalf("製造待ちへ入れられません: %d %v", code, out)
	}
	if w := waitsOf(t, 972); w["A-1"] != 100 || w["A-2"] != 5 {
		t.Fatalf("製造待ちの数が違います: %v", w)
	}
	if code, _ := postRFQ(t, u, MfgWaitAPIHandler, map[string]any{"items": []any{pick("000972", 0, "A-1")}}); code != 409 {
		t.Errorf("⚠ もう全部入った行を、また入れられます: %d", code)
	}
	view := backlogViewHTML(u, 971)
	if !strings.Contains(view, `class="backlog-in-mfg"`) || !strings.Contains(view, MfgWaitLabel+" 100") {
		t.Errorf("⚠ 案件残表で、製造に入った行が打ち消し線になっていないか、「進み」に製造待ちが出ていません:\n%s", view)
	}
	if strings.Contains(view, `class="backlog-wait-pick"`+` title="製造へ入れる（まだ製造に入っていない数 100）"`) {
		t.Errorf("⚠ 全部入った行にまだ製造へのチェックがあります")
	}
	if !strings.Contains(view, `まだ製造に入っていない数 10）`) {
		t.Errorf("⚠ まだ入れていない行（B-1）のチェックがありません:\n%s", view)
	}
	wait := mfgWaitViewHTML(u, 0)
	if !strings.Contains(wait, "あけぼの精工") || !strings.Contains(wait, "ブラケット") || strings.Contains(wait, "シャフト") {
		t.Errorf("⚠ 製造待ちのビューが違います:\n%s", wait)
	}

	// 開始は1社。製造待ちでない行は開始できない。
	if code, _ := postRFQ(t, u, MfgStartAPIHandler, map[string]any{"items": []any{pick("000973", 0, "B-1")}}); code != 409 {
		t.Errorf("⚠ 製造待ちでない行を開始できます: %d", code)
	}
	postRFQ(t, u, MfgWaitAPIHandler, map[string]any{"items": []any{pick("000973", 0, "B-1")}})
	if code, _ := postRFQ(t, u, MfgStartAPIHandler, map[string]any{"items": []any{pick("000972", 0, "A-1"), pick("000973", 0, "B-1")}}); code != 400 {
		t.Errorf("⚠ 客先の違う行を1つの製造にできます: %d", code)
	}

	// ▶ 開始。
	code, out := postRFQ(t, u, MfgStartAPIHandler, map[string]any{"items": []any{pick("000972", 0, "A-1"), pick("000972", 1, "A-2")}})
	if code != 200 {
		t.Fatalf("開始できません: %d %v", code, out)
	}
	mfg := out["page_id"].(string)
	if body := readPageBody(t, mfg); !strings.Contains(body, "<td>A-1</td><td>ブラケット</td><td></td><td>100</td>") || !strings.Contains(body, "<td>A-2</td>") {
		t.Errorf("⚠ 製造明細表に製造待ちの数が入っていません:\n%s", body)
	}
	if w := waitsOf(t, 972); w["A-1"] != 0 || w["A-2"] != 0 {
		t.Errorf("⚠ 開始したのに製造待ちが残っています: %v", w)
	}
	if view := backlogViewHTML(u, 971); !strings.Contains(view, `href="/`+mfg+`">製造中</a>`) {
		t.Errorf("⚠ 案件残表の「進み」に製造中が出ていません:\n%s", view)
	}

	// 製造ページから1行（A-2）を製造待ちへ戻す。
	if code, out := postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 1, "item": "A-2"}); code != 200 {
		t.Fatalf("製造待ちへ戻せません: %d %v", code, out)
	}
	if body := readPageBody(t, mfg); strings.Contains(body, "<td>A-2</td>") || !strings.Contains(body, "<td>A-1</td>") {
		t.Errorf("⚠ 製造明細表から A-2 だけが外れていません:\n%s", body)
	}
	if w := waitsOf(t, 972); w["A-2"] != 5 {
		t.Errorf("⚠ 戻した数が製造待ちに入っていません: %v", w)
	}

	// 納めた分がある行は戻せない。
	if code, out := postRFQ(t, u, DeliveryNewAPIHandler, map[string]any{"mfg": mfg, "items": []any{map[string]any{"order": "000972", "row": -1, "item": "A-1", "name": "ブラケット", "qty": "15"}}}); code != 200 {
		t.Fatalf("製造ページから納品書を作れません: %d %v", code, out)
	}
	if code, _ := postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item": "A-1"}); code != 409 {
		t.Errorf("⚠ この製造で納めた分がある行を戻せます: %d", code)
	}

	// ↩ 案件へ戻す。
	if code, out := postRFQ(t, u, MfgUnwaitAPIHandler, map[string]any{"items": []any{pick("000972", 1, "A-2")}}); code != 200 {
		t.Fatalf("案件へ戻せません: %d %v", code, out)
	}
	if w := waitsOf(t, 972); w["A-2"] != 0 {
		t.Errorf("⚠ 案件へ戻したのに製造待ちが残っています: %v", w)
	}
	if view := backlogViewHTML(u, 971); !strings.Contains(view, `まだ製造に入っていない数 5）`) {
		t.Errorf("⚠ 案件へ戻した行（A-2）を、また選べません:\n%s", view)
	}
}
