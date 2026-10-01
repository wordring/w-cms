package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// 手配の帳簿（2026-10-01・procure_ledger.go）——手当てを**受注ごとに**数える・余りは在庫・手配不要。
//
// 下ごしらえ: 加工製品 41（材料 板 t3.2 を1台に2枚）・受注 40（2台・納期 10-10）と 42（3台・納期 10-20）。
// 板の必要は 受注40 が 4枚、受注42 が 6枚。

func seedLedger(t *testing.T) {
	t.Helper()
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 41, 0, "カバー", "root", "302", true)
	addPage(t, 40, 0, "受注A", "root", "302", true)
	addPage(t, 42, 0, "受注B", "root", "302", true)
	syncBody(t, 41, `<h1>カバー</h1>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>2</td></tr>`+
		`</tbody></table>`)
	setOrder(t, 40, "2", "", "2026-10-10")
	setOrder(t, 42, "3", "", "2026-10-20")
}

// setOrder は受注ページ id の明細（加工製品 41 を qty 台・出荷済み shipped・納期 due）を書きます。
func setOrder(t *testing.T, id int, qty, shipped, due string) {
	t.Helper()
	syncBody(t, id, `<h1>受注</h1>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>出荷済み</th><th>納期</th></tr>`+
		`<tr><td>000041</td><td>K-9</td><td>カバー</td><td>`+qty+`</td><td>`+shipped+`</td><td>`+due+`</td></tr>`+
		`</tbody></table>`)
}

// setPurchase は発注書ページ id に、板を qty 枚（受注 forOrder のため・空なら書かない）の行を書きます。
func setPurchase(t *testing.T, id int, qty, forOrder string) {
	t.Helper()
	addPage(t, id, 0, "発注 わかば鋼業", "root", "302", true)
	syncBody(t, id, `<h1>発注 わかば鋼業</h1>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>受注</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th></tr>`+
		`<tr><td>000041</td><td>`+forOrder+`</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>`+qty+`</td></tr>`+
		`</tbody></table>`)
}

// remainingByOrder は必要部材表の残を受注ページごとに返します。
func remainingByOrder(t *testing.T) map[int]int {
	t.Helper()
	list, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]int{}
	for _, u := range list {
		out[u.OrderPageID] += u.Remaining
	}
	return out
}

// TestLedgerCountsPerOrder は、受注の書かれた発注をその受注にだけ当てることを固定します（それまでは加工製品ごとの
// 通算を両方の受注から引き、受注Aも埋まって見えた）。⚠ 当てる先は納期の遅い受注B——納期の順に当てても通らないように。
func TestLedgerCountsPerOrder(t *testing.T) {
	seedLedger(t)
	setPurchase(t, 50, "6", "000042")
	got := remainingByOrder(t)
	if got[40] != 4 || got[42] != 0 {
		t.Errorf("受注Bのための6枚は受注Bにだけ当たるはず: %v（A 4・B 0）", got)
	}
}

// TestLedgerSharesLegacyOnce は、受注の書かれていない発注（それまでの行）を、納期の早い受注から1回だけ当てることを
// 固定します（二重に当てない）。
func TestLedgerSharesLegacyOnce(t *testing.T) {
	seedLedger(t)
	setPurchase(t, 50, "6", "")
	got := remainingByOrder(t)
	if got[40] != 0 || got[42] != 4 {
		t.Errorf("受注の無い6枚は納期の早いAに4枚・Bに2枚のはず: %v（A 0・B 4）", got)
	}
}

// TestLedgerSurplusBecomesStock は、閉じた受注のために多く買った余りが在庫として回ることを固定します（2026-10-01
// 利用者:「必要数より購入数のほうが多ければ、在庫になります」）。
func TestLedgerSurplusBecomesStock(t *testing.T) {
	seedLedger(t)
	setOrder(t, 40, "2", "2", "2026-10-10") // 受注A は出し終えた（必要4枚）
	setPurchase(t, 50, "7", "000040")       // 受注Aのために7枚買った → 余り3枚
	got := remainingByOrder(t)
	if _, ok := got[40]; ok {
		t.Errorf("出し終えた受注Aが必要部材表に並んでいます: %v", got)
	}
	if got[42] != 3 {
		t.Errorf("受注Bは6枚のうち余りの3枚が在庫から回り、残3のはず: %v", got)
	}
	list, err := ProcurementByProduct(&auth.User{Username: "root", IsAdmin: true}, 42)
	if err != nil || len(list) != 1 || len(list[0].Items) != 1 {
		t.Fatalf("手配状況が引けません: %v %#v", err, list)
	}
	it := list[0].Items[0]
	if it.Ordered != 3 || it.Remaining != 3 || len(it.Orders) != 1 || !it.Orders[0].Stock {
		t.Errorf("受注Bの手配状況に在庫の3枚（在庫の印）が出るはず: %#v", it)
	}
}

// TestSkipRemovesFromRequiredList は「不要にする」が受注ごとに引き、「↩ 戻す」で戻ることを固定します（2026-10-01
// 利用者:「必要なくなった時に消すのはどうしましょう？」）。
func TestSkipRemovesFromRequiredList(t *testing.T) {
	seedLedger(t)
	addPage(t, 60, 0, "発注", "root", "302", true)
	body := `<h1>発注</h1><section data-type="` + UnorderedViewType + `"></section>`
	out, n := addSkipRows(body, []ourOrderLine{{ProductID: "000041", ForOrder: "000042", Kind: "材料",
		Material: "鉄", Shape: "板", Size: "t3.2*100*200", Quantity: "6"}}, "在庫あり", "2026-10-01")
	if n != 1 || !strings.Contains(out, "<caption>手配不要</caption>") || !strings.Contains(out, "<td>在庫あり</td>") {
		t.Fatalf("手配不要の表に入っていません:\n%s", out)
	}
	if strings.Index(out, "<caption>手配不要</caption>") < strings.Index(out, UnorderedViewType) {
		t.Errorf("手配不要の表は必要部材表の下のはず:\n%s", out)
	}
	syncBody(t, 60, out)
	got := remainingByOrder(t)
	if got[40] != 4 || got[42] != 0 {
		t.Errorf("受注Bの6枚だけが不要になるはず: %v（A 4・B 0）", got)
	}
	back, ln, ok := takeSkipRow(out, 1)
	if !ok || ln.ForOrder != "000042" || strings.Contains(back, "手配不要") {
		t.Fatalf("戻すと行（と空になった表）が消えるはず: %v %#v\n%s", ok, ln, back)
	}
	syncBody(t, 60, back)
	if got := remainingByOrder(t); got[42] != 6 {
		t.Errorf("戻すと受注Bの6枚が必要部材表へ戻るはず: %v", got)
	}
}

// TestAppendToDraftFollowsHeader は、列を足す前に作った発注部材表へ足しても、見出しと値がずれないことを固定します。
func TestAppendToDraftFollowsHeader(t *testing.T) {
	old := `<section><table><caption>発注部材表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>材質</th><th>数量</th></tr>` +
		`<tr><td>000041</td><td>鉄</td><td>2</td></tr></tbody></table></section>`
	got, n, ok := appendToDraft(old, "1", []ourOrderLine{{ProductID: "000041", ForOrder: "000042", Material: "SUS", Quantity: "5"}})
	if !ok || n != 1 || !strings.Contains(got, "<tr><td>000041</td><td>SUS</td><td>5</td></tr>") {
		t.Errorf("見出しに合わせて足していません:\n%s", got)
	}
}
