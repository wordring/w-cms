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
	// ⚠ **状態は書きません**——まだ発注していないのに `未納品` と書くと、
	//    **もう注文したように見えます**（紙のほうには書きます）。
	if strings.Contains(draft, "<td>未納品</td>") {
		t.Errorf("⚠ 発注部材表に「未納品」が入っています（まだ発注していません）:\n%s", draft)
	}
	if !strings.Contains(paper, "<td>未納品</td>") {
		t.Errorf("⚠ 紙のほうに「未納品」がありません:\n%s", paper)
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
