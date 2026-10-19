package cms

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// タグの値の候補——DB にある値を多い順に（2026-10-19・tag_values.go）。

func tagPage(t *testing.T, id, kind string, meta page.PageMeta) {
	t.Helper()
	newPage(t, id, `<h1>`+id+`</h1><dl data-type="tags"><dt>種類</dt><dd>`+kind+`</dd></dl>`, meta)
}

// TestTagValueCountsOrdersByCountAndHidesUnreadable は、
//   - 値をページの数の多い順に並べる（同じ数なら値の順）
//   - 読めないページの値は数えない・候補に出さない
//   - 口は {values:[{value, count}]} で返す
//
// ことを固定します。
func TestTagValueCountsOrdersByCountAndHidesUnreadable(t *testing.T) {
	setupSaveTest(t)
	open := page.PageMeta{Owner: "alice", Mode: "302"} // ほかの人も読める
	tagPage(t, "000031", "装置", open)
	tagPage(t, "000032", "装置", open)
	tagPage(t, "000033", "装置", open)
	tagPage(t, "000034", "社名", open)
	tagPage(t, "000035", "社名", open)
	tagPage(t, "000036", "加工製品", open)
	tagPage(t, "000037", "秘密の種類", page.PageMeta{Owner: "alice", Mode: "300"}) // alice だけが読める

	bob := &auth.User{Username: "bob"}
	got, err := TagValueCounts(bob, "種類")
	if err != nil {
		t.Fatal(err)
	}
	want := []TagValueCount{{"装置", 3}, {"社名", 2}, {"加工製品", 1}}
	if len(got) != len(want) {
		t.Fatalf("⚠ 候補が %+v です（期待 %+v——読めないページの値は出さない）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("⚠ %d 番目が %+v です（期待 %+v——多い順）", i, got[i], want[i])
		}
	}
	if mine, _ := TagValueCounts(&auth.User{Username: "alice"}, "種類"); len(mine) != 4 {
		t.Errorf("⚠ 持ち主には自分のページの値も出るはず: %+v", mine)
	}

	rr := httptest.NewRecorder()
	TagValuesAPIHandler(rr, auth.WithUser(httptest.NewRequest("GET", "/api/tag-values?name=種類", nil), bob))
	var res struct {
		Values []TagValueCount `json:"values"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil || rr.Code != 200 || len(res.Values) != 3 || res.Values[0].Value != "装置" {
		t.Errorf("⚠ 口の答えが違います: %d %s", rr.Code, rr.Body.String())
	}
}
