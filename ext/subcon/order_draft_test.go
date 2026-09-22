package subcon

import (
	"strings"
	"testing"
)

// 発注部材表（2026-09-22）——未発注の表と発注書のあいだに置く、**人が足し引きする表**。
//
// ⚠ **人が足し引きした結果は「推測」ではなく「事実」**なので、本文に書いてよい、
// というのがこの表の根拠です。

// TestOrderDraftBuildsFromPickedLines は、選んだ行がそのまま表になることを固定します。
func TestOrderDraftBuildsFromPickedLines(t *testing.T) {
	got := orderDraftHTML([]ourOrderLine{
		{ProductID: "000080", Material: "鉄", Shape: "角パイプ", Size: "□75*75*t3.2*120",
			Quantity: "6", Cost: "400"},
	})
	for _, want := range []string{
		`data-type="` + OrderDraftType + `"`,
		"<caption>発注部材表</caption>",
		"<td>000080</td>", "<td>□75*75*t3.2*120</td>", "<td>400</td>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q がありません:\n%s", want, got)
		}
	}
	// ⚠ **見出しは宣言から**（手書きに戻すと、列を足した日に足した列が読めません）。
	for _, c := range columnsOf(OrderDraftType) {
		if !strings.Contains(got, "<th>"+c.Label+"</th>") {
			t.Errorf("宣言の列 %q が見出しにありません", c.Label)
		}
	}
}

// TestOrderDraftStartsEmpty は、⚠ **何も選ばなくても表を作れる**ことを固定します。
//
// ユーザー:「（**必要なければ何もクリックしなくても**）表作成ボタンをクリックすると
// 発注部材表が開き」——**加工製品ページに無い部材だけを買う道**がこれです。
func TestOrderDraftStartsEmpty(t *testing.T) {
	got := orderDraftHTML(nil)
	if !strings.Contains(got, "<caption>発注部材表</caption>") {
		t.Fatalf("空でも表の骨が出ていません:\n%s", got)
	}
	// ⚠ **書き足す取っ掛かりとして、空の行を1つ置くこと**——見出しだけの表は、
	//    エディタで「ここへ書く」が分かりません。
	rows := strings.Count(got, "<tr>")
	if rows != 2 { // 見出し行 ＋ 空の行
		t.Errorf("行が %d です（見出し＋空の1行＝2を期待）:\n%s", rows, got)
	}
}

// TestOrderDraftFollowsTheSameRulesAsThePaper は、⚠ **表と紙で値が食い違わない**ことを
// 固定します。
//
// ⚠ 人は表を見て「これでよい」と判断し、そのまま紙になると思っています。**違っていたら、
// どちらを信じてよいか分かりません。**
func TestOrderDraftFollowsTheSameRulesAsThePaper(t *testing.T) {
	// 材料の行（品名は焼き直しなので書かない・単価0は書かない）。
	ln := ourOrderLine{ProductID: "000080", Material: "鉄", Size: "t4.5",
		ItemName: "鉄 t4.5", Quantity: "2", Cost: "0"}
	draft := orderDraftHTML([]ourOrderLine{ln})
	paper := buildOurOrderHTML("000200", "みなと商店", "2026-09-22", "", "", "",
		[]ourOrderLine{ln})
	for _, bad := range []string{"鉄 t4.5</td>", "<td>0</td>"} {
		if strings.Contains(draft, bad) {
			t.Errorf("⚠ 発注部材表に %q が出ています（紙には出ません）:\n%s", bad, draft)
		}
		if strings.Contains(paper, bad) {
			t.Errorf("⚠ 紙に %q が出ています:\n%s", bad, paper)
		}
	}
	// ⚠ **状態は書きません**——まだ紙にもなっていないのに `未発注` と書くと、
	//    **もう発注書ができたように見えます**（紙のほうには書きます）。
	if strings.Contains(draft, "<td>"+OrderLineUnsent+"</td>") {
		t.Errorf("⚠ 発注部材表に「状態」が入っています（まだ紙にもなっていません）:\n%s", draft)
	}
	if !strings.Contains(paper, "<td>"+OrderLineUnsent+"</td>") {
		t.Errorf("⚠ 紙のほうに「未発注」がありません:\n%s", paper)
	}
}

