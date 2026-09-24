package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// 発注明細の行を「必要部材表」へ戻す（2026-09-24）。
// ユーザー:「ボタンを押すと、発注明細表から行が消え、必要部材表へ戻ります」
// 「発注書の送付後でも押せます」。

// returnTestBody は発注明細が3行ある発注書ページです。
func returnTestBody() string {
	return `<h1>発注 みなと商店</h1>` +
		`<dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>みなと商店</dd></dl>` +
		`<table><caption>発注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>発注済</td></tr>` +
		`<tr><td>000031</td><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>3</td><td>未発注</td></tr>` +
		`<tr><td>000031</td><td>鉄</td><td>PL</td><td>t9*100*150</td><td>2</td><td>取消</td></tr>` +
		`</tbody></table>`
}

// TestRemoveOrderRowTakesOnlyThatRow は、⚠ **指した1行だけが消える**ことを固定します。
//
// ⚠ **行の数え方を取り違えると、別の行が消えます**——見出し行を数えると1つずれて、
// **押した行ではなく次の行が発注書から消えます**（しかもエラーは出ません）。
func TestRemoveOrderRowTakesOnlyThatRow(t *testing.T) {
	got, ok := removeOrderRow(returnTestBody(), 2)
	if !ok {
		t.Fatal("外せていません")
	}
	if strings.Contains(got, "t4.5*75*1090") {
		t.Errorf("⚠ 2行目が残っています:\n%s", got)
	}
	for _, keep := range []string{"<th>弊社品番</th>", "t3.2*100*200", "t9*100*150", "<caption>発注明細</caption>"} {
		if !strings.Contains(got, keep) {
			t.Errorf("⚠ %s まで消えました:\n%s", keep, got)
		}
	}
	// 範囲の外・0行目は断る（本文は変えない）。
	for _, row := range []int{0, 4, -1} {
		if out, ok := removeOrderRow(returnTestBody(), row); ok || out != returnTestBody() {
			t.Errorf("行 %d を外せてしまいます", row)
		}
	}
}

// TestRemoveOrderRowKeepsTheTable は、⚠ **最後の1行を外しても表は残る**ことを固定します
// ——発注書ページは発注書そのもので、表ごと消えると行を足し直す場所が無くなります。
func TestRemoveOrderRowKeepsTheTable(t *testing.T) {
	body := returnTestBody()
	for i := 0; i < 3; i++ {
		var ok bool
		body, ok = removeOrderRow(body, 1)
		if !ok {
			t.Fatalf("%d回目で外せません:\n%s", i+1, body)
		}
	}
	if !strings.Contains(body, "<caption>発注明細</caption>") || !strings.Contains(body, "<th>弊社品番</th>") {
		t.Errorf("⚠ 表ごと消えています:\n%s", body)
	}
}

// TestReturnedRowAppearsInRequiredParts は、⚠ **戻した部材が必要部材表に現れる**
// ことを固定します——戻す先へは何も書かないので、**本当に現れるか**は計算まで
// 通して確かめるしかありません。
//
// ⚠ **取消と対にして見ます**。取消は「もう発注しない」なので戻らず
// （`TestCancelledOrderStaysConsumed`）、戻すは行が消えるので戻ります。
func TestReturnedRowAppearsInRequiredParts(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	viewer := &auth.User{Username: "root", IsAdmin: true}

	before, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("下ごしらえが違います（必要部材 %d 件）: %#v", len(before), before)
	}

	// 下ごしらえの発注書（32）の1行（板 t3.2 を6本）を戻します。
	body := `<h1>発注 みなと商店</h1>` +
		`<dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>みなと商店</dd></dl>` +
		`<table data-type="` + ourOrderItemsType + `"><tbody>` +
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単価</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>800</td><td>` +
		OrderLineSent + `</td></tr></tbody></table>`
	out, ok := removeOrderRow(body, 1)
	if !ok {
		t.Fatal("外せていません")
	}
	syncBody(t, 32, out)

	after, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	found := false
	for _, u := range after {
		if strings.Contains(u.Size, "t3.2") {
			found = true
			if u.Remaining != 6 {
				t.Errorf("残が %d です（6を期待）: %#v", u.Remaining, u)
			}
		}
	}
	if !found {
		t.Errorf("⚠ 戻した材料が必要部材表にありません: %#v", after)
	}
}

// TestReturnButtonIsOnEveryRow は、⚠ **どの段からも戻せる**こと・**いつも確かめる**
// こと・**編集モードでは出ない**ことを固定します。
func TestReturnButtonIsOnEveryRow(t *testing.T) {
	for _, st := range []string{OrderLineUnsent, "", OrderLineSent, OrderLineDelivered,
		OrderLineCancelled, orderLineLegacySent} {
		got := orderRowButtonsHTML("000041", 3, st)
		if !strings.Contains(got, "order-row-return") {
			t.Errorf("⚠ 状態 %q の行に「必要部材表へ戻す」がありません:\n%s", st, got)
			continue
		}
		if !strings.Contains(got, "view-only") {
			t.Errorf("⚠ 状態 %q の戻すボタンが編集モードでも出ます:\n%s", st, got)
		}
		if !strings.Contains(got, `data-order-row="3"`) {
			t.Errorf("⚠ 状態 %q の戻すボタンが行を指していません:\n%s", st, got)
		}
		// ⚠ 戻すボタンだけを切り出して確かめます（取消の確認と混ざらないように）。
		ret := got[strings.Index(got, `class="chip-btn order-row-return`):]
		if !strings.Contains(ret, "data-order-confirm=") {
			t.Errorf("⚠ 状態 %q の戻すボタンが確かめずに行を消します:\n%s", st, ret)
		}
		sent := st == OrderLineSent || st == OrderLineDelivered || st == orderLineLegacySent
		if asksResend := strings.Contains(ret, "再送"); asksResend != sent {
			t.Errorf("状態 %q の確認に「再送」が %v です（%v を期待）:\n%s", st, asksResend, sent, ret)
		}
	}
}
