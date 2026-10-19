package toho

import (
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// あとから改定にする（2026-10-04）——「図面追加」で並べた2つの図面の片方を旧版の子ページへ移し、
// もう片方を改定図面にする。整理の「図面改定」（mergeAsRevision）と同じ形になることを見ます。

// twoDrawingPage は「図面追加」で図面が2つ並んだ加工製品ページを作り、そのIDを返します（上が old・下が new）。
func twoDrawingPage(t *testing.T, inbox, oldNo, oldName, newNo, newName string) string {
	t.Helper()
	dst := makeDrawingPageFrom(t, inbox, "pdf-a", oldNo, oldName, "装置A", "客A")
	src := makeDrawingPageFrom(t, inbox, "pdf-b", newNo, newName, "装置A", "客A")
	if err := mergeAsDrawing(&auth.User{Username: "alice"}, src, dst); err != nil {
		t.Fatalf("mergeAsDrawing: %v", err)
	}
	return dst
}

func TestSupersedeDrawingMovesOldToChild(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "X1", "ブラケット", "X2", "ブラケット")

	before, _ := cms.ReadPageBody(dst)
	if nos := drawingNosOf(before); strings.Join(nos, ",") != "X1,X2" {
		t.Fatalf("図面が X1・X2 の順に並んでいません: %v", nos)
	}
	oldPage, err := supersedeDrawing(user, dst, drawingPick{1, "X2"}, drawingPick{0, "X1"})
	if err != nil {
		t.Fatalf("supersedeDrawing: %v", err)
	}

	after, _ := cms.ReadPageBody(dst)
	blocks, _ := extractDrawingSections(after)
	if len(blocks) != 1 || drawingNoOf(blocks[0]) != "X2" {
		t.Fatalf("加工製品ページに残る図面は新しい X2 だけのはず: %d 個 %q", len(blocks), after)
	}
	if strings.Count(after, "<section") != strings.Count(after, "</section>") {
		t.Errorf("⚠ 本文が釣り合っていません:\n%s", after)
	}
	// 新しい図面は古い図面のいた場所（改訂明細表より上）。
	if i, j := strings.Index(after, "<dd>X2</dd>"), strings.Index(after, "<caption>改訂明細表</caption>"); i < 0 || j < 0 || i > j {
		t.Errorf("新しい図面が改訂明細表より上にありません:\n%s", after)
	}
	// 旧版の子ページ——題と、古い図面のブロックごと。
	title, ok := findChildByTitle(dst, "旧版 X1 ブラケット")
	if !ok || title != oldPage {
		t.Errorf("旧版の子ページ「旧版 X1 ブラケット」がありません（返した %s・見つけた %s）", oldPage, title)
	}
	oldBody, _ := cms.ReadPageBody(oldPage)
	if ob, _ := extractDrawingSections(oldBody); len(ob) != 1 || drawingNoOf(ob[0]) != "X1" || !strings.Contains(ob[0], "file-view") {
		t.Errorf("旧版ページに古い図面がブロックごと入っていません:\n%s", oldBody)
	}
	// 旧版は `ページ種類：加工製品の旧版` を名乗る（2026-10-16）——木の形に頼らずに最新版と見分けるため。
	if !strings.Contains(oldBody, "<dt>"+PageKindTag+"</dt><dd>"+PageKindProductOld+"</dd>") {
		t.Errorf("旧版ページに「%s：%s」がありません:\n%s", PageKindTag, PageKindProductOld, oldBody)
	}
	// 改訂明細表——X2 の行が足され、X1 の行は旧版ページへのリンク。
	if !strings.Contains(after, "<td>X2</td>") {
		t.Errorf("改訂明細表に X2 の行がありません:\n%s", after)
	}
	if !strings.Contains(after, `<a href="/`+oldPage+`">X1</a>`) {
		t.Errorf("改訂明細表の X1 が旧版ページへのリンクになっていません:\n%s", after)
	}
	// 品名は変わっていないので、品名を2つにしない。
	if strings.Count(after, "<dt>"+ItemNameTag+"</dt>") > 1 {
		t.Errorf("品名が変わっていないのに品名が増えています:\n%s", after)
	}
}

