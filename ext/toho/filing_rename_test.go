package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
)

// TestRevisionKeepsBothNames は、**改定で品名が変わっても、どちらの名前でも探せる**ことを固定します
// （2026-10-01 利用者:「改定図面にする場合、品名を変えてくる場合があるようです。弊社としては、どちらの品名でも
// 検索できる必要が出てきました」「一つの加工製品ページに二つの品名を許容してはどうでしょう？」）——加工製品ページの
// `品名` タグに古い名前と新しい名前が残る。
//
// ⚠ それまでは、改定の行き先を決めるために欄へ入れた**古い名前（既にあるページの題）**を改定図面に書き戻していた
// ので、図面に書いてある新しい名前が消えて、新しい名前では探せませんでした。
func TestRevisionKeepsBothNames(t *testing.T) {
	const inbox = "000061"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// 改定図面——品名が「取付ベース板」に変わって届いた。行き先は既にあるページ（題は古い名前）。
	rev := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1A", "取付ベース板", "標準2輪", "南北スポーツ")
	if r := postFiling(t, u, []filingRequest{secondRow(rev, "取付ベース", "revision")}); len(r) != 1 || r[0].Outcome != "revision" {
		t.Fatalf("改定として合流しません: %+v", r)
	}
	body := readPageBody(t, first)
	if !strings.Contains(body, "<dd>取付ベース板</dd>") {
		t.Errorf("⚠ 改定図面の新しい名前が消えています（欄の古い名前で上書きした？）:\n%s", body)
	}
	for _, want := range []string{"<dt>品名</dt><dd>取付ベース</dd>", "<dt>品名</dt><dd>取付ベース板</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 品名が2つ残っていません（%s が無い）:\n%s", want, body)
		}
	}
	if !hasChildTitled(t, first, "旧版 K120-1 取付ベース") {
		t.Errorf("旧版の子ページの題が、その版の名前になっていません")
	}

	// もう一度改定——また名前が変わった。旧版の名前も一覧に寄る。
	rev2 := makeDrawingPageFrom(t, inbox, "pdf003", "K120-1B", "取付ベースプレート", "標準2輪", "南北スポーツ")
	if r := postFiling(t, u, []filingRequest{secondRow(rev2, "取付ベース", "revision")}); len(r) != 1 || r[0].Outcome != "revision" {
		t.Fatalf("2度目の改定として合流しません: %+v", r)
	}
	if body := readPageBody(t, first); strings.Count(body, "<dt>品名</dt>") != 3 || !strings.Contains(body, "<dd>取付ベースプレート</dd>") {
		t.Errorf("2度目の改定で品名が3つになっていません:\n%s", body)
	}
	if !hasChildTitled(t, first, "旧版 K120-1A 取付ベース板") {
		t.Errorf("2度目の旧版の子ページの題が、その版の名前（取付ベース板）になっていません")
	}

	boxID, _ := CustomerBoxPageID()
	custID, _ := findChildByTitle(boxID, "南北スポーツ")
	productsID, _ := findChildByTitle(custID, ProductsBoxTitle)
	host := mustAtoi(t, productsID)
	rows, err := productListRows(u, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("一覧が1行ではありません: %+v", rows)
	}
	names := strings.Join(rows[0].Names, "・")
	for _, want := range []string{"取付ベースプレート", "取付ベース板"} {
		if !strings.Contains(names, want) {
			t.Errorf("一覧の行に名前 %s がありません: %q", want, names)
		}
	}
	if strings.Contains("・"+names+"・", "・取付ベース・") {
		t.Errorf("題と同じ名前まで並べています: %q", names)
	}
	html := productListViewHTML(u, host)
	if !strings.Contains(html, "取付ベースプレート") || !strings.Contains(html, "取付ベース板") {
		t.Errorf("一覧の絞り込みの文字に新旧の名前がありません:\n%s", html)
	}

	// 整理の候補——どの名前で打っても、既にあるページが候補に出る。
	for _, name := range []string{"取付ベース", "取付ベース板", "取付ベースプレート"} {
		cands := productCandidates(u, "南北スポーツ", "", name)
		if len(cands) != 1 || cands[0].PageID != first {
			t.Errorf("名前「%s」で既にある加工製品が候補に出ません: %+v", name, cands)
		}
	}
}

// hasChildTitled は parentID の子に、題が title のページがあるかを返します。
func hasChildTitled(t *testing.T, parentID, title string) bool {
	t.Helper()
	_, ok := findChildByTitle(parentID, title)
	return ok
}

// TestCandidatesPreferNumberAndName は、整理の候補が**まず番号と名前の両方が合うページ**を出し、無ければどちらかが
// 合うページを出すことを固定します（2026-10-01 利用者:「検索や結びの照合は、最初に品番と品名で行うべきです。
// 一致しない場合に、ほかの方法を試せば良いと思います」）。
func TestCandidatesPreferNumberAndName(t *testing.T) {
	const inbox = "000062"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	a := makeDrawingPage(t, inbox, "K9-1", "台座", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(a, "台座", "")})
	b := makeDrawingPageFrom(t, inbox, "pdf009", "K9-1", "台座カバー", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(b, "台座カバー", "")})

	ids := func(cs []productCandidate) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.PageID)
		}
		return strings.Join(out, ",")
	}
	if got := productCandidates(u, "南北スポーツ", "K9-1", "台座"); ids(got) != a {
		t.Errorf("番号と名前の両方が合うページだけのはず（%s）: %s", a, ids(got))
	}
	if got := productCandidates(u, "南北スポーツ", "K9-1", "別の名前"); len(got) != 2 {
		t.Errorf("名前が合わなければ番号の合うページを全部出すはず: %s", ids(got))
	}
}

// TestStackLinesPutsEachOnItsOwnLine は、加工製品の一覧で図面番号・品番を1つずつ縦に並べることを固定します（2026-10-01
// 利用者:「図面番号が複数ある場合、縦に並べれば全体が上手く表示されるのでは？」）。値はエスケープする。
func TestStackLinesPutsEachOnItsOwnLine(t *testing.T) {
	if got := stackLines("K120-1・K120-1W・<x>"); got != "K120-1<br/>K120-1W<br/>&lt;x&gt;" {
		t.Errorf("縦に並んでいません: %q", got)
	}
	if got := stackLines(""); got != "" {
		t.Errorf("空なら空のはず: %q", got)
	}
}

// TestPartNoCellPrefersPartNo は、加工製品の一覧の「品番」の列が、品番があれば品番だけ・無ければ図面番号に「図番」の印を
// 付けて出すことを固定します（2026-10-01 利用者:「加工製品の一覧は品番があれば図面番号は必要ないかもしれません」）。
func TestPartNoCellPrefersPartNo(t *testing.T) {
	if got := partNoCell(productListRow{PartNo: "K-1", DrawingNo: "A100-B01-001"}); got != "K-1" {
		t.Errorf("品番があるのに図面番号が出ています: %q", got)
	}
	got := partNoCell(productListRow{DrawingNo: "A100-B01-001・A100-B01-002"})
	if !strings.Contains(got, "A100-B01-001<br/>A100-B01-002") || !strings.Contains(got, "（図番）") {
		t.Errorf("品番が無いのに図面番号が印つきで出ていません: %q", got)
	}
	if got := partNoCell(productListRow{}); got != "" {
		t.Errorf("どちらも無ければ空のはず: %q", got)
	}
}
