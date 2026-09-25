package subcon

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 臨時部材表（2026-09-25）。要求:「加工製品ページに無い材料は、この表に手で書きます。
// 書いた行は、下の必要部材表に並びます…発注部材表へ入れると、この表から消えます。
// 弊社品番の無い行を…戻すと、この表へ戻ってきます。行が無いときも、空の表を表示して
// おきます」。

const tempBoxBody = `<h1>発注</h1><p>弊社が出す発注書の置き場です。</p>` +
	`<section data-type="unordered-items"></section>`

func tempLine(name, qty string) ourOrderLine {
	return ourOrderLine{ItemName: name, Quantity: qty, Unit: "個"}
}

// TestAddTempPartsCreatesTheTableAboveRequiredParts は、⚠ **表が無ければ必要部材表の
// 上に作る**こと・**品番と表面を落とさない**こと・**弊社品番は持たない**ことを固定します。
func TestAddTempPartsCreatesTheTableAboveRequiredParts(t *testing.T) {
	ln := ourOrderLine{ProductID: "000031", ItemID: "A100-B01-242", ItemName: "留めプレート",
		Color: "緑", Quantity: "20", Unit: "個", Cost: "160", Note: "装置A用"}
	got, ok := addTempParts(tempBoxBody, []ourOrderLine{ln})
	if !ok {
		t.Fatal("足せていません")
	}
	cap := strings.Index(got, "<caption>臨時部材表</caption>")
	marker := strings.Index(got, `data-type="unordered-items"`)
	if cap < 0 || marker < 0 || cap > marker {
		t.Fatalf("⚠ 臨時部材表が必要部材表の上にありません:\n%s", got)
	}
	parts := tempPartsOf(got)
	if len(parts) != 1 {
		t.Fatalf("行が %d です（1を期待）:\n%s", len(parts), got)
	}
	p := parts[0].Line
	if p.ItemID != "A100-B01-242" || p.Color != "緑" || p.Cost != "160" || p.Note != "装置A用" {
		t.Errorf("⚠ 品番・表面・単価・備考のどれかが落ちています: %#v", p)
	}
	if strings.Contains(got, "<th>弊社品番</th>") || strings.Contains(got, "<th>状態</th>") {
		t.Errorf("⚠ 臨時部材表に弊社品番か状態の列があります:\n%s", got)
	}
}

// TestTempPartsKeepsAnEmptyTable は、⚠ **行が無くても表を残す**ことを固定します
// （要求:「行が無いときも、空の表を表示しておきます」）。発注部材表は空になると
// 消えますが、こちらは消しません。⚠ 空の取っ掛かりの行は、実の行を足すと消えます。
func TestTempPartsKeepsAnEmptyTable(t *testing.T) {
	body, _ := addTempParts(tempBoxBody, []ourOrderLine{tempLine("ウエス", "1"), tempLine("軍手", "2")})
	body, n := removeTempPartRows(body, []int{1, 2})
	if n != 2 {
		t.Fatalf("消した行が %d です（2を期待）", n)
	}
	if !strings.Contains(body, "<caption>臨時部材表</caption>") {
		t.Fatalf("⚠ 全部消したら表ごと消えました:\n%s", body)
	}
	if got := tempPartsOf(body); len(got) != 0 {
		t.Errorf("空のはずが %d 行あります: %#v", len(got), got)
	}
	// 空の取っ掛かりの行は、実の行を足したら取り除かれる（2行にならない）。
	body, _ = addTempParts(body, []ourOrderLine{tempLine("ボルト", "4")})
	if strings.Count(body, "<tr>") != 2 { // 見出し＋実の1行
		t.Errorf("⚠ 空の行が残っています:\n%s", body)
	}
}