// TestSupersedeDrawingNewAboveOld は、新しい図面が既に上にあるとき古い図面を外すだけなこと、
// 品名が変わっていれば2つとも残すことを見ます。
func TestSupersedeDrawingNewAboveOld(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "Y2", "新しい名前", "Y1", "古い名前")

	if _, err := supersedeDrawing(user, dst, drawingPick{0, "Y2"}, drawingPick{1, "Y1"}); err != nil {
		t.Fatalf("supersedeDrawing: %v", err)
	}
	after, _ := cms.ReadPageBody(dst)
	blocks, _ := extractDrawingSections(after)
	if len(blocks) != 1 || drawingNoOf(blocks[0]) != "Y2" {
		t.Fatalf("残る図面は上の Y2 だけのはず:\n%s", after)
	}
	if _, ok := findChildByTitle(dst, "旧版 Y1 古い名前"); !ok {
		t.Errorf("旧版の子ページ「旧版 Y1 古い名前」がありません")
	}
	if !strings.Contains(after, "古い名前") || !strings.Contains(after, "新しい名前") {
		t.Errorf("品名が変わった改定なので、品名を2つとも残すはず:\n%s", after)
	}
}

// TestSupersedeDrawingTakesOldPlace は、新しい図面が下の離れた所にあるとき（手で足した図面）、
// 古い図面のいた場所へ上がることを見ます。あいだの節は残ります。
func TestSupersedeDrawingTakesOldPlace(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "W1", "台", "W2", "台")
	before, _ := cms.ReadPageBody(dst)
	// 2つの図面のあいだに別の節を挟む。
	_, open, _, ok := drawingSectionAt(before, drawingPick{1, "W2"})
	if !ok {
		t.Fatalf("下の図面が見つかりません")
	}
	between := `<section data-id="qqqq"><h2>データ</h2><p>あいだ</p></section>`
	if err := cms.RewriteBody(dst, "alice", func(b string) string { return b[:open] + between + b[open:] }); err != nil {
		t.Fatalf("あいだの節: %v", err)
	}
	if _, err := supersedeDrawing(user, dst, drawingPick{1, "W2"}, drawingPick{0, "W1"}); err != nil {
		t.Fatalf("supersedeDrawing: %v", err)
	}
	after, _ := cms.ReadPageBody(dst)
	if i, j := strings.Index(after, "<dd>W2</dd>"), strings.Index(after, "あいだ"); i < 0 || j < 0 || i > j {
		t.Errorf("新しい図面が古い図面の場所（あいだの節より上）へ上がっていません:\n%s", after)
	}
	if strings.Count(after, "<dd>W2</dd>") != 1 || strings.Count(after, "あいだ") != 1 {
		t.Errorf("図面かあいだの節が2つになったか消えています:\n%s", after)
	}
}

// TestSupersedeDrawingIndentedBody は、エディタで保存した本文（タグの dt と dd・改訂明細表の tr と td のあいだに字下げ）でも
// 図面番号を読めることを見ます（2026-10-04 に職場で「古い図面がこのページに見つかりません」——詰めた形しか読まなかった）。
// 旧版の題に番号が入り、改訂明細表の新しい行は版2（字下げされた行も数える）、古い行は旧版へのリンク。
func TestSupersedeDrawingIndentedBody(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "P1", "台", "P2", "台")
	if err := cms.RewriteBody(dst, "alice", func(b string) string {
		b = strings.ReplaceAll(b, "</dt><dd>", "</dt>\n        <dd>")
		b = regexp.MustCompile(`(<tr data-id="[0-9a-z]+">)<td>`).ReplaceAllString(b, "$1\n            <td>")
		return strings.ReplaceAll(b, "</td><td>", "</td>\n            <td>")
	}); err != nil {
		t.Fatalf("字下げ: %v", err)
	}
	before, _ := cms.ReadPageBody(dst)
	if strings.Contains(before, "</dt><dd>") || !strings.Contains(before, "\n            <td>1</td>") {
		t.Fatalf("字下げした本文になっていません:\n%s", before)
	}
	oldPage, err := supersedeDrawing(user, dst, drawingPick{1, "P2"}, drawingPick{0, "P1"})
	if err != nil {
		t.Fatalf("字下げした本文で断りました: %v", err)
	}
	if id, ok := findChildByTitle(dst, "旧版 P1 台"); !ok || id != oldPage {
		t.Errorf("旧版の題に図面番号がありません（「旧版 P1 台」が無い）")
	}
	after, _ := cms.ReadPageBody(dst)
	if !strings.Contains(after, "<td>2</td><td>P2</td>") {
		t.Errorf("改訂明細表の新しい行が版2になっていません（字下げされた行を数えていない）:\n%s", after)
	}
	if !strings.Contains(after, `<a href="/`+oldPage+`">P1</a>`) {
		t.Errorf("改訂明細表の P1 が旧版ページへのリンクになっていません:\n%s", after)
	}
}

