package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 整理で新しく置いた加工製品ページに、弊社品番と取引先の決まり（品番＝図面番号・品名＝図面名称）を書く（2026-10-01・
// partner_rules.go）。利用者:「整理ボタンを押してプーリーを追加したのですが、品名や弊社品番のタグが出来ず、品番に図面番号も
// 入りません」。

// TestFilingWritesPartnerRules は、決まりのある取引先では弊社品番・品番・品名が入り、決まりの無い取引先では弊社品番だけが入り、
// 人が書いた品番は上書きしないことを固定します。
func TestFilingWritesPartnerRules(t *testing.T) {
	const inbox = "000059"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	boxID, err := EnsureCustomerBox(u)
	if err != nil {
		t.Fatalf("取引先の箱を作れません: %v", err)
	}
	if _, err := cms.CreateChildPage(boxID, "alice", `<h1>南北スポーツ</h1><dl data-type="tags">`+
		`<dt>`+PartNoRuleTag+`</dt><dd>図面番号</dd><dt>`+ItemNameRuleTag+`</dt><dd>図面名称</dd></dl>`); err != nil {
		t.Fatalf("取引先のページを作れません: %v", err)
	}

	// 決まりのある取引先。
	ruled := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	if r := postFiling(t, u, []filingRequest{secondRow(ruled, "取付ベース", "new")}); len(r) != 1 || r[0].Outcome != "moved" {
		t.Fatalf("整理できません: %+v", r)
	}
	body := readPageBody(t, ruled)
	for _, want := range []string{
		"<dt>" + OurItemNoTag + "</dt><dd>" + ruled + "</dd>",
		"<dt>" + ItemNoTag + "</dt><dd>K120-1</dd>",
		"<dt>" + ItemNameTag + "</dt><dd>取付ベース</dd>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("決まりのある取引先の加工製品に %q がありません:\n%s", want, body)
		}
	}
	// 書くのは題の下（ページのタグ）——図面ブロックの中ではない。
	if at, sec := strings.Index(body, "<dt>"+ItemNameTag+"</dt>"), strings.Index(body, "<section"); at < 0 || sec < at {
		t.Errorf("品名が題の下のタグに入っていません:\n%s", body)
	}

	// 決まりの無い取引先——弊社品番だけ。
	plain := makeDrawingPageFrom(t, inbox, "pdf002", "M300-7", "カバー", "汎用機", "みなと商店")
	row := filingRequest{PageID: plain, Customer: "みなと商店", MachineName: "汎用機", DrawingName: "カバー", Merge: "new"}
	if r := postFiling(t, u, []filingRequest{row}); len(r) != 1 || r[0].Outcome != "moved" {
		t.Fatalf("整理できません: %+v", r)
	}
	body = readPageBody(t, plain)
	if !strings.Contains(body, "<dt>"+OurItemNoTag+"</dt><dd>"+plain+"</dd>") {
		t.Errorf("弊社品番がありません:\n%s", body)
	}
	if strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>M300-7</dd>") || strings.Contains(body, "<dt>"+ItemNameTag+"</dt>") {
		t.Errorf("決まりの無い取引先に品番・品名を書きました:\n%s", body)
	}

	// 人が書いた品番は上書きしない。
	hand := makeDrawingPageFrom(t, inbox, "pdf003", "K120-9", "取付ベース", "標準2輪", "南北スポーツ")
	if err := cms.RewriteBody(hand, "alice", func(cur string) string {
		return strings.Replace(cur, "<dt>品番</dt><dd><br/></dd>", "<dt>品番</dt><dd>X-9</dd>", 1)
	}); err != nil {
		t.Fatalf("品番を書けません: %v", err)
	}
	if b := readPageBody(t, hand); !strings.Contains(b, "<dd>X-9</dd>") {
		t.Fatalf("試験の下ごしらえ（人の品番）が入っていません:\n%s", b)
	}
	if r := postFiling(t, u, []filingRequest{secondRow(hand, "取付ベース", "new")}); len(r) != 1 || r[0].Outcome != "moved" {
		t.Fatalf("整理できません: %+v", r)
	}
	body = readPageBody(t, hand)
	if !strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>X-9</dd>") || strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>K120-9</dd>") {
		t.Errorf("人が書いた品番を上書きしたか、二重にしました:\n%s", body)
	}
}

