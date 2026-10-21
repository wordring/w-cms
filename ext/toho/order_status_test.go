package toho

import (
	"strings"
	"testing"
)

// TestDeliveredAtFillsWhenAllRowsIn は、部材発注書の取消でない行が全部「納品済」になったとき、`納品日` に今日が入ることを固定します
// （2026-10-21 夜——業者からの納品は部材発注書ページで受け取る）。1行でも残っていれば入れない。取消の行は数えない。
func TestDeliveredAtFillsWhenAllRowsIn(t *testing.T) {
	body := func(st1, st2 string) string {
		return `<h1>【弊社】部材発注　みなと商店</h1><dl data-type="tags"><dt>` + DeliveredAtTag + `</dt><dd><br/></dd></dl>` +
			`<table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>材質</th><th>数量</th><th>状態</th></tr>` +
			`<tr><td>000031</td><td>鉄</td><td>2</td><td>` + st1 + `</td></tr><tr><td>000031</td><td>SUS</td><td>1</td><td>` + st2 + `</td></tr></tbody></table>`
	}
	if got := fillDeliveredAtWhenAllIn(body(OrderLineDelivered, OrderLineSent), "2026-10-21"); strings.Contains(got, "2026-10-21") {
		t.Errorf("⚠ まだ届いていない行があるのに納品日を入れました:\n%s", got)
	}
	if got := fillDeliveredAtWhenAllIn(body(OrderLineDelivered, OrderLineCancelled), "2026-10-21"); !strings.Contains(got, "<dd>2026-10-21</dd>") {
		t.Errorf("⚠ 取消を除いて全部届いたのに納品日が入っていません:\n%s", got)
	}
	if got := fillDeliveredAtWhenAllIn(body(OrderLineCancelled, OrderLineCancelled), "2026-10-21"); strings.Contains(got, "2026-10-21") {
		t.Errorf("⚠ 全部取消の部材発注書に納品日を入れました:\n%s", got)
	}
}
