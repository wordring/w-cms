package toho

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"w-cms/internal/auth"
)

// 整理の「行き先を探す」（2026-10-03・filing_search.go）。
//
// 利用者:「図面を追加してもどこに入れるか入力する欄が無い」——二つ目の図面（溶接図など）は番号も名前も違うので、機械の
// 候補に出ず、相手の題を知らなければ行き先を決められなかった。

func getFilingSearch(t *testing.T, u *auth.User, customer, q string) []productHit {
	t.Helper()
	v := url.Values{}
	v.Set("customer", customer)
	v.Set("q", q)
	req := httptest.NewRequest("GET", "/api/filing-search?"+v.Encode(), nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	FilingSearchAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("探せません: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Results []productHit `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("応答を読めません: %v %s", err, rr.Body.String())
	}
	return out.Results
}

// TestFilingSearchFindsByPartsOfNames は、⚠ **題・図面番号の一部やページ番号で既にある加工製品が見つかる**こと、
// **同じ取引先のものが先に出る**こと、**当たらなければ空**であることを固定します。
func TestFilingSearchFindsByPartsOfNames(t *testing.T) {
	const inbox = "000063"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	mine := makeDrawingPage(t, inbox, "K120-B01-7", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(mine, "取付ベース", "")})
	other := makeDrawingPage(t, inbox, "A100-B01-3", "取付ベース補強", "大型", "みなと商店")
	postFiling(t, u, []filingRequest{{PageID: other, Customer: "みなと商店", MachineName: "大型", DrawingName: "取付ベース補強"}})

	got := getFilingSearch(t, u, "南北スポーツ", "取付")
	if len(got) != 2 || got[0].PageID != mine || got[1].PageID != other {
		t.Fatalf("「取付」で2件・同じ取引先が先のはず: %+v", got)
	}
	if got[0].Customer != "南北スポーツ" || got[0].Machine != "標準2輪" || got[0].Title != "取付ベース" {
		t.Errorf("押したときに欄へ入れる値（取引先・装置名称・題）が違います: %+v", got[0])
	}
	// 図面番号の一部（区切り・大小は畳む）。
	if got := getFilingSearch(t, u, "", "k120b01"); len(got) != 1 || got[0].PageID != mine {
		t.Errorf("図面番号の一部で見つかりません: %+v", got)
	}
	// ページ番号（/ を付けても）。
	if got := getFilingSearch(t, u, "", "/"+other); len(got) != 1 || got[0].PageID != other {
		t.Errorf("ページ番号で見つかりません: %+v", got)
	}
	if got := getFilingSearch(t, u, "南北スポーツ", "在りもしない品物"); len(got) != 0 {
		t.Errorf("当たらないのに %+v", got)
	}
	if got := getFilingSearch(t, u, "南北スポーツ", "  "); len(got) != 0 {
		t.Errorf("空で探して %+v", got)
	}
}
