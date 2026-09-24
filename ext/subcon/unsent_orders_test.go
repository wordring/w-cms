package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// 未発注の発注書（2026-09-24）。要求:「DBから『未発注の発注書』を探します」
// 「発注書ページを削除するとリンクも消えます」。

func unsentPaper(supplier string, statuses ...string) string {
	var b strings.Builder
	b.WriteString(`<h1>発注　` + supplier + `</h1><dl data-type="tags"><dt>` + SupplierTag +
		`</dt><dd>` + supplier + `</dd></dl>`)
	b.WriteString(`<table><caption>発注明細</caption><tbody><tr><th>品名</th><th>数量</th><th>状態</th></tr>`)
	for _, st := range statuses {
		b.WriteString(`<tr><td>ボルト</td><td>1</td><td>` + st + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// TestUnsentOrdersListsOnlyPagesWithUnsentRows は、⚠ **未発注の行を持つページだけ**が
// 並ぶことを固定します——出した紙・取り消した紙は並べません。
//
// ⚠ **空欄の状態も未発注に数えます**（人が手で足した行）。数えないと、その行だけの
// 発注書が黙って一覧から消えます。
func TestUnsentOrdersListsOnlyPagesWithUnsentRows(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 50, 0, "発注　みなと商店", "root", "302", true)
	addPage(t, 51, 0, "発注　ひかりレーザー", "root", "302", true)
	addPage(t, 52, 0, "発注　みらい産業", "root", "302", true)
	addPage(t, 53, 0, "発注　かなめ商会", "root", "302", true)
	syncBody(t, 50, unsentPaper("みなと商店", OrderLineSent, OrderLineUnsent, OrderLineCancelled))
	syncBody(t, 51, unsentPaper("ひかりレーザー", OrderLineSent, OrderLineDelivered))
	syncBody(t, 52, unsentPaper("みらい産業", OrderLineCancelled, OrderLineCancelled))
	syncBody(t, 53, unsentPaper("かなめ商会", ""))

	got, err := UnsentOrders(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnsentOrdersエラー: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("未発注の発注書が %d 枚です（2枚を期待・50と53）: %#v", len(got), got)
	}
	if got[0].PageID != 50 || got[0].Unsent != 1 || got[0].Total != 3 || got[0].Supplier != "みなと商店" {
		t.Errorf("1枚目が違います: %#v", got[0])
	}
	if got[1].PageID != 53 {
		t.Errorf("⚠ 状態が空の行だけの発注書が並んでいません: %#v", got)
	}

	// 出したら消える（鏡なので、本文を変えれば次に数えたとき居ない）。
	syncBody(t, 50, unsentPaper("みなと商店", OrderLineSent, OrderLineSent, OrderLineCancelled))
	got2, _ := UnsentOrders(&auth.User{Username: "root", IsAdmin: true})
	for _, o := range got2 {
		if o.PageID == 50 {
			t.Errorf("⚠ 出した発注書がまだ並んでいます: %#v", o)
		}
	}
}

// TestUnsentOrdersViewSaysWhenEmpty は、⚠ **0件も黙らない**ことと、一覧にリンクが出る
// ことを固定します。
func TestUnsentOrdersViewSaysWhenEmpty(t *testing.T) {
	setupMaterialsPermsTest(t)
	viewer := &auth.User{Username: "root", IsAdmin: true}
	if got := unsentOrdersViewHTML(viewer, 0); !strings.Contains(got, "未発注の発注書はありません") {
		t.Errorf("⚠ 0件のとき黙っています:\n%s", got)
	}
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 60, 0, "発注　みなと商店", "root", "302", true)
	syncBody(t, 60, unsentPaper("みなと商店", OrderLineUnsent))
	got := unsentOrdersViewHTML(viewer, 0)
	for _, want := range []string{`href="/000060"`, "発注　みなと商店", "みなと商店", "1 / 1 行"} {
		if !strings.Contains(got, want) {
			t.Errorf("一覧に %q がありません:\n%s", want, got)
		}
	}
}
