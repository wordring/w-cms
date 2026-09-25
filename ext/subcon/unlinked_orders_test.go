package subcon

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// 加工製品ページに結べない受注明細の警告（2026-09-25・引き継ぎの P1）。
//
// ユーザー:「受注したのに加工製品のページが無い場合、それは作れないわけですから、
// 何かが間違っています」「弊社品番の背景を薄赤にしても良いかもしれません」。
//
// ⚠ **見るのは「赤い行 ⇔ 必要部材表に材料が出ない行」**です。赤くするだけの番人は、
// 全部を赤くしても通ります——**結べる行が赤くならないこと**も同じ試験で見ます。

// unlinkedOrderRow は受注明細の1行（弊社品番・品番・品名・数量・状態）を組みます。
func unlinkedOrderRow(our, code, name, status string) string {
	return `<tr><td>` + our + `</td><td>` + code + `</td><td>` + name +
		`</td><td>1</td><td>` + status + `</td></tr>`
}

// unlinkedOrderTable は受注明細の表を組みます。
func unlinkedOrderTable(rows ...string) string {
	return `<table data-type="` + clientOrderItemsType + `"><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>状態</th></tr>` +
		strings.Join(rows, "") + `</tbody></table>`
}

// seedProductWithCode は図面番号タグを持つ加工製品ページを1枚作ります。
func seedProductWithCode(t *testing.T, id int, title, code, owner, mode string, public bool) {
	t.Helper()
	addPage(t, id, 0, title, owner, mode, public)
	syncBody(t, id, `<h1>`+title+`</h1>`+
		`<dl data-type="tags"><dt>図面番号</dt><dd>`+code+`</dd></dl>`)
}

// markedRows は描画結果の受注明細から、弊社品番が薄赤になった行（1始まり）を返します。
func markedRows(t *testing.T, out string) []int {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(out)
	if err != nil {
		t.Fatalf("パース: %v", err)
	}
	table := findElement(nodes, func(n *html.Node) bool {
		return n.Data == "table" && isOrderItemsTable(n)
	})
	if table == nil {
		t.Fatalf("受注明細がありません:\n%s", out)
	}
	var got []int
	for i, tr := range rowsOf(table) {
		if i == 0 || inChrome(tr) {
			continue
		}
		cells := cellsOf(tr)
		if len(cells) > 0 && strings.Contains(cms.Attr(cells[0], "class"), unlinkedCellClass) {
			got = append(got, i)
		}
	}
	return got
}

// TestUnlinkedRowsAreMarkedOnOrderPage は、受注ページで**結べない行だけ**が薄赤になり、
// 足元に理由が出ることを固定します。
func TestUnlinkedRowsAreMarkedOnOrderPage(t *testing.T) {
	setupMaterialsPermsTest(t)
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "受注", "root", "302", true)
	seedProductWithCode(t, 71, "K120-01-211 留めブラケット", "K120-01-211", "root", "302", true)
	seedProductWithCode(t, 72, "K120-01-300 ベース", "K120-01-300", "root", "302", true)
	seedProductWithCode(t, 73, "K120-01-300 ベース（別物）", "K120-01-300", "root", "302", true)

	body := `<h1>受注</h1>` + unlinkedOrderTable(
		unlinkedOrderRow("", "A100-B01-001", "ステー", ""),              // 1 加工製品ページが無い
		unlinkedOrderRow("", "", "ボルト", ""),                          // 2 弊社品番も品番も空
		unlinkedOrderRow("", "K120-01-211", "留めブラケット", ""),         // 3 品番で1枚に当たる（結べる）
		unlinkedOrderRow("000099", "", "カバー", ""),                    // 4 弊社品番のページが無い
		unlinkedOrderRow("", "A100-B01-002", "ステー", StatusDone),      // 5 完了（黙る）
		unlinkedOrderRow("", "K120-01-300", "ベース", ""),               // 6 2枚に当たる
		`<tr><td></td><td></td><td></td><td></td><td></td></tr>`,      // 7 空の行（黙る）
		unlinkedOrderRow("000071", "K120-01-211", "留めブラケット", ""),   // 8 弊社品番で結べる
		unlinkedOrderRow("", "A100-B01-003", "ステー", ""),              // 9 加工製品ページが無い
	)
	req := httptest.NewRequest("GET", "/000070", nil)
	req = auth.WithUser(req, &auth.User{Username: "root", IsAdmin: true})
	out := cms.RenderComputedViews(req, 70, body)

	want := []int{1, 2, 4, 6, 9}
	got := markedRows(t, out)
	if len(got) != len(want) {
		t.Fatalf("薄赤の行 %v（期待 %v）:\n%s", got, want, out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("薄赤の行 %v（期待 %v）", got, want)
		}
	}

	for _, s := range []string{
		// ⚠ **同じ理由の行は1行にまとめる**（7行とも同じ理由だと足元が表より長くなる）。
		"⚠ 1・9行目の加工製品ページがありません",
		"⚠ 2行目は弊社品番も品番も空です",
		"⚠ 4行目「カバー」の弊社品番 000099 のページがありません",
		"⚠ 6行目「ベース」の品番 K120-01-300 は、加工製品ページの複数に当たります",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("足元に %q が出ていません:\n%s", s, out)
		}
	}
	// ⚠ **候補を名指ししない**（2枚に当たるときは、どちらかを言うとかえって誤らせる）。
	if strings.Contains(out, "000073") {
		t.Errorf("⚠ 2枚に当たる行で候補を名指ししています:\n%s", out)
	}
	// ⚠ **警告は本文に残らない**——足元はクローム、印は class（サニタイズで落ちる）。
	if saved := cms.Sanitize(out); strings.Contains(saved, "加工製品ページがありません") ||
		strings.Contains(saved, unlinkedCellClass) {
		t.Errorf("⚠ 警告が本文へ保存されます:\n%s", saved)
	}
}