// TestRemoveTempPartRowsTakesOnlyThoseRows は、⚠ **指した行だけが消える**ことを
// 固定します——行番号がずれると、**選んでいない臨時部材が消えます**。
func TestRemoveTempPartRowsTakesOnlyThoseRows(t *testing.T) {
	body, _ := addTempParts(tempBoxBody, []ourOrderLine{
		tempLine("ウエス", "1"), tempLine("軍手", "2"), tempLine("ボルト", "4")})
	got, n := removeTempPartRows(body, []int{2})
	if n != 1 || strings.Contains(got, "軍手") {
		t.Fatalf("⚠ 2行目が消えていません（%d 行）:\n%s", n, got)
	}
	for _, keep := range []string{"ウエス", "ボルト"} {
		if !strings.Contains(got, keep) {
			t.Errorf("⚠ %s まで消えました:\n%s", keep, got)
		}
	}
}

// TestTempPartsAppearInRequiredParts は、⚠ **臨時部材が必要部材表に並ぶ**ことを、
// 計算まで通して固定します。⚠ **受注明細が1件も無くても出ること**まで見ます
// （受注の無い月に消耗品だけ買うことは普通にある）。
func TestTempPartsAppearInRequiredParts(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, PurchaseOrderBoxTitle, "root", "302", true)
	body, _ := addTempParts(tempBoxBody, []ourOrderLine{
		{ItemName: "ウエス", Quantity: "3", Unit: "袋", Cost: "500"},
		{Material: "SS400", Shape: "FB", Size: "t6*50*1000", Color: "生地"}, // 数量が空
	})
	writeBodyFile(t, 40, body)

	got, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("必要部材表の行が %d です（臨時部材の2行を期待）: %#v", len(got), got)
	}
	a, b := got[0], got[1]
	if a.Client != tempPartsClient || a.TempRow != 1 || a.TempPage != "000040" ||
		a.Remaining != 3 || a.Cost != 500 || a.Name != "ウエス" {
		t.Errorf("1行目が違います: %#v", a)
	}
	// ⚠ 数量が空なら 1。材料は3つ組で名乗り、表面も見える。
	if b.Remaining != 1 || b.Name != "SS400 FB t6*50*1000（生地）" || b.TempRow != 2 {
		t.Errorf("2行目が違います: %#v", b)
	}
	// 画面の行は、臨時部材表の何行目かを運ぶ（入れたら消すため）。
	html := unorderedViewHTML(&auth.User{Username: "root", IsAdmin: true}, 40)
	for _, want := range []string{`data-temp-page="000040"`, `data-temp-row="1"`, "臨時部材", `data-unit="袋"`} {
		if !strings.Contains(html, want) {
			t.Errorf("必要部材表に %q がありません:\n%s", want, html)
		}
	}
	if strings.Contains(html, `href="/000000"`) {
		t.Errorf("⚠ 臨時部材に加工製品ページのリンクが出ています:\n%s", html)
	}
}

// TestTempRowsOnPicksOnlyThatPage は、⚠ **発注部材表へ入れるとき、同じページの臨時部材
// だけをその場で消す**ことを固定します（別のページの分は別の保存で消す）。
func TestTempRowsOnPicksOnlyThatPage(t *testing.T) {
	lines := []ourOrderLine{
		{ProductID: "000031", ItemName: "計算の行"},
		{ItemName: "ウエス", TempPage: "000040", TempRow: 2},
		{ItemName: "軍手", TempPage: "40", TempRow: 3}, // 桁が足りなくても同じページ
		{ItemName: "よそ", TempPage: "000041", TempRow: 1},
	}
	got := tempRowsOn(lines, "000040")
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("同じページの臨時部材の行が %v です（[2 3] を期待）", got)
	}
	if else_ := tempRowsElsewhere(lines, "000040"); len(else_["000041"]) != 1 {
		t.Errorf("別のページの臨時部材が拾えていません: %v", else_)
	}
}