// TestOrderDraftIsFoundByCaptionToo は、⚠ **キャプションで名乗った表も見つかる**ことを
// 固定します。
//
// ⚠ **文字列で探すと見落とします**——表は `data-type` でも `<caption>` でも名乗れる
// （2026-09-20）ので、人が手で作った表（属性なし）が2枚目として通ってしまいます。
func TestOrderDraftIsFoundByCaptionToo(t *testing.T) {
	withAttr := `<table data-type="` + OrderDraftType + `"><tbody><tr><th>弊社品番</th></tr></tbody></table>`
	if !isTableOfTypeInBody(withAttr, OrderDraftType) {
		t.Error("属性で名乗った表が見つかりません")
	}
	byCaption := `<table><caption>発注部材表</caption><tbody><tr><th>弊社品番</th></tr></tbody></table>`
	if !isTableOfTypeInBody(byCaption, OrderDraftType) {
		t.Error("⚠ キャプションで名乗った表が見つかりません（2枚目が通ってしまいます）")
	}
	if isTableOfTypeInBody(`<p>ただの本文</p>`, OrderDraftType) {
		t.Error("無いのに在ると答えています")
	}
}

// TestAppendToDraftAddsToTheRightTable は、⚠ **足す先を取り違えない**ことを
// 固定します（2026-09-22）。
//
// ⚠ **発注書は1枚に1社**なので、別の表へ入れると**別の業者の行が1つの紙に混ざります**
// ——しかも**紙にしてから気づきます**。
func TestAppendToDraftAddsToTheRightTable(t *testing.T) {
	body := orderDraftHTML([]ourOrderLine{{ItemName: "1枚目の行"}}) +
		orderDraftHTML([]ourOrderLine{{ItemName: "2枚目の行"}})
	got, n, ok := appendToDraft(body, "2", []ourOrderLine{{ItemName: "足した行"}})
	if !ok || n != 1 {
		t.Fatalf("足せていません: ok=%v n=%d", ok, n)
	}
	// 2枚目の表に入っていること（1枚目には入らない）。
	i1 := strings.Index(got, "1枚目の行")
	i2 := strings.Index(got, "2枚目の行")
	ia := strings.Index(got, "足した行")
	if !(i1 < i2 && i2 < ia) {
		t.Errorf("⚠ 足す先が違います（1枚目に入っていませんか）:\n%s", got)
	}
	// 無い枚数は断る（黙って1枚目に入れない）。
	if _, _, ok := appendToDraft(body, "9", []ourOrderLine{{ItemName: "x"}}); ok {
		t.Error("⚠ 無い枚数なのに足しています")
	}
	if _, _, ok := appendToDraft(body, "0", []ourOrderLine{{ItemName: "x"}}); ok {
		t.Error("⚠ 0枚目に足しています")
	}
}

// TestReplaceDraftTableLeavesTheOthers は、⚠ **発注書にした表だけが消える**ことを
// 固定します。
//
// ⚠ **他の表まで消すと、同時に進めていた別の業者ぶんの作業が丸ごと失われます。**
func TestReplaceDraftTableLeavesTheOthers(t *testing.T) {
	body := `<h1>発注</h1>` +
		orderDraftHTML([]ourOrderLine{{ItemName: "1枚目の行"}}) +
		orderDraftHTML([]ourOrderLine{{ItemName: "2枚目の行"}})
	got, ok := replaceDraftTable(body, 1, `<p>📄 発注書 000143</p>`)
	if !ok {
		t.Fatal("置き換えられていません")
	}
	if strings.Contains(got, "1枚目の行") {
		t.Errorf("⚠ 1枚目が残っています:\n%s", got)
	}
	if !strings.Contains(got, "2枚目の行") {
		t.Errorf("⚠ 2枚目まで消えました（別の業者ぶんの作業が失われます）:\n%s", got)
	}
	if !strings.Contains(got, "発注書 000143") {
		t.Errorf("リンクが入っていません:\n%s", got)
	}
	// ⚠ **見出しは残ること**（表だけを置き換える）。
	if !strings.Contains(got, "<h1>発注</h1>") {
		t.Errorf("⚠ ページの見出しまで消えました:\n%s", got)
	}
	// 無い枚数は何もしない。
	if _, ok := replaceDraftTable(body, 9, `<p>x</p>`); ok {
		t.Error("⚠ 無い枚数なのに置き換えています")
	}
}

