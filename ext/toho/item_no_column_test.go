package toho

import "testing"

// TestItemNoColumnSwitchesToCode は、⚠ **品番に名前の列が選ばれたら記号の列へ替える**ことを固定します
// （2026-09-29 利用者:「品番が装置名称になっています。これは、顧客が部品番号の項目にそう書いてきたから」）。
func TestItemNoColumnSwitchesToCode(t *testing.T) {
	j := &orderJudgment{
		SourceTable: orderSourceTable{
			Headers: []string{"部品番号", "図面番号", "品名", "数量"},
			Rows: [][]string{
				{"搬送ユニット", "A100-B01-02", "ベース板", "2"},
				{"搬送ユニット", "A100-B01-03", "側板", "2"},
				{"", "", "小計", ""}, // 品番の無い行は見ない
			},
		},
		Items: []orderPDFItem{
			{ItemNo: "搬送ユニット", ItemNoSource: "部品番号", ItemName: "ベース板", Quantity: "2"},
			{ItemNo: "搬送ユニット", ItemNoSource: "部品番号", ItemName: "側板", Quantity: "2"},
		},
	}
	// 明細の順を原本と逆にしておく——並びではなく中身で当てていることを見る。
	j.Items[0], j.Items[1] = j.Items[1], j.Items[0]
	if n := fixItemNoColumn(j); n != 2 {
		t.Fatalf("替えた数 = %d, want 2", n)
	}
	// ⚠ 全部の行が同じ名前でも、1行目の図番で揃えない——品名・数量で行を当てる。
	want := map[string]string{"ベース板": "A100-B01-02", "側板": "A100-B01-03"}
	for _, it := range j.Items {
		if it.ItemNo != want[it.ItemName] {
			t.Errorf("%s の品番 = %q, want %q", it.ItemName, it.ItemNo, want[it.ItemName])
		}
		if it.ItemNoSource != "図面番号" {
			t.Errorf("item_no_source = %q, want 図面番号", it.ItemNoSource)
		}
	}
}

// TestItemNoColumnSameRowsUsedOnce は、品名も数量も同じ明細が2つあっても、行を1度ずつ使うことを見ます。
func TestItemNoColumnSameRowsUsedOnce(t *testing.T) {
	j := &orderJudgment{
		SourceTable: orderSourceTable{
			Headers: []string{"部品番号", "図面番号", "品名", "数量"},
			Rows: [][]string{
				{"搬送ユニット", "A100-B01-02", "ボス", "1"},
				{"搬送ユニット", "A100-B01-09", "ボス", "1"},
			},
		},
		Items: []orderPDFItem{
			{ItemNo: "搬送ユニット", ItemName: "ボス", Quantity: "1"},
			{ItemNo: "搬送ユニット", ItemName: "ボス", Quantity: "1"},
		},
	}
	fixItemNoColumn(j)
	if j.Items[0].ItemNo != "A100-B01-02" || j.Items[1].ItemNo != "A100-B01-09" {
		t.Errorf("品番 = %q, %q, want A100-B01-02, A100-B01-09", j.Items[0].ItemNo, j.Items[1].ItemNo)
	}
}

// TestItemNoColumnMatchesRowByRow は、行ごとに正しい図番へ替わることを見ます。
func TestItemNoColumnMatchesRowByRow(t *testing.T) {
	j := &orderJudgment{
		SourceTable: orderSourceTable{
			Headers: []string{"No", "部品番号", "図番", "数量"},
			Rows: [][]string{
				{"1", "送り台", "K120-001", "1"},
				{"2", "受け台", "K120-002", "1"},
			},
		},
		Items: []orderPDFItem{
			{ItemNo: "送り台", ItemNoSource: "部品番号"},
			{ItemNo: "受け台", ItemNoSource: "部品番号"},
		},
	}
	fixItemNoColumn(j)
	if j.Items[0].ItemNo != "K120-001" || j.Items[1].ItemNo != "K120-002" {
		t.Errorf("品番 = %q, %q, want K120-001, K120-002", j.Items[0].ItemNo, j.Items[1].ItemNo)
	}
	// ⚠ 「No」（数だけの列）は記号と見なさない——そちらへ替えていないこと。
	if j.Items[0].ItemNoSource != "図番" {
		t.Errorf("item_no_source = %q, want 図番", j.Items[0].ItemNoSource)
	}
}

// TestItemNoColumnLeavesCodesAlone は、品番が既に記号なら何もしないことを見ます。
func TestItemNoColumnLeavesCodesAlone(t *testing.T) {
	j := &orderJudgment{
		SourceTable: orderSourceTable{
			Headers: []string{"品番", "図番"},
			Rows:    [][]string{{"P-100", "K120-001"}},
		},
		Items: []orderPDFItem{{ItemNo: "P-100", ItemNoSource: "品番"}},
	}
	if n := fixItemNoColumn(j); n != 0 || j.Items[0].ItemNo != "P-100" {
		t.Errorf("触ってはいけない: n=%d item_no=%q", n, j.Items[0].ItemNo)
	}
}

// TestItemNoColumnNoCodeColumn は、記号の列が無ければ名前のまま残す（人が直す）ことを見ます。
func TestItemNoColumnNoCodeColumn(t *testing.T) {
	j := &orderJudgment{
		SourceTable: orderSourceTable{
			Headers: []string{"部品番号", "数量", "単価"},
			Rows:    [][]string{{"搬送ユニット", "2", "1,200"}},
		},
		Items: []orderPDFItem{{ItemNo: "搬送ユニット", ItemNoSource: "部品番号"}},
	}
	if n := fixItemNoColumn(j); n != 0 || j.Items[0].ItemNo != "搬送ユニット" {
		t.Errorf("替える先が無いのに替えた: n=%d item_no=%q", n, j.Items[0].ItemNo)
	}
}

// TestItemNoColumnThroughParse は、Gemini の返答を読むところで効くことを見ます。
func TestItemNoColumnThroughParse(t *testing.T) {
	resp := `{"is_client_order": true, "order_no": "PO-1",
  "source_table": {"headers": ["部品番号", "図面番号", "数量"], "rows": [["搬送ユニット", "A100-B01-02", "2"]]},
  "items": [{"item_no": "搬送ユニット", "item_no_source": "部品番号", "item_name": "ベース板", "quantity": "2"}]}`
	j, err := parseOrderJudgment(resp)
	if err != nil {
		t.Fatal(err)
	}
	if j.Items[0].ItemNo != "A100-B01-02" || j.Items[0].ItemNoSource != "図面番号" {
		t.Errorf("品番 = %q（%q）, want A100-B01-02（図面番号）", j.Items[0].ItemNo, j.Items[0].ItemNoSource)
	}
}

func TestLooksLikeCode(t *testing.T) {
	for v, want := range map[string]bool{
		"A100-B01-02": true, "K120-3": true, "100-01-00": true, "ABC123": true,
		"２": false, "1,200": false, "12": false, "搬送ユニット": false, "": false, "ABC": false,
	} {
		if got := looksLikeCode(v); got != want {
			t.Errorf("looksLikeCode(%q) = %v, want %v", v, got, want)
		}
	}
}
