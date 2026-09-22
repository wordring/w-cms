package subcon

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 発注書を送る・発注済みの印（2026-09-22）
//
// ユーザー:「**発注書の表の一品ずつに発注済みの印**をつけます。…**メールの送信など
// 自明な時は、自動で印をつければ良い**し、発注書を印刷して手渡しするような場合は、
// **人間が発注済みの印をつける**と思います。大事なことは、**発注の取り消しもある**
// ということです」。

// orderPaper は発注明細1枚ぶんの本文を組みます（行は `状態` つき）。
func orderPaper(statuses ...string) string {
	var b strings.Builder
	b.WriteString(`<h1>発注 みなと商店</h1>`)
	b.WriteString(`<table data-type="` + ourOrderItemsType + `"><tbody>`)
	b.WriteString(`<tr><th>弊社品番</th><th>寸法</th><th>数量</th><th>状態</th></tr>`)
	for i, st := range statuses {
		b.WriteString(`<tr><td>000031</td><td>t` + string(rune('1'+i)) +
			`</td><td>2</td><td>` + st + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// TestSetOrderLineStatusOneRow は、⚠ **1行だけ**書き換わることを固定します。
func TestSetOrderLineStatusOneRow(t *testing.T) {
	body := orderPaper(OrderLineUnsent, OrderLineUnsent)
	out, n := setOrderLineStatus(body, 2, OrderLineCancelled)
	if n != 1 {
		t.Fatalf("書き換えた行が %d です（1を期待）:\n%s", n, out)
	}
	if strings.Count(out, "<td>"+OrderLineUnsent+"</td>") != 1 {
		t.Errorf("⚠ 1行目まで巻き込んでいます:\n%s", out)
	}
	if !strings.Contains(out, "<td>"+OrderLineCancelled+"</td>") {
		t.Errorf("2行目が取消になっていません:\n%s", out)
	}
}

// TestSetOrderLineStatusAllSkipsCancelled は、⚠ **まとめ書きが取消を起こさない**ことを
// 固定します。
//
// ⚠ **これは実害のある取り違えです。** 巻き込むと、**取り消したはずの材料が
// 「手配済み」に戻り**、未手配の一覧から静かに消えます——そして誰も買いません。
func TestSetOrderLineStatusAllSkipsCancelled(t *testing.T) {
	body := orderPaper(OrderLineUnsent, OrderLineCancelled, OrderLineUnsent)
	out, n := setOrderLineStatus(body, 0, OrderLineSent)
	if n != 2 {
		t.Fatalf("書き換えた行が %d です（2を期待）:\n%s", n, out)
	}
	if !strings.Contains(out, "<td>"+OrderLineCancelled+"</td>") {
		t.Errorf("⚠ 取消の行まで発注済みにしています:\n%s", out)
	}
	if strings.Count(out, "<td>"+OrderLineSent+"</td>") != 2 {
		t.Errorf("発注済みが2行になっていません:\n%s", out)
	}
}

// TestSetOrderLineStatusSaysNothingChanged は、⚠ **同じ値なら書かない**ことを
// 固定します（版と更新日時を無駄に進めないため）。
func TestSetOrderLineStatusSaysNothingChanged(t *testing.T) {
	body := orderPaper(OrderLineSent)
	out, n := setOrderLineStatus(body, 0, OrderLineSent)
	if n != 0 || out != body {
		t.Errorf("同じ値で書き換えています: n=%d\n%s", n, out)
	}
}

// TestCancelledOrderReturnsToUnordered は、⚠ **取り消したら未手配へ戻る**ことを
// 固定します（ユーザー:「大事なことは、**発注の取り消しもある**ということです」）。
//
// ⚠ **これがいちばん危ない取り違えです。** 取消を「手配した」に数えると、
// **その材料は未手配の一覧から消えたまま**になり、⚠ **誰も買わないまま納期が
// 来ます**——しかもエラーは出ません。
func TestCancelledOrderReturnsToUnordered(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	viewer := &auth.User{Username: "root", IsAdmin: true}

	// 下ごしらえの発注書（32）は 板 t3.2 を6本手配していて、必要数と同じです
	// ——だから一覧には FB t4.5 の1件しか出ません（`unordered_test.go` の番人）。
	before, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("下ごしらえが違います（未手配 %d 件）: %#v", len(before), before)
	}

	// その発注書を取り消します。
	syncBody(t, 32, `<h1>発注 みなと商店</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>みなと商店</dd>`+
		`<dt>`+OrderedAtTag+`</dt><dd>2026-09-21</dd></dl>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単価</th><th>状態</th></tr>`+
		`<tr><td>000031</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>800</td>`+
		`<td>`+OrderLineCancelled+`</td></tr>`+
		`</tbody></table>`)

	after, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("⚠ 取り消した材料が未手配に戻っていません（%d 件）: %#v", len(after), after)
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
		t.Errorf("⚠ 取り消した材料が一覧にありません: %#v", after)
	}
}

// TestUnsentPriceIsNotABoughtPrice は、⚠ **出していない紙の単価を「最新単価」に
// 出さない**ことを固定します。
//
// `未発注` は**こちらが書いただけ**で、仕入先はまだ何も承諾していません——
// それを相場として出すと、**自分で書いた希望額で見積もって受注**することになります。
func TestUnsentPriceIsNotABoughtPrice(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店", Status: OrderLineUnsent,
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	if strings.Contains(got, "800円") {
		t.Errorf("⚠ まだ出していない発注書の単価が「最新単価」に出ています:\n%s", got)
	}
	// ⚠ **黙りません。** 「引けなかった」と「そもそも鏡が走っていない」を
	//    見分けられる形で言います。
	if !strings.Contains(got, "買った記録がありません") {
		t.Errorf("⚠ 引けなかったことを黙っています:\n%s", got)
	}
}

// TestOrderSendState は、⚠ **合っているときも黙らない**ことを固定します。
//
// 黙ると「出した」と「そもそも数えていない」が見分けられません（検算と同じ規律）。
func TestOrderSendState(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    OrderSendCounts
		want string
	}{
		{"未発注", OrderSendCounts{Total: 3}, "まだ発注していません"},
		{"一部", OrderSendCounts{Total: 3, Sent: 1}, "2行がまだ発注済みになっていません"},
		{"全部", OrderSendCounts{Total: 3, Sent: 3}, "✓ 発注済み"},
		{"全部取消", OrderSendCounts{Cancelled: 2}, "全部取り消しました"},
		{"明細なし", OrderSendCounts{}, "明細がありません"},
	} {
		if got := orderSendStateHTML(tc.c); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q に %q がありません", tc.name, got, tc.want)
		}
	}
	// ⚠ **0行を「発注済み」と言わないこと**——沈黙を成功と読む形です。
	if (OrderSendCounts{}).AllSent() {
		t.Error("⚠ 明細の無い紙が「発注済み」になっています")
	}
}

