package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestFileDrawingsAsksWhenSameCodeExists は、整理の「新規」で、同じ客先に同じ品番（図面番号）の加工製品ページが既にあれば
// 作る前に確かめることを固定します（2026-10-19・filing_same_code.go）。
//
// 2026-10-01 利用者:「とすると、二つの加工製品ページが同じ品番を持つのはマズいので、加工製品ページを作る前にチェックし、編集者に選択させる必要があります」
//
//   - 題が違っても（同じ題なら「図面改定・図面追加・新規」を選ぶ別の関門）、同じ客先に同じ番号があれば needs_confirm（same_code）
//   - ⚠ 図面の確かめ・装置名の確かめの印では通らない（別の印）
//   - 「別の品物として作る」（confirm_same_code）で通る
//   - 会社が違えば、同じ番号でも確かめない（「会社が違えば同じ品番を許します」）
func TestFileDrawingsAsksWhenSameCodeExists(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "みなと商店")
	if res := postFiling(t, u, []filingRequest{{PageID: first, Customer: "みなと商店", MachineName: "標準2輪", DrawingName: "取付ベース"}}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("1枚目を収められません: %+v", res)
	}

	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース（左）", "標準2輪", "みなと商店")
	row := filingRequest{PageID: second, Customer: "みなと商店", MachineName: "標準2輪", DrawingName: "取付ベース（左）"}
	res := postFiling(t, u, []filingRequest{row})
	if len(res) != 1 || res[0].Outcome != "needs_confirm" || res[0].Confirm != "same_code" ||
		!strings.Contains(res[0].Message, "/"+first) || !strings.Contains(res[0].Message, "K120-1") {
		t.Fatalf("同じ客先に同じ品番があるのに確かめません: %+v", res)
	}
	if meta, _ := page.ReadSidecar(second); meta.ParentID != inbox {
		t.Errorf("確かめの前に動かしています: %+v", meta)
	}
	row.ConfirmRevision, row.ConfirmNoMachine = true, true
	if res := postFiling(t, u, []filingRequest{row}); len(res) != 1 || res[0].Confirm != "same_code" {
		t.Errorf("ほかの確かめの印で、品番の確かめを飛ばしました: %+v", res)
	}
	row.ConfirmRevision, row.ConfirmNoMachine, row.ConfirmSameCode = false, false, true
	if res := postFiling(t, u, []filingRequest{row}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("「別の品物として作る」で収めていません: %+v", res)
	}

	other := makeDrawingPageFrom(t, inbox, "pdf003", "K120-1", "取付ベース", "標準2輪", "ひかりレーザー")
	if res := postFiling(t, u, []filingRequest{{PageID: other, Customer: "ひかりレーザー", MachineName: "標準2輪", DrawingName: "取付ベース"}}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Errorf("会社が違うのに確かめています: %+v", res)
	}
}

// TestProductLookupPrefersFiledPages は、受注の行から加工製品ページを引くとき、整理を済ませたページがあれば
// 整理を待つページ（🤖解析が2通目の図面から作った下書き）を数えないことを固定します（2026-10-19）。
// それまでは「同じ客先に2枚」になり、整理で片付けるまで受注の行が結ばれませんでした（【要求】東邦拡張 §12）。
func TestProductLookupPrefersFiledPages(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	filed := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "みなと商店")
	if res := postFiling(t, u, []filingRequest{{PageID: filed, Customer: "みなと商店", MachineName: "標準2輪", DrawingName: "取付ベース"}}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("収められません: %+v", res)
	}
	waiting := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "標準2輪", "みなと商店")

	got := productPagesForCustomer(database.DB, "みなと商店", "K120-1", "")
	if len(got) != 1 || page.FormatID(got[0]) != filed {
		t.Errorf("整理を済ませたページ /%s だけに当たっていません（整理待ち /%s）: %v", filed, waiting, got)
	}

	// 整理を待つページしか無ければ、いままでどおりそれに当たる（受注を先に結べる）。
	only := makeDrawingPageFrom(t, inbox, "pdf003", "Z900-1", "ブラケット", "標準2輪", "みなと商店")
	if got := productPagesForCustomer(database.DB, "みなと商店", "Z900-1", ""); len(got) != 1 || page.FormatID(got[0]) != only {
		t.Errorf("整理を待つページしか無いのに当たりません: %v", got)
	}
}

// TestFileDrawingsMakesNoFolderBeforeConfirm は、確かめで止まった整理が、行き先の装置のフォルダを作らないことを固定します
// （2026-10-22 の見直し）。それまでは確かめより先にフォルダを作っていて、「やめる」を選ぶと空のフォルダが残った。
func TestFileDrawingsMakesNoFolderBeforeConfirm(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "みなと商店")
	if res := postFiling(t, u, []filingRequest{{PageID: first, Customer: "みなと商店", MachineName: "標準2輪", DrawingName: "取付ベース"}}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("1枚目を収められません: %+v", res)
	}
	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "新型3輪", "みなと商店")
	row := filingRequest{PageID: second, Customer: "みなと商店", MachineName: "新型3輪", DrawingName: "取付ベース"}
	if res := postFiling(t, u, []filingRequest{row}); len(res) != 1 || res[0].Confirm != "same_code" {
		t.Fatalf("同じ品番の確かめが出ません: %+v", res)
	}
	for _, m := range machineFoldersOf(u, "みなと商店") {
		if m == "新型3輪" {
			t.Fatalf("⚠ 確かめの前に装置のフォルダを作りました: %v", machineFoldersOf(u, "みなと商店"))
		}
	}
	row.ConfirmSameCode = true
	if res := postFiling(t, u, []filingRequest{row}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("確かめのあとに収めていません: %+v", res)
	}
	if got := strings.Join(machineFoldersOf(u, "みなと商店"), ","); !strings.Contains(got, "新型3輪") {
		t.Errorf("確かめのあとに装置のフォルダを作っていません: %s", got)
	}
}
