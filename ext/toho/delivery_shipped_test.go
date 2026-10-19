package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 出荷済数量は納品書から数える（2026-10-17・delivery_shipped.go）。

// seedDelivery は納品書ページ id を索引に入れます（`ページ種類：弊社納品書`・納品明細表）。行は {受注ページ, 品番, 数量}。
func seedDelivery(t *testing.T, id int, rows ...[3]string) {
	t.Helper()
	body := `<h1>【弊社】納品書</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` + PageKindOurDelivery + `</dd></dl>` +
		`<table><caption>納品明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>発注書番号</th><th>品番</th><th>品名</th>` +
		`<th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>`
	for _, r := range rows {
		body += `<tr><td></td><td>` + r[0] + `</td><td></td><td>` + r[1] + `</td><td></td><td>` + r[2] + `</td><td>個</td><td></td><td></td></tr>`
	}
	syncBody(t, id, body+`</tbody></table>`)
}

// TestShippedCountsDeliveries は、
//   - 受注ページ×品番で、納品書の数を足す（分納の2枚を足す・別の受注の同じ品番は混ぜない）
//   - `ページ種類：弊社納品書` でないページの納品明細表は数えない
//   - 受注ページの欄にクロームで出す・人が欄に書いた値は使わないと添える（本文の欄は書き換えない）
//
// ことを固定します。
func TestShippedCountsDeliveries(t *testing.T) {
	setupExtTest(t, "000880", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	seedDelivery(t, 881, [3]string{"000890", "A-1", "4"}, [3]string{"000891", "A-1", "100"})
	seedDelivery(t, 882, [3]string{"000890", "a-1", "3"}) // 品番の揺れは畳む
	// ページ種類の無いページ（写しなど）は数えない。
	syncBody(t, 883, `<h1>写し</h1><table><caption>納品明細表</caption><tbody><tr><th>案件</th><th>品番</th><th>数量</th></tr>`+
		`<tr><td>000890</td><td>A-1</td><td>50</td></tr></tbody></table>`)

	look := newShippedLookup(database.DB)
	if got := look.of("000890", "A-1", ""); got != 7 {
		t.Errorf("⚠ 受注 000890 の A-1 が %v です（4+3=7 を期待）", got)
	}
	if got := look.of("/000891", "A-1", ""); got != 100 {
		t.Errorf("⚠ 受注 000891 の A-1 が %v です（100 を期待）", got)
	}

	// 品番が空の行は品名で結ぶ（品番の空いた行どうしの数を重ねない）。
	syncBody(t, 884, `<h1>【弊社】納品書</h1><dl data-type="tags"><dt>`+PageKindTag+`</dt><dd>`+PageKindOurDelivery+`</dd></dl>`+
		`<table><caption>納品明細表</caption><tbody><tr><th>案件</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000892</td><td></td><td>ブラケット(L)</td><td>200</td></tr><tr><td>000892</td><td></td><td>カバー</td><td>50</td></tr></tbody></table>`)
	look = newShippedLookup(database.DB)
	if l, v := look.of("000892", "", "ブラケット(L)"), look.of("000892", "", "カバー"); l != 200 || v != 50 {
		t.Errorf("⚠ 品番の空いた行が品名で分かれていません: ブラケット(L) %v・カバー %v（200・50 を期待）", l, v)
	}

	order := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><caption>案件明細表</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th><th>出荷済数量</th><th>状態</th></tr>` +
		`<tr><td></td><td>A-1</td><td>甲</td><td>10</td><td>100</td><td>9</td><td>未着手</td></tr></tbody></table>`
	syncBody(t, 890, order)
	out := cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000890", nil), adminUser()), 890, order)
	for _, want := range []string{"shipped-mirror", ">7（⚠ 欄に書いた 9 は使いません", "受注残高: 300円"} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ 案件ページに %q がありません:\n%s", want, out)
		}
	}
}
