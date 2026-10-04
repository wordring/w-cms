package toho

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 加工製品ページの「受注の単価」（2026-10-04・order_prices.go）——利用者:「発注書から最新価格が分かるという目論見もあります」
// →「客先の注文の単価」。

func pricedOrderBody(no, client, date string, rows ...[5]string) string {
	b := `<h1>受注 ` + no + `</h1><dl data-type="tags"><dt>` + OrderNoTag + `</dt><dd>` + no + `</dd><dt>` + OrderClientTag + `</dt><dd>` + client +
		`</dd><dt>` + OrderedAtTag + `</dt><dd>` + date + `</dd></dl><table data-type="` + clientOrderItemsType + `"><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単価</th><th>状態</th></tr>`
	for _, r := range rows {
		b += `<tr><td>` + r[0] + `</td><td>` + r[1] + `</td><td>カバー</td><td>` + r[2] + `</td><td>` + r[3] + `</td><td>` + r[4] + `</td></tr>`
	}
	return b + `</tbody></table>`
}

// TestProductPageListsOrderPrices は、⚠ **弊社品番がこのページの行と、客先と品番で当たる行が並び**（品番で当てた行には ※）、
// 別の客先・別の加工製品に結ばれた行・読めない受注は並ばないこと、新しい順・受注ページへのリンクを固定します。
func TestProductPageListsOrderPrices(t *testing.T) {
	setupExtTest(t, "000700", page.PageMeta{Owner: "root", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 701, -1, "カバー", "root", "302", true)
	product := `<h1>カバー</h1><dl data-type="tags"><dt>品番</dt><dd>K-1</dd><dt>` + ClientNameTag + `</dt><dd>みなと商店</dd></dl>` +
		productWithSupplied[len(`<h1>カバー</h1>`):]
	seedBody(t, "000701", product)
	for _, o := range []struct {
		id     int
		owner  string
		mode   string
		public bool
		body   string
	}{
		{702, "root", "302", true, pricedOrderBody("A-1", "みなと商店", "2026-05-01", [5]string{"000701", "K-1", "10", "500", "納品済"})},
		{703, "root", "302", true, pricedOrderBody("A-2", "みなと商店", "2026-07-01", [5]string{"", "K-1", "20", "520", "未着手"})},
		{704, "root", "302", true, pricedOrderBody("B-1", "かなめ商会", "2026-08-01", [5]string{"", "K-1", "5", "999", ""})},
		{705, "root", "302", true, pricedOrderBody("A-3", "みなと商店", "2026-09-01", [5]string{"000799", "K-1", "5", "777", ""})},
		{706, "alice", "300", false, pricedOrderBody("A-4", "みなと商店", "2026-09-15", [5]string{"000701", "K-1", "5", "888", ""})},
	} {
		addPage(t, o.id, -1, "受注", o.owner, o.mode, o.public)
		seedBody(t, page.FormatID(o.id), o.body)
	}
	show := func(u *auth.User) string {
		return cms.RenderComputedViews(auth.WithUser(httptest.NewRequest("GET", "/000701", nil), u), 701, product)
	}
	bob := &auth.User{Username: "bob"}
	if page.GetPerms(706).CanRead(bob) {
		t.Fatal("前提が崩れています: bob は /000706 を読めてはいけません")
	}
	got := show(bob)
	for _, want := range []string{"🧾 受注の単価", "500円", "520円", `<a href="/000702">2026-05-01</a>`, `<a href="/000703">2026-07-01</a>`,
		">A-1<", ">A-2<", ">K-1※<", ">納品済<", `class="vocab-chrome rfq-quotes order-prices"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("⚠ 鏡が走っていないか、%q が出ていません:\n%s", want, got)
		}
	}
	for _, not := range []string{"999円", "777円", "888円"} {
		if strings.Contains(got, not) {
			t.Errorf("⚠ 並べてはいけない行（別の客先・別の加工製品・読めない受注）の %s が出ています:\n%s", not, got)
		}
	}
	if strings.Index(got, "520円") > strings.Index(got, "500円") {
		t.Errorf("⚠ 新しい受注（2026-07-01）が先に来ていません:\n%s", got)
	}
	if n := strings.Count(got, ">K-1※<"); n != 1 {
		t.Errorf("※ は品番で当てた1行だけのはず（弊社品番で結ばれた行には付けない）: %d\n%s", n, got)
	}
	if i, j := strings.Index(got, "💴 見積回答"), strings.Index(got, "🧾 受注の単価"); !(i >= 0 && i < j) {
		t.Errorf("受注の単価は見積回答の後ろのはず（%d・%d）", i, j)
	}
	if admin := show(&auth.User{Username: "root", IsAdmin: true}); !strings.Contains(admin, "888円") {
		t.Errorf("読める人には /000706 の行も出るはず:\n%s", admin)
	}
}