// TestSupersedeDrawingSameNumber は、図面番号を変えない改定（職場の 000226 の形）で、旧版へのリンクが
// **古い行（版1）**に付き、足した新しい行（版2）には付かないことを見ます。
func TestSupersedeDrawingSameNumber(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "S1", "台", "S1", "台")
	oldPage, err := supersedeDrawing(user, dst, drawingPick{1, "S1"}, drawingPick{0, "S1"})
	if err != nil {
		t.Fatalf("supersedeDrawing: %v", err)
	}
	after, _ := cms.ReadPageBody(dst)
	if !strings.Contains(after, `<td>1</td><td><a href="/`+oldPage+`">S1</a></td>`) {
		t.Errorf("旧版へのリンクが古い行（版1）に付いていません:\n%s", after)
	}
	if !strings.Contains(after, "<td>2</td><td>S1</td>") {
		t.Errorf("新しい行（版2）にリンクが付いてしまったか、行がありません:\n%s", after)
	}
}

// TestSupersedeDrawingRefuses は、同じ図面・無い図面・番号の合わない図面（開いてからページが変わった）では
// 何も作らず断ることを見ます。
func TestSupersedeDrawingRefuses(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "Z1", "台", "Z2", "台")
	before, _ := cms.ReadPageBody(dst)

	for _, c := range [][2]drawingPick{
		{{0, "Z1"}, {0, "Z1"}}, // 同じ図面
		{{1, "Z2"}, {2, ""}},   // 3つ目は無い
		{{1, "Z2"}, {0, "Z9"}}, // 番号が合わない
		{{0, "Z2"}, {1, "Z1"}}, // 順が入れ替わっている（何番目かと番号が合わない）
		{{-1, ""}, {0, "Z1"}},
	} {
		if _, err := supersedeDrawing(user, dst, c[0], c[1]); err == nil {
			t.Errorf("new=%v old=%v は断るはず", c[0], c[1])
		}
	}
	if after, _ := cms.ReadPageBody(dst); after != before {
		t.Errorf("断ったのに本文が変わっています")
	}
	if _, ok := findChildByTitle(dst, "旧版 Z1 台"); ok {
		t.Errorf("断ったのに旧版ページができています")
	}
}

// TestOldVersionKeepsPartsAndSteps は（2026-10-19）、改定したとき旧版ページにも工程・部材の節と表が残り、
// 加工製品ページ（新しい版）にも残ること、題の直下のタグ（品番など）は旧版へ写さないことを固定します。
// 利用者:「図面を改定する場合、旧図面の加工製品ページは、工程や部材の項目が消えるのですが、新ページと旧ページの両方に残してほしいです。」
func TestOldVersionKeepsPartsAndSteps(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := twoDrawingPage(t, inbox, "X1", "ブラケット", "X2", "ブラケット")
	parts := `<section><h2>工程</h2><p>曲げ 2か所</p></section>` +
		`<table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td></tr></tbody></table>`
	if err := cms.RewriteBody(dst, "alice", func(cur string) string { return cur + parts }); err != nil {
		t.Fatal(err)
	}
	before, _ := cms.ReadPageBody(dst)
	topTags := leadingTags.FindString(leadingH1.ReplaceAllString(before, ""))

	oldPage, err := supersedeDrawing(user, dst, drawingPick{1, "X2"}, drawingPick{0, "X1"})
	if err != nil {
		t.Fatalf("supersedeDrawing: %v", err)
	}
	after, _ := cms.ReadPageBody(dst)
	oldBody, _ := cms.ReadPageBody(oldPage)
	for _, want := range []string{"<h2>工程</h2>", "曲げ 2か所", "<caption>材料表</caption>", "t4.5*75*1090"} {
		if !strings.Contains(oldBody, want) {
			t.Errorf("⚠ 旧版ページに %q が残っていません:\n%s", want, oldBody)
		}
		if !strings.Contains(after, want) {
			t.Errorf("⚠ 加工製品ページから %q が消えています:\n%s", want, after)
		}
	}
	if ob, _ := extractDrawingSections(oldBody); len(ob) != 1 || drawingNoOf(ob[0]) != "X1" {
		t.Errorf("⚠ 旧版ページの図面は古い X1 だけのはず:\n%s", oldBody)
	}
	if topTags != "" && strings.Contains(oldBody, strings.TrimSpace(topTags)) {
		t.Errorf("⚠ 題の直下のタグ（品番など）を旧版へ写しています:\n%s", oldBody)
	}
	if strings.Count(oldBody, "<section") != strings.Count(oldBody, "</section>") {
		t.Errorf("⚠ 旧版ページの本文が釣り合っていません:\n%s", oldBody)
	}
}