// TestReturnWithoutProductGoesToTempParts は、⚠ **弊社品番の無い行を戻すと臨時部材表へ
// 入る**ことを、発注フォルダの本文まで書いて固定します（2026-09-24 ユーザー報告:
// 「必要部材表へ戻すを押すと発注明細から消えますが、必要部材表へは表示されません」）。
//
// ⚠ **弊社品番のある行は臨時部材表へ入れません**——計算の側に戻るので、入れると2か所に出ます。
func TestReturnWithoutProductGoesToTempParts(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, PurchaseOrderBoxTitle, "root", "302", true)
	writeBodyFile(t, 40, tempBoxBody)
	user := &auth.User{Username: "root", IsAdmin: true}

	order := unsentPaper("ふじ鍍金", OrderLineSent) // 弊社品番の列が無い＝空
	_, ln, ok := takeOrderRow(order, 1)
	if !ok || !needsTempParts(ln) {
		t.Fatalf("⚠ 弊社品番の無い行が臨時部材表行きになりません: %#v", ln)
	}
	if err := returnToTempParts(user, ln); err != nil {
		t.Fatalf("臨時部材表へ戻せません: %v", err)
	}
	b, err := os.ReadFile(page.BodyPath("000040"))
	if err != nil {
		t.Fatalf("発注フォルダを読めません: %v", err)
	}
	if parts := tempPartsOf(string(b)); len(parts) != 1 || parts[0].Line.ItemName != "ボルト" {
		t.Errorf("⚠ 臨時部材表に戻っていません: %#v\n%s", parts, b)
	}

	if needsTempParts(ourOrderLine{ProductID: "000031", ItemName: "計算の行"}) {
		t.Error("⚠ 弊社品番のある行まで臨時部材表へ入れようとしています（2か所に出ます）")
	}
	if needsTempParts(ourOrderLine{}) {
		t.Error("⚠ 空の行まで臨時部材表へ入れようとしています")
	}
}

// TestDraftReturnOnTheSamePageMovesToTempParts は、⚠ **発注部材表の「↩ 戻す」も、弊社品番の
// 無い行は臨時部材表へ入る**ことを固定します（同じページなので1回の書き換えで）。
func TestDraftReturnOnTheSamePageMovesToTempParts(t *testing.T) {
	body := tempBoxBody + orderDraftHTML([]ourOrderLine{
		{ItemName: "ウエス", Quantity: "3"},
		{ProductID: "000031", Material: "SS400", Shape: "板", Size: "t3.2", Quantity: "2"},
	})
	out, ln, ok := takeDraftRow(body, 1, 1)
	if !ok || !needsTempParts(ln) {
		t.Fatalf("⚠ 1行目（弊社品番なし）が臨時部材表行きになりません: %#v", ln)
	}
	out, _ = addTempParts(out, []ourOrderLine{ln})
	if parts := tempPartsOf(out); len(parts) != 1 || parts[0].Line.ItemName != "ウエス" {
		t.Errorf("⚠ 臨時部材表に入っていません:\n%s", out)
	}
	if !strings.Contains(out, "t3.2") {
		t.Errorf("⚠ 残るはずの発注部材表の行まで消えています:\n%s", out)
	}
}

// TestOrderCarriesItemIDAndSurface は、⚠ **品番と表面が発注部材表・発注書まで運ばれる**
// ことを固定します——2026-09-25 まで運んでおらず、**塗装・鍍金の発注書で表面が
// 落ちていました**。
func TestOrderCarriesItemIDAndSurface(t *testing.T) {
	ln := ourOrderLine{ItemID: "A100-B01-242", ItemName: "留めプレート", Color: "緑", Quantity: "20"}
	for name, got := range map[string]string{
		"発注部材表": orderDraftHTML([]ourOrderLine{ln}),
		"発注書":   buildOurOrderHTML("000150", "ふじ鍍金", "2026-09-25", "", "", "", []ourOrderLine{ln}),
	} {
		for _, want := range []string{"<td>A100-B01-242</td>", "<td>緑</td>"} {
			if !strings.Contains(got, want) {
				t.Errorf("⚠ %s に %s がありません:\n%s", name, want, got)
			}
		}
	}
}
