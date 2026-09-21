package cms

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// トップ直下の置き場の題は**1枚だけ**（2026-09-21 ユーザー:「トップ直下に
// テンプレートページは**一つだけしかないようにすべき**です」）。
//
// ⚠ **それまでは知らせるだけ**でした。知らせる形の問題は、**気づいたときには既に
// 2枚ある**こと——そして**使われるのはいちばん古い1枚**なので、**新しいほうへ書いた
// 内容は誰からも見えないまま残ります**（実データで4枚に増えました）。
func TestTopLevelBoxTitleIsUnique(t *testing.T) {
	newTestFileDB(t)
	RegisterRequiredPage(RequiredPage{Title: "箱テスト", Extension: "test", Why: "試験用"})

	mk := func(id, parent, title string) {
		t.Helper()
		if err := page.WriteSidecar(id, page.PageMeta{
			Owner: "a", Mode: "302", ParentID: parent}); err != nil {
			t.Fatalf("サイドカー: %v", err)
		}
		if err := SyncIndex(id, "<h1>"+title+"</h1>"); err != nil {
			t.Fatalf("SyncIndex: %v", err)
		}
	}
	mk(TopPageID, "", "トップ")
	mk("000601", TopPageID, "箱テスト") // 1枚目（正）
	mk("000602", TopPageID, "ただのページ")
	mk("000603", "000601", "箱テスト") // ⚠ 深いところは止めない

	save := func(id, title string) int {
		t.Helper()
		body := `<h1>` + title + `</h1>`
		// ⚠ `writeBody` は応答だけ書きます（利用者は見ません）——関門は
		//    「題と親」で決まるので、ここでは記録器だけ渡します。
		w := httptest.NewRecorder()
		if _, ok := writeBody(w, id, body); ok {
			return 200
		}
		return w.Code
	}

	// ⚠ **2枚目にしようとしたら止める。**
	if code := save("000602", "箱テスト"); code != 409 {
		t.Errorf("⚠ トップ直下に2枚目の「箱テスト」を作れてしまいます（%d）", code)
	}
	// 1枚目は自分なので通る（自分自身を「既にある」と数えない）。
	if code := save("000601", "箱テスト"); code != 200 {
		t.Errorf("1枚目の保存まで止めています（%d）", code)
	}
	// ⚠ **深いところは止めない**——特別な意味を持つのはトップ直下の1枚だけ。
	if code := save("000603", "箱テスト"); code != 200 {
		t.Errorf("⚠ トップ直下でないページまで止めています（%d）", code)
	}
	// 名簿に無い題は関係ない。
	if code := save("000602", "ただのページ"); code != 200 {
		t.Errorf("置き場でない題まで止めています（%d）", code)
	}
}

// TestDuplicateBoxMessageSaysWhere は、⚠ **どこに在るかを言う**ことを固定します。
// 「作れません」だけでは、人はどうすればよいか分かりません。
func TestDuplicateBoxMessageSaysWhere(t *testing.T) {
	newTestFileDB(t)
	RegisterRequiredPage(RequiredPage{Title: "箱テスト2", Extension: "test", Why: "試験用"})
	for _, p := range []struct{ id, parent, title string }{
		{TopPageID, "", "トップ"}, {"000611", TopPageID, "箱テスト2"},
		{"000612", TopPageID, "別"},
	} {
		page.WriteSidecar(p.id, page.PageMeta{Owner: "a", Mode: "302", ParentID: p.parent})
		SyncIndex(p.id, "<h1>"+p.title+"</h1>")
	}
	why, bad := refuseDuplicateBoxTitle("000612", "<h1>箱テスト2</h1>")
	if !bad {
		t.Fatal("2枚目を止めていません")
	}
	if !strings.Contains(why, "000611") {
		t.Errorf("⚠ 既にあるページの場所を言っていません: %q", why)
	}
}