// TestPartnerRuleTellsUnknownValue は、分からない決まりの値を書かずに知らせることを固定します。
func TestPartnerRuleTellsUnknownValue(t *testing.T) {
	const inbox = "000060"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	boxID, _ := EnsureCustomerBox(u)
	if _, err := cms.CreateChildPage(boxID, "alice", `<h1>南北スポーツ</h1><dl data-type="tags">`+
		`<dt>`+PartNoRuleTag+`</dt><dd>客先品番</dd></dl>`); err != nil {
		t.Fatalf("取引先のページを作れません: %v", err)
	}
	id := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	r := postFiling(t, u, []filingRequest{secondRow(id, "取付ベース", "new")})
	if len(r) != 1 || r[0].Outcome != "moved" || !strings.Contains(r[0].Message, "品番の決め方：客先品番") {
		t.Fatalf("分からない決まりを知らせていません: %+v", r)
	}
	if body := readPageBody(t, id); strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>K120-1</dd>") {
		t.Errorf("分からない決まりで品番を書きました:\n%s", body)
	}
}

// TestFilingCheckboxesOverridePartnerRule は、整理の画面の印（「図面番号を品番に」「図面名称を品名に」）が取引先の決まりより
// 先に効くことを固定します（2026-10-01 利用者:「図面名称を品名、図面番号を品番にするチェックボックスがあっても良いのかもしれません」）。
func TestFilingCheckboxesOverridePartnerRule(t *testing.T) {
	const inbox = "000061"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	boxID, _ := EnsureCustomerBox(u)
	if _, err := cms.CreateChildPage(boxID, "alice", `<h1>南北スポーツ</h1><dl data-type="tags">`+
		`<dt>`+PartNoRuleTag+`</dt><dd>図面番号</dd><dt>`+ItemNameRuleTag+`</dt><dd>図面名称</dd></dl>`); err != nil {
		t.Fatalf("取引先のページを作れません: %v", err)
	}
	yes, no := true, false

	// 決まりのある取引先で、印を外した——弊社品番だけ。
	off := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	row := secondRow(off, "取付ベース", "new")
	row.PartNoFromDrawing, row.ItemNameFromDrawing = &no, &no
	postFiling(t, u, []filingRequest{row})
	body := readPageBody(t, off)
	if !strings.Contains(body, "<dt>"+OurItemNoTag+"</dt><dd>"+off+"</dd>") ||
		strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>K120-1</dd>") || strings.Contains(body, "<dt>"+ItemNameTag+"</dt>") {
		t.Errorf("印を外したのに品番・品名を書いたか、弊社品番がありません:\n%s", body)
	}

	// 決まりの無い取引先で、印を付けた——品番・品名も書く。
	on := makeDrawingPageFrom(t, inbox, "pdf002", "M300-7", "カバー", "汎用機", "みなと商店")
	row = filingRequest{PageID: on, Customer: "みなと商店", MachineName: "汎用機", DrawingName: "カバー", Merge: "new",
		PartNoFromDrawing: &yes, ItemNameFromDrawing: &yes}
	postFiling(t, u, []filingRequest{row})
	body = readPageBody(t, on)
	if !strings.Contains(body, "<dt>"+ItemNoTag+"</dt><dd>M300-7</dd>") || !strings.Contains(body, "<dt>"+ItemNameTag+"</dt><dd>カバー</dd>") {
		t.Errorf("印を付けたのに品番・品名がありません:\n%s", body)
	}
}
