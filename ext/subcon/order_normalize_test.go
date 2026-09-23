package subcon

import (
	"strings"
	"testing"
)

// **弊社の表は正規形、原本の写しは読んだまま**（2026-09-21 ユーザー:「品名が半角カナに
// なっているので、正規化します。半角カナ以外も正規化します」）。
//
// ⚠ **この2つが同じ本文の中に並んでいるのが肝です。** 畳むと原本と食い違いますが、
// **その原本がすぐ上に出ている**ので見比べられます——だから畳めます。
// 逆に、原本の写しまで畳むと「読んだまま」が嘘になります。

// ourItemsSegment は本文を「原本まで」と「弊社の明細から」に割ります。
func ourItemsSegment(t *testing.T, body string) (source, ours string) {
	t.Helper()
	at := strings.Index(body, `<table><caption>`+displayNameOf(clientOrderItemsType)+`</caption>`)
	if at < 0 {
		t.Fatalf("弊社の明細がありません:\n%s", body)
	}
	return body[:at], body[at:]
}

// TestOrderItemsAreNormalized は、**品名・品番・単位が畳まれる**ことを固定します。
func TestOrderItemsAreNormalized(t *testing.T) {
	j := realOrderJudgment()
	// 実データの形——半角カナの品名、全角の図番、全角空白。
	j.SourceTable.Rows = [][]string{
		{"1", "ﾌﾞﾗｹｯﾄ", "t3.2×100", "Ｋ１２０－１", "100", "ｾｯﾄ", "390", "39000"},
	}
	j.Items = []orderPDFItem{{
		ItemNo: "Ｋ１２０－１", ItemName: "ﾌﾞﾗｹｯﾄ　　カバー",
		Quantity: "100", Unit: "ｾｯﾄ", Price: "390",
	}}

	source, ours := ourItemsSegment(t, buildOrderPageHTML("000001", "pdf001", j))

	for _, want := range []string{
		"<td>K120-1</td>",        // 全角英数・全角ハイフンが半角へ
		"<td>ブラケット カバー</td>", // 半角カナが全角へ・全角空白が半角へ・重なりは1つへ
		"<td>セット</td>",          // ⚠ 単位も畳む（選択肢に当たらないと色が付く）
	} {
		if !strings.Contains(ours, want) {
			t.Errorf("弊社の明細に %q がありません:\n%s", want, ours)
		}
	}

	// ⚠ **原本の写しは畳みません**——「読んだまま」が名前どおりであること。
	for _, want := range []string{"<td>ﾌﾞﾗｹｯﾄ</td>", "<td>Ｋ１２０－１</td>"} {
		if !strings.Contains(source, want) {
			t.Errorf("原本の写しが書き換えられています（%q がありません）:\n%s", want, source)
		}
	}
}

// TestOrderRowDueDateMatchesPageTag は、**行の納期がページのタグと同じ形**に
// なることを固定します。
//
// ⚠ 行だけ生のまま書いていたので、先方が `2026/10/15` と書くと
// **ページのタグは `2026-10-15`、行は `2026/10/15`** と食い違っていました。
func TestOrderRowDueDateMatchesPageTag(t *testing.T) {
	j := realOrderJudgment()
	j.DueDate = "2026/10/15"

	body := buildOrderPageHTML("000001", "pdf001", j)
	if !strings.Contains(body, "<dt>"+DueDateTag+"</dt><dd>2026-10-15</dd>") {
		t.Errorf("ページのタグが正規形ではありません:\n%s", body)
	}
	_, ours := ourItemsSegment(t, body)
	if strings.Contains(ours, "2026/10/15") {
		t.Errorf("行の納期が生のままです（タグと食い違います）:\n%s", ours)
	}
	if !strings.Contains(ours, "<td>2026-10-15</td>") {
		t.Errorf("行に正規形の納期がありません:\n%s", ours)
	}
}

// TestOrderRowKeepsUnreadableDueDate は、⚠ **日付として読めない納期はそのまま**
// 配られることを固定します（実データの1通目が「最短納期」でした）。
//
// **取り込みは情報を捨てない**（D-3）——読めなかったことは人が見れば分かります。
func TestOrderRowKeepsUnreadableDueDate(t *testing.T) {
	j := realOrderJudgment()
	j.DueDate = "最短納期"

	body := buildOrderPageHTML("000001", "pdf001", j)
	if !strings.Contains(body, "<dt>"+DueDateTag+"</dt><dd>最短納期</dd>") {
		t.Errorf("ページのタグから納期が消えています:\n%s", body)
	}
	_, ours := ourItemsSegment(t, body)
	if !strings.Contains(ours, "<td>最短納期</td>") {
		t.Errorf("行に納期が配られていません:\n%s", ours)
	}
}