// TestUnlinkedRowsSpeakWithoutOurItemColumn は、⚠ **弊社品番の列が無い表でも黙らない**
// ことを固定します（2026-09-25 に職場のデータで踏んだ——必要部材表は数えるのに、
// 指された受注ページを開くと何も書いていなかった）。
func TestUnlinkedRowsSpeakWithoutOurItemColumn(t *testing.T) {
	setupMaterialsPermsTest(t)
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 70, 0, "受注", "root", "302", true)

	body := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>数量</th></tr>` +
		`<tr><td>A100-B01-001</td><td>ステー</td><td>3</td></tr></tbody></table>`
	req := httptest.NewRequest("GET", "/000070", nil)
	req = auth.WithUser(req, &auth.User{Username: "root", IsAdmin: true})
	out := cms.RenderComputedViews(req, 70, body)

	for _, s := range []string{"⚠ 1行目の加工製品ページがありません", "弊社品番の列がありません"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q が出ていません:\n%s", s, out)
		}
	}
	if strings.Contains(out, unlinkedCellClass) {
		t.Errorf("⚠ 弊社品番ではない列を塗っています:\n%s", out)
	}
}

// TestUnlinkedOrdersMatchRequiredParts は、必要部材表の警告が**必要部材表に出ない行**を
// 数えることを固定します（出る行・完了・読めない受注ページは数えない）。
func TestUnlinkedOrdersMatchRequiredParts(t *testing.T) {
	setupMaterialsPermsTest(t)
	withProductCodeTags(t, "図面番号", "品番")
	seedProcurement(t, "root", "302", true) // 受注30 → 加工製品31（結べる・材料あり）
	seedProductWithCode(t, 35, "K120-01-500 秘の品", "K120-01-500", "alice", "300", false)
	addPage(t, 33, 0, "受注 みらい産業", "root", "302", true)
	syncBody(t, 33, `<h1>受注 みらい産業</h1>`+unlinkedOrderTable(
		unlinkedOrderRow("", "A100-B01-001", "ステー", ""),
		unlinkedOrderRow("", "A100-B01-002", "ステー", StatusDone),
		// ⚠ 読めない加工製品に結ばれた行は、**結べている**ので数えない（C案）。
		unlinkedOrderRow("000035", "K120-01-500", "秘の品", ""),
	))
	addPage(t, 34, 0, "受注 あけぼの精工", "alice", "300", false) // alice 専有
	syncBody(t, 34, `<h1>受注 あけぼの精工</h1>`+unlinkedOrderTable(
		unlinkedOrderRow("", "A100-B01-009", "ステー", ""),
	))

	bob := &auth.User{Username: "bob"}
	got := UnlinkedOrders(bob)
	if len(got) != 1 || got[0].PageID != 33 || got[0].Rows != 1 {
		t.Fatalf("結べない受注明細の数え方が違います: %#v", got)
	}
	// ⚠ **静けさと成功を区別する**——読める人には alice の受注も数えます。
	if all := UnlinkedOrders(&auth.User{Username: "root", IsAdmin: true}); len(all) != 2 {
		t.Fatalf("⚠ 読める人に数えていません: %#v", all)
	}

	// 必要部材表の表示にも出ること（受注ページへのリンクと行数）。
	out := unorderedViewHTML(bob, 1)
	for _, s := range []string{"加工製品ページに結べない受注明細が <strong>1行</strong>",
		`href="/000033"`, "受注 みらい産業</a>（1行）"} {
		if !strings.Contains(out, s) {
			t.Errorf("必要部材表に %q が出ていません:\n%s", s, out)
		}
	}
	if strings.Contains(out, "000034") || strings.Contains(out, "あけぼの精工") {
		t.Errorf("⚠ 読めない受注ページが出ています:\n%s", out)
	}
	// 結べる受注（30）の材料は、警告と並んでちゃんと出る。
	if !strings.Contains(out, "t4.5") {
		t.Errorf("結べる行の材料が消えています:\n%s", out)
	}
}

// TestUnlinkedOrdersSilentWhenAllLinked は、全部結べていれば**何も言わない**ことを
// 固定します（常に何か言っていると、人は読まなくなる）。
func TestUnlinkedOrdersSilentWhenAllLinked(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)

	root := &auth.User{Username: "root", IsAdmin: true}
	if got := UnlinkedOrders(root); len(got) != 0 {
		t.Fatalf("結べているのに数えています: %#v", got)
	}
	if out := unorderedViewHTML(root, 1); strings.Contains(out, "unorder-unlinked") {
		t.Errorf("結べているのに警告が出ています:\n%s", out)
	}
}
