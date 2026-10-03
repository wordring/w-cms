package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// 図面追加で同じ図面が在る・図面改定で同じ図面が無いときの確認（2026-10-03・filing_search.go の mergeMismatch）。
//
// 利用者:「図面追加で同じ図面が在ったら警告、図面改定で同じ図面が無ければ警告ということになります。警告ダイアログが出て、
// 追加するかやめるか選んではどうでしょうか」。

// TestFilingAddWarnsWhenSameDrawingExists は、⚠ **図面追加の行き先に同じ図面番号の図面が既にあれば確認を求め、
// 確かめたら（confirm_revision）足す**ことを固定します。
func TestFilingAddWarnsWhenSameDrawingExists(t *testing.T) {
	const inbox = "000065"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// 同じ図面番号（版の印だけ違う）を「図面追加」で——たぶん改定か重複。
	same := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1_rev1", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(same, "取付ベース", "drawing")})
	if len(results) != 1 || results[0].Outcome != "needs_confirm" || !strings.Contains(results[0].Message, "図面追加ですが") {
		t.Fatalf("同じ図面が在るのに確かめずに足しました: %+v", results)
	}
	if body := readPageBody(t, first); strings.Contains(body, "K120-1_rev1") {
		t.Fatalf("確かめる前に足しています")
	}
	row := secondRow(same, "取付ベース", "drawing")
	row.ConfirmRevision = true
	results = postFiling(t, u, []filingRequest{row})
	if len(results) != 1 || results[0].Outcome != "added" {
		t.Fatalf("確かめたのに足しません: %+v", results)
	}
}

// TestFilingRevisionWarnsWhenNoSameDrawing は、⚠ **図面改定の行き先に同じ図面（版違い）が無ければ確認を求め、
// 確かめたら差し替える**ことを固定します。
func TestFilingRevisionWarnsWhenNoSameDrawing(t *testing.T) {
	const inbox = "000066"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// まるで別の番号の図面を「図面改定」で——たぶん別の図面（図面追加）。
	other := makeDrawingPageFrom(t, inbox, "pdf002", "Z900-3", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(other, "取付ベース", "revision")})
	if len(results) != 1 || results[0].Outcome != "needs_confirm" || !strings.Contains(results[0].Message, "図面改定ですが") ||
		!strings.Contains(results[0].Message, "K120-1") {
		t.Fatalf("同じ図面が無いのに確かめずに改定しました: %+v", results)
	}
	row := secondRow(other, "取付ベース", "revision")
	row.ConfirmRevision = true
	results = postFiling(t, u, []filingRequest{row})
	if len(results) != 1 || results[0].Outcome != "revision" {
		t.Fatalf("確かめたのに改定しません: %+v", results)
	}
}

// TestSameDrawingStrictAndLoose は、同じ図面の見分けの2つの強さを固定します（確からしいときだけ確認を出す向き）。
func TestSameDrawingStrictAndLoose(t *testing.T) {
	cases := []struct {
		a, b          string
		strict, loose bool
	}{
		{"K120-1", "K120-1", true, true},
		{"K120-1_rev0", "K120-1 rev1", true, true}, // 版の印は見ない
		{"k120-1", "K1201", true, true},            // 区切り・大小
		{"K120-1", "K120-1A", false, true},         // 改訂記号の英字（改定では同じ図面）
		{"K120-1A", "K120-1B", false, true},
		{"K120-1", "K120-1W", false, true}, // 溶接図かもしれない——図面追加では別の図面
		{"K120-1", "K120-10", false, false}, // 数字の付け足しは別の図面
		{"K120-1", "Z900-3", false, false},
		{"", "K120-1", false, false},
	}
	for _, c := range cases {
		if got := sameDrawingStrict(c.a, c.b); got != c.strict {
			t.Errorf("sameDrawingStrict(%q, %q) = %v（%v のはず）", c.a, c.b, got, c.strict)
		}
		if got := sameDrawingLoose(c.a, c.b); got != c.loose {
			t.Errorf("sameDrawingLoose(%q, %q) = %v（%v のはず）", c.a, c.b, got, c.loose)
		}
	}
}