// TestOrderLinkHasNoBakedState は、⚠ **「まだ発注していません」を本文に焼き込まない**
// ことを固定します。
//
// ⚠ **出したら消えなければならない文**なので、本文に書くと**発注書を出したあとも
// 古いまま**残ります——そして直す人は現れません。
func TestOrderLinkHasNoBakedState(t *testing.T) {
	got := orderLinkHTML("000145", "みなと商店", 5)
	if strings.Contains(got, "まだ発注していません") {
		t.Errorf("⚠ 進み具合が本文に焼き込まれています:\n%s", got)
	}
	for _, want := range []string{`data-ref="000145"`, "みなと商店", "5行", `href="/000145"`} {
		if !strings.Contains(got, want) {
			t.Errorf("本文に %q がありません:\n%s", want, got)
		}
	}
}

// TestOrderLinkMirrorReadsTheOrderPage は、⚠ **リンクが発注書ページから読み直す**
// ことを固定します。
//
// ⚠ **「⚠ が出ないこと」だけを見ません**——鏡が一度も走っていなくても通るためです。
// **出していないときは「まだ」、出したあとは「✓」**の**両方**を見ます。
func TestOrderLinkMirrorReadsTheOrderPage(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, "発注", "root", "302", true)
	addPage(t, 41, 0, "発注 みなと商店", "root", "302", true)

	body := orderLinkHTML("000041", "みなと商店", 2)
	viewer := &auth.User{Username: "root", IsAdmin: true}

	syncBody(t, 41, orderPaper(OrderLineUnsent, OrderLineUnsent))
	if got := showPage(t, viewer, 40, body); !strings.Contains(got, "まだ発注していません") {
		t.Fatalf("⚠ 出していないのに「まだ」が出ません（鏡が走っていない？）:\n%s", got)
	}

	syncBody(t, 41, orderPaper(OrderLineSent, OrderLineSent))
	got := showPage(t, viewer, 40, body)
	if strings.Contains(got, "まだ発注していません") {
		t.Errorf("⚠ 出したのに「まだ発注していません」が残っています:\n%s", got)
	}
	if !strings.Contains(got, "✓ 発注済み") {
		t.Errorf("⚠ 出したことを黙っています:\n%s", got)
	}
}

// TestOurOrderMirrorPutsTheSendForm は、発注明細の足元に送信欄が出ることと、
// ⚠ **それが本文に残らないクローム**であることを固定します。
//
// ⚠ **`vocab-chrome` が無いと、人が画面の表をコピーして貼ったとき本当の列として
// 保存されます**（`class` はサニタイズで落ちるので、貼られた時点では見分けが
// 付かなくなる）。
func TestOurOrderMirrorPutsTheSendForm(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 41, 0, "発注 みなと商店", "root", "302", true)

	body := orderPaper(OrderLineUnsent)
	syncBody(t, 41, body)
	got := showPage(t, &auth.User{Username: "root", IsAdmin: true}, 41, body)

	for _, want := range []string{
		"FAXで送った", "手渡した", "メールで送る",
		`data-order-send="1"`,                       // メールの送信ボタン
		`data-order-status="` + OrderLineSent + `"`, // 行ごとの「発注済」
		`class="vocab-chrome order-row-act"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 鏡に %q がありません:\n%s", want, got)
		}
	}
}

// showPage はページを鏡ごしに描きます。
func showPage(t *testing.T, viewer *auth.User, pageID int, body string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	if viewer != nil {
		req = auth.WithUser(req, viewer)
	}
	return cms.RenderComputedViews(req, pageID, body)
}
