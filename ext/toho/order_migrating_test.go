package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 「移行中」の受注ページは、受注明細を読む集計に入れない（2026-09-29）。
//
// ワンノートとメールから過去の注文を移すと、**納め済みの注文**も受注ページになります。
// 確かめる前に受注残・必要部材表へ並ぶと、終わった仕事が「残っている」に、納めた品の材料が
// 「買うもの」に化けます（⚠ 同じものを二度買う）。加工製品ページの「移行中」と同じく、
// **印が在るだけで止め**ます。

// TestBacklogSkipsMigratingOrders は、受注残が「移行中」の受注ページを数えず、
// **その枚数を知らせる**ことを固定します（黙って欠けると集計の壊れと見分けられない）。
func TestBacklogSkipsMigratingOrders(t *testing.T) {
	setupExtTest(t, "000190", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 191, -1, "受注", "alice", "302", true)
	seedOrder(t, 192, 191, "あけぼの精工", "2026-10-15",
		item("A-1", "いまの注文", "10", "2026-10-15", ""))
	seedOrder(t, 193, 191, "あけぼの精工", "2026-10-15",
		item("A-2", "移した注文", "10", "2026-10-15", ""))
	// 193 に「移行中」を付け直す。
	body := `<h1>受注</h1><dl data-type="tags"><dt>発注元</dt><dd>あけぼの精工</dd>` +
		`<dt>納期</dt><dd>2026-10-15</dd><dt>` + MigratingTag + `</dt><dd>確認待ち</dd></dl>` +
		`<table><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>` +
		`<th>単価</th><th>納期</th><th>出荷済み</th><th>備考</th><th>状態</th></tr>` +
		item("A-2", "移した注文", "10", "2026-10-15", "") + `</tbody></table>`
	if err := cms.SyncIndex(page.FormatID(193), body); err != nil {
		t.Fatal(err)
	}

	gs, mig := backlogScan(adminUser(), 191)
	if mig != 1 {
		t.Errorf("移行中で飛ばした枚数が %d です（1のはず）", mig)
	}
	if len(gs) != 1 || len(gs[0].Rows) != 1 || gs[0].Rows[0].ItemName != "いまの注文" {
		t.Fatalf("移行中の受注ページの行が受注残に出ています: %+v", gs)
	}
	html := backlogViewHTML(adminUser(), 191)
	if !strings.Contains(html, "受注ページ 1 枚は入れていません") {
		t.Errorf("飛ばした枚数を知らせていません:\n%s", html)
	}
}

// TestUnorderedSkipsMigratingOrders は、必要部材表が「移行中」の受注ページの行を
// 「買うもの」に数えないことを固定します。
func TestUnorderedSkipsMigratingOrders(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	u := &auth.User{Username: "root", IsAdmin: true}
	if list, _ := UnorderedItems(u); len(list) != 1 {
		t.Fatalf("下ごしらえ: 印の無いときは1件のはずです: %#v", list)
	}
	// 同じ受注ページに「移行中」を付ける。
	syncBody(t, 30, `<h1>受注</h1><dl data-type="tags"><dt>`+MigratingTag+`</dt><dd>確認待ち</dd></dl>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>3</td></tr>`+
		`</tbody></table>`)
	list, err := UnorderedItems(u)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("移行中の受注ページの行が必要部材表に出ています: %#v", list)
	}
}
