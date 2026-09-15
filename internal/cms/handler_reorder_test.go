package cms

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 子ページの並べ替えのテスト（2026-09-03）。
//
// 固定するのは2つです:
//   - 並べ替えた結果が並び順キーになり、次の一覧で効く
//   - **他人の親の子でないページは触れない**——ここを緩めると、並べ替えの口が
//     「任意のページのサイドカーを書き換える口」になります

// setupReorderTree は `トップ／箱` の木とファイルDBを用意します。
//
// 2026-09-15 までは通信の試験の下ごしらえ（setupIntakeTest・putIntakeRecord）を借りて
// いましたが、通信を ext/comm へ出したので自前にしました。**並べ替えは通信の機能では
// ない**ので、通信のタグは書きません（引数の受信日時と余分なタグは、借りていたころの
// 呼び方を変えないために残してあります）。ファイルDBなのは、並べ替えが権限を引くため。
func setupReorderTree(t *testing.T) {
	t.Helper()
	newTestFileDB(t)
	for _, p := range []struct{ id, parent, title string }{
		{"000000", "", "トップ"},
		{"000100", "000000", "箱"},
	} {
		if err := page.WriteSidecar(p.id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: p.parent}); err != nil {
			t.Fatalf("サイドカーの作成エラー: %v", err)
		}
		if err := SyncIndex(p.id, "<h1>"+p.title+"</h1>"); err != nil {
			t.Fatalf("SyncIndexエラー: %v", err)
		}
	}
}

// putReorderChild は親の下へ子ページを1枚置きます。
func putReorderChild(t *testing.T, id, parent, title, _ string, _ string) {
	t.Helper()
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: parent}); err != nil {
		t.Fatalf("サイドカーの作成エラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>"+title+"</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

func postReorder(t *testing.T, u *auth.User, parent string, order []string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"order": order})
	req := httptest.NewRequest("POST", "/api/reorder?parent="+parent, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	ReorderAPIHandler(rr, req)
	return rr
}

// TestReorderWritesKeysInOrder は、送った並びがそのままキーになることを固定します。
func TestReorderWritesKeysInOrder(t *testing.T) {
	setupReorderTree(t)
	putReorderChild(t, "000201", "000100", "あ", "2026-09-01T10:00:00+09:00", "")
	putReorderChild(t, "000202", "000100", "い", "2026-09-02T10:00:00+09:00", "")
	putReorderChild(t, "000203", "000100", "う", "2026-09-03T10:00:00+09:00", "")

	u := &auth.User{Username: "alice", IsAdmin: true}
	rr := postReorder(t, u, "000100", []string{"000203", "000201", "000202"})
	if rr.Code != 200 {
		t.Fatalf("並べ替えできません: %d %s", rr.Code, rr.Body.String())
	}
	for i, id := range []string{"000203", "000201", "000202"} {
		meta, ok := page.ReadSidecar(id)
		if !ok {
			t.Fatalf("サイドカーが読めません: %s", id)
		}
		if want := ReorderKey(i); meta.SortKey != want {
			t.Errorf("%s のキーが %q ではなく %q です", id, want, meta.SortKey)
		}
	}

	// 一覧が実際にその順で返ること（キーが効いている）。
	children, err := visibleChildren(u, 100)
	if err != nil {
		t.Fatalf("子ページ一覧: %v", err)
	}
	got := make([]string, 0, len(children))
	for _, c := range children {
		got = append(got, c.Title)
	}
	if len(got) != 3 || got[0] != "う" || got[1] != "あ" || got[2] != "い" {
		t.Errorf("並びが反映されていません: %v", got)
	}
}

// TestReorderKeysSortAsNumbers は、キーが**数の順**に並ぶことを固定します。
// 桁を固定しないと 9 と 10 が文字列比較で逆になります。
func TestReorderKeysSortAsNumbers(t *testing.T) {
	if !(ReorderKey(8) < ReorderKey(9)) {
		t.Errorf("9番目と10番目が逆です: %q < %q", ReorderKey(8), ReorderKey(9))
	}
	if !(ReorderKey(0) < ReorderKey(100)) {
		t.Errorf("桁が増えると逆転します: %q < %q", ReorderKey(0), ReorderKey(100))
	}
}

// TestReorderRejectsForeignChild は、**その親の子でないページを触れない**ことを
// 固定します。ここを緩めると、並べ替えが任意のページのサイドカーを書き換える口に
// なります。
func TestReorderRejectsForeignChild(t *testing.T) {
	setupReorderTree(t)
	putReorderChild(t, "000201", "000100", "受信箱の子", "2026-09-01T10:00:00+09:00", "")
	putReorderChild(t, "000301", "000000", "よその子", "2026-09-02T10:00:00+09:00", "")

	before, _ := page.ReadSidecar("000301")
	rr := postReorder(t, &auth.User{Username: "alice", IsAdmin: true},
		"000100", []string{"000201", "000301"})
	if rr.Code != 400 {
		t.Fatalf("よその子を受け付けました: %d %s", rr.Code, rr.Body.String())
	}
	after, _ := page.ReadSidecar("000301")
	if after.SortKey != before.SortKey {
		t.Errorf("よその子のキーが書き換わりました: %q -> %q", before.SortKey, after.SortKey)
	}
}
