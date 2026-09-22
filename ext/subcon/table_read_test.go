package subcon

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
)

// 2026-09-23 に寄せた口の番人。09-22 の鏡3本と本文の書き換え3本が写していた
// 「行末のクロームのセル」「根の無いノード列の差し替え」を、1か所ずつ固定します。

// TestAddRowChromeCells は、見出し行に空の `<th>`・データ行に1始まりの番号で
// 中身を足すことと、⚠ **どのセルも `vocab-chrome`** であることを固定します。
func TestAddRowChromeCells(t *testing.T) {
	nodes, err := htmldoc.ParseFragment(`<table><tbody>` +
		`<tr><th>a</th></tr><tr><td>1</td></tr><tr><td>2</td></tr></tbody></table>`)
	if err != nil {
		t.Fatal(err)
	}
	table := nodes[0]
	var seen []int
	addRowChromeCells(table, "x-act", func(row int, tr *html.Node) string {
		seen = append(seen, row)
		return `<button data-row="` + string(rune('0'+row)) + `">b</button>`
	})
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("データ行は1始まりで2回のはず: %v", seen)
	}
	out := htmldoc.Render(nodes)
	for _, want := range []string{
		`<th class="vocab-chrome x-act"></th>`,
		`<td class="vocab-chrome x-act"><button data-row="1">b</button></td>`,
		`<td class="vocab-chrome x-act"><button data-row="2">b</button></td>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ %q がありません:\n%s", want, out)
		}
	}
}

// TestSpliceNodesTopLevel は、⚠ **本文の直下の要素には `Parent` が無い**罠を越えて
// 置き換え・削除・直後への挿入ができることを固定します。
func TestSpliceNodesTopLevel(t *testing.T) {
	parse := func(s string) []*html.Node {
		nodes, err := htmldoc.ParseFragment(s)
		if err != nil {
			t.Fatal(err)
		}
		return nodes
	}
	cases := []struct {
		name string
		repl string
		keep bool
		want string
	}{
		{"置き換え", "<i>x</i>", false, "<p>a</p><i>x</i><p>c</p>"},
		{"削除", "", false, "<p>a</p><p>c</p>"},
		{"直後へ挿入", "<i>x</i>", true, "<p>a</p><p>b</p><i>x</i><p>c</p>"},
	}
	for _, c := range cases {
		nodes := parse("<p>a</p><p>b</p><p>c</p>")
		var repl []*html.Node
		if c.repl != "" {
			repl = parse(c.repl)
		}
		got, ok := spliceNodes(nodes, nodes[1], repl, c.keep)
		if !ok || got != c.want {
			t.Errorf("%s: got %q (ok=%v), want %q", c.name, got, ok, c.want)
		}
	}
	// 断片に無い要素を指せば (元のまま, false)。
	nodes := parse("<p>a</p>")
	stray := parse("<p>z</p>")[0]
	if got, ok := spliceNodes(nodes, stray, nil, false); ok || got != "<p>a</p>" {
		t.Errorf("無い要素で ok=%v got=%q", ok, got)
	}
}

// TestSpliceNodesNested は、親のある要素でも同じ3通りが効くことを固定します。
func TestSpliceNodesNested(t *testing.T) {
	for _, c := range []struct {
		keep bool
		want string
	}{
		{false, "<div><p>a</p><i>x</i><p>c</p></div>"},
		{true, "<div><p>a</p><p>b</p><i>x</i><p>c</p></div>"},
	} {
		nodes, _ := htmldoc.ParseFragment("<div><p>a</p><p>b</p><p>c</p></div>")
		target := nodes[0].FirstChild.NextSibling // <p>b</p>
		repl, _ := htmldoc.ParseFragment("<i>x</i>")
		got, ok := spliceNodes(nodes, target, repl, c.keep)
		if !ok || got != c.want {
			t.Errorf("keep=%v: got %q (ok=%v), want %q", c.keep, got, ok, c.want)
		}
	}
}

// TestOrderDraftMirrorPutsBackButtonsAndForm は、発注部材表の鏡が**行ごとの「戻す」**と
// **足元の「発注書を作る」**を出し、どちらもクロームであることを固定します
// （09-22 に書かれた鏡で、番人が無かった）。
func TestOrderDraftMirrorPutsBackButtonsAndForm(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, "発注", "root", "302", true)

	body := orderDraftHTML([]ourOrderLine{
		{ProductID: "000031", Material: "SS400", Shape: "板", Size: "t3.2", Quantity: "2"},
	})
	syncBody(t, 40, body)
	got := showPage(t, &auth.User{Username: "root", IsAdmin: true}, 40, body)

	for _, want := range []string{
		`<th class="vocab-chrome draft-row-act"></th>`, // 見出し行にも1つ（列がずれない）
		`class="vocab-chrome draft-row-act"><button`,   // データ行の「戻す」
		`data-draft-table="1"`, `data-draft-row="1"`,
		`data-draft-go="1"`, "発注書を作る", // 足元の欄
		`class="draft-form-row"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 鏡に %q がありません:\n%s", want, got)
		}
	}
}
