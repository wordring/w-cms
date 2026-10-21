package toho

import "testing"

// 数で返ってきた欄があっても、解析を捨てない。表の行に数が混じっても、行を落とさない（2026-10-22 の見直し）。
func TestParseOrderJudgmentAcceptsNumbers(t *testing.T) {
	j, err := parseOrderJudgment(`{"is_client_order": true, "order_no": 250401203, "customer": "みなと商店",
		"items": [{"item_no": "A100-B01", "quantity": 5, "price": 1200}],
		"source_table": {"headers": ["No", "品名", "数量", "単価"], "rows": [[1, "ブラケット", 100, 1200.5]]}}`)
	if err != nil {
		t.Fatalf("⚠ 数の混じった答えを捨てました: %v", err)
	}
	if j.OrderNo != "250401203" || j.Customer != "みなと商店" || len(j.Items) != 1 {
		t.Errorf("⚠ 読めた欄が違います: %+v", j)
	}
	if len(j.SourceTable.Rows) != 1 || j.SourceTable.Rows[0][3] != "1200.5" {
		t.Errorf("⚠ 数の混じった行を落としました: %+v", j.SourceTable)
	}
}