// TestRemoveDraftRowTakesTheRightRow は、⚠ **外す行を取り違えない**ことを
// 固定します（2026-09-22）。
//
// ユーザー:「**発注部材表から未手配の一覧へ戻す方法がありません**」——
// ⚠ **取り違えると、戻したかった行は残り、買うつもりの行が消えます**。
func TestRemoveDraftRowTakesTheRightRow(t *testing.T) {
	body := orderDraftHTML([]ourOrderLine{
		{ItemName: "1行目"}, {ItemName: "2行目"}, {ItemName: "3行目"},
	})
	got, ok := removeDraftRow(body, 1, 2)
	if !ok {
		t.Fatal("外せていません")
	}
	if strings.Contains(got, "2行目") {
		t.Errorf("⚠ 2行目が残っています:\n%s", got)
	}
	for _, keep := range []string{"1行目", "3行目"} {
		if !strings.Contains(got, keep) {
			t.Errorf("⚠ %s まで消えました:\n%s", keep, got)
		}
	}
	// ⚠ **見出し行は数えません**（人が見ている「何行目」と揃える）。
	if !strings.Contains(got, "<th>弊社品番</th>") {
		t.Errorf("⚠ 見出し行を外しています:\n%s", got)
	}
	// 無い行・無い表は何もしない。
	if _, ok := removeDraftRow(body, 1, 9); ok {
		t.Error("⚠ 無い行を外したと答えています")
	}
	if _, ok := removeDraftRow(body, 2, 1); ok {
		t.Error("⚠ 無い表から外したと答えています")
	}
	if _, ok := removeDraftRow(body, 1, 0); ok {
		t.Error("⚠ 0行目（見出し）を外しています")
	}
}

// TestRemoveDraftRowDropsTheEmptyTable は、⚠ **最後の1行を外したら表ごと消える**ことを
// 固定します（2026-09-22）。
//
// ユーザー:「**戻しても部材表から消えません**」——⚠ **見出しだけの表が残ると、
// 発注ページに空の表が溜まり**、しかも「何枚目へ足すか」の選択肢に並ぶので
// **押し間違いの元**になります。
func TestRemoveDraftRowDropsTheEmptyTable(t *testing.T) {
	body := `<h1>発注</h1>` +
		orderDraftHTML([]ourOrderLine{{ItemName: "1枚目の唯一の行"}}) +
		orderDraftHTML([]ourOrderLine{{ItemName: "2枚目の行A"}, {ItemName: "2枚目の行B"}})

	// 1枚目の唯一の行を外す → **表ごと消える**。
	got, ok := removeDraftRow(body, 1, 1)
	if !ok {
		t.Fatal("外せていません")
	}
	if n := strings.Count(got, `data-type="`+OrderDraftType+`"`); n != 1 {
		t.Errorf("⚠ 表が %d 枚です（空になった1枚が消えて1枚のはず）:\n%s", n, got)
	}
	if strings.Contains(got, "1枚目の唯一の行") {
		t.Errorf("⚠ 行が残っています:\n%s", got)
	}
	// ⚠ **他の表は残ること**（同時に進めていた作業が失われないように）。
	for _, keep := range []string{"2枚目の行A", "2枚目の行B", "<h1>発注</h1>"} {
		if !strings.Contains(got, keep) {
			t.Errorf("⚠ %s まで消えました:\n%s", keep, got)
		}
	}

	// ⚠ **まだ行が残っているときは、表を消さないこと。**
	got2, ok := removeDraftRow(body, 2, 1)
	if !ok {
		t.Fatal("2枚目から外せていません")
	}
	if n := strings.Count(got2, `data-type="`+OrderDraftType+`"`); n != 2 {
		t.Errorf("⚠ まだ行が残っているのに表を消しました（%d枚）:\n%s", n, got2)
	}
	if !strings.Contains(got2, "2枚目の行B") {
		t.Errorf("⚠ 残るはずの行が消えました:\n%s", got2)
	}
}
