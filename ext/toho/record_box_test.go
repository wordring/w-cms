package toho

import (
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// placeOf はページの親を4段たどり、`記録／2026年／10月／依頼` の形で返します（試験で置き場を確かめるため）。
func placeOf(pageID string) string {
	var titles []string
	cur := pageID
	for i := 0; i < 4; i++ {
		meta, ok := page.ReadSidecar(cur)
		if !ok || meta.ParentID == "" || meta.ParentID == cms.TopPageID {
			break
		}
		cur = meta.ParentID
		titles = append([]string{cms.PageTitleByID(pageNum(cur))}, titles...)
	}
	return strings.Join(titles, "／")
}

// TestRecordFolderMakesTaggedFolders は、`記録／年／月／依頼` を作るとき、新しいフォルダに `ページ種類` が入り、
// 2回目は同じフォルダを返す（増えない）ことを固定します（2026-10-16・record_box.go）。
func TestRecordFolderMakesTaggedFolders(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	when := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local)
	id, err := recordFolder("alice", RecordRequestFolder, when)
	if err != nil {
		t.Fatal(err)
	}
	if got := placeOf(id) + "／" + cms.PageTitleByID(pageNum(id)); got != "記録／2026年／10月／依頼" {
		t.Errorf("置き場が違います: %q", got)
	}
	// 新しいフォルダは `ページ種類` を名乗る（年・月・依頼）。
	for cur, want := id, []string{"依頼", "月", "年"}; len(want) > 0; want = want[1:] {
		tags, _ := cms.TagsOfPage(database.DB, pageNum(cur))
		if got := cms.FirstTag(tags, PageKindTag); got != want[0] {
			t.Errorf("%s のページ種類が %q です（%q のはず）", cms.PageTitleByID(pageNum(cur)), got, want[0])
		}
		meta, _ := page.ReadSidecar(cur)
		cur = meta.ParentID
	}
	// 2回目は同じフォルダ。製造記録は同じ月の下の別のフォルダ。
	if again, _ := recordFolder("alice", RecordRequestFolder, when); again != id {
		t.Errorf("2回目に別のフォルダを返しました: %s と %s", id, again)
	}
	work, _ := recordFolder("alice", RecordWorkFolder, when)
	if a, b := placeOf(work), placeOf(id); work == id || a != b {
		t.Errorf("製造記録が同じ月の下にありません: %s（%s）と %s（%s）", work, a, id, b)
	}
}

// TestIssuerTitle は題の頭に出した側を付けること・二重に付けないこと・加工製品には付けないことを固定します。
func TestIssuerTitle(t *testing.T) {
	for _, c := range []struct{ kind, title, want string }{
		{PageKindCustomerOrder, "受注 PO-1", "【顧客】受注 PO-1"},
		{PageKindOurEstimate, "見積　みなと商店", "【弊社】見積　みなと商店"},
		{PageKindVendorQuote, "部材見積依頼の返事", "【業者】部材見積依頼の返事"},
		{PageKindOurRFQ, "【弊社】部材見積依頼　ふじ鍍金", "【弊社】部材見積依頼　ふじ鍍金"},
		{PageKindProduct, "ブラケット", "ブラケット"},
	} {
		if got := issuerTitle(c.kind, c.title); got != c.want {
			t.Errorf("issuerTitle(%q, %q) = %q（%q のはず）", c.kind, c.title, got, c.want)
		}
	}
}
