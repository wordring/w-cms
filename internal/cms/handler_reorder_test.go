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
	setupIntakeTest(t)
	putIntakeRecord(t, "000201", "000100", "あ", "2026-09-01T10:00:00+09:00", "")
	putIntakeRecord(t, "000202", "000100", "い", "2026-09-02T10:00:00+09:00", "")
	putIntakeRecord(t, "000203", "000100", "う", "2026-09-03T10:00:00+09:00", "")

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
	setupIntakeTest(t)
	putIntakeRecord(t, "000201", "000100", "受信箱の子", "2026-09-01T10:00:00+09:00", "")
	putIntakeRecord(t, "000301", "000000", "よその子", "2026-09-02T10:00:00+09:00", "")

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
