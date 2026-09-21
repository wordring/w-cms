package subcon

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// **結びの気づき**（2026-09-21）——押し出しの取りこぼしを見せ、誤結合を疑わせます。
//
// ⚠ **押し出し（整理の直後に埋める）だけでは取りこぼします**: 整理のときに受注ページが
// 開かれていた／加工製品ページを手で作った／ページの `品番` をあとから人が書いた。
// どれも**黙って埋まらないまま**なので、開いたときに見せます。
//
// ⚠ そして**埋まっている行も疑います**——「1件だけ当たったが、それは別物だった」は
// 機械には最後まで分かりません。**結び先の題と品名の食い違い**が唯一の手掛かりです。

// seedProductPage は加工製品ページを1枚、索引に作ります。
func seedProductPage(t *testing.T, idInt int, title, drawingNo string) {
	t.Helper()
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (?, ?, '')`, idInt, title); err != nil {
		t.Fatalf("ページ作成: %v", err)
	}
	body := `<h1>` + title + `</h1><section><h2>図面</h2>` +
		`<dl data-type="tags"><dt>図面番号</dt><dd>` + drawingNo + `</dd>` +
		`<dt>品番</dt><dd><br/></dd></dl></section>`
	if err := cms.SyncIndex(page.FormatID(idInt), body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
}

// notesFor は受注明細の本文から気づきを取り出します。
func notesFor(t *testing.T, body string) []string {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatalf("パース: %v", err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" && isOrderItemsTable(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if found == nil {
		t.Fatalf("表がありません:\n%s", body)
	}
	// ⚠ **閲覧者は admin にします。** `page.CanView` を通るので、nil のままだと
	// **見せ分けで全部落ち**、番人が「何も出ない」を正しさと取り違えます
	// ——守りを外しても通る試験になってしまいます。
	return orderLinkNotes(database.DB, &auth.User{Username: "root", IsAdmin: true}, found)
}

// TestLinkNoteSuggestsWhenEmpty は、**空いている行に候補を出す**ことを固定します。
func TestLinkNoteSuggestsWhenEmpty(t *testing.T) {
	setupExtTest(t, "000070", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 71, "K120-01-211 受けブラケット", "K120-01-211")

	notes := notesFor(t, orderBody([2]string{"", "K120-01-211"}))
	if len(notes) != 1 {
		t.Fatalf("候補が出ていません: %v", notes)
	}
	if !strings.Contains(notes[0], "000071") {
		t.Errorf("どのページか出ていません: %s", notes[0])
	}
}

// TestLinkNoteStaysSilentWhenAmbiguous は、⚠ **2件以上なら名指ししない**ことを
// 固定します。
//
// ⚠ 中途半端に名指しすると、**かえって誤らせます**。同じ図番で別の品物が実在する
// のは 2026-09-20 に確かめたとおりで、そこは人が見るしかありません。
func TestLinkNoteStaysSilentWhenAmbiguous(t *testing.T) {
	setupExtTest(t, "000072", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 73, "K120-01-211 受けブラケット", "K120-01-211")
	seedProductPage(t, 74, "K120-01-211 別の品物", "K120-01-211")

	if notes := notesFor(t, orderBody([2]string{"", "K120-01-211"})); len(notes) != 0 {
		t.Errorf("⚠ 候補が2件あるのに名指ししています: %v", notes)
	}
}

// TestLinkNoteStaysSilentWhenFilled は、**埋まっていて食い違いも無ければ黙る**ことを
// 固定します。
func TestLinkNoteStaysSilentWhenFilled(t *testing.T) {
	setupExtTest(t, "000075", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 76, "K120-01-211 受けブラケット", "K120-01-211")

	body := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th></tr>` +
		`<tr><td>000076</td><td>K120-01-211</td><td>受けブラケット</td></tr>` +
		`</tbody></table>`
	if notes := notesFor(t, body); len(notes) != 0 {
		t.Errorf("正しく結ばれているのに何か言っています: %v", notes)
	}
}

// TestLinkNoteWarnsOnNameMismatch は、⚠ **結び先の題と品名の食い違いを知らせる**
// ことを固定します。
//
// ⚠ **これが「1件だけ当たったが、それは別物だった」の最後の砦**です。機械が黙って
// 埋める以上、**当たったこと自体を疑う目**がどこかに要ります。
func TestLinkNoteWarnsOnNameMismatch(t *testing.T) {
	setupExtTest(t, "000077", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 78, "K120-01-211 受けブラケット", "K120-01-211")

	body := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th></tr>` +
		`<tr><td>000078</td><td>K120-01-211</td><td>カバー</td></tr>` +
		`</tbody></table>`
	notes := notesFor(t, body)
	if len(notes) != 1 || !strings.Contains(notes[0], "受けブラケット") {
		t.Fatalf("⚠ 食い違いを見逃しています: %v", notes)
	}
	if !strings.HasPrefix(notes[0], "⚠") {
		t.Errorf("警告の印がありません: %s", notes[0])
	}
}

// TestLinkNoteToleratesTitlePrefix は、⚠ **題に図番が付いていても食い違い扱いしない**
// ことを固定します。
//
// ⚠ 加工製品ページの題は「図面番号 図面名称」なので、**完全一致で見ると全行が ⚠ に
// なります**——狼少年になると本物の ⚠ も読まれなくなります。
func TestLinkNoteToleratesTitlePrefix(t *testing.T) {
	setupExtTest(t, "000079", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 80, "K120-01-211 受けブラケット", "K120-01-211")

	for _, name := range []string{"受けブラケット", "ｳｹﾌﾞﾗｹｯﾄ"} {
		body := `<h1>受注</h1><table data-type="` + clientOrderItemsType + `"><tbody>` +
			`<tr><th>弊社品番</th><th>品番</th><th>品名</th></tr>` +
			`<tr><td>000080</td><td>K120-01-211</td><td>` + name + `</td></tr>` +
			`</tbody></table>`
		if notes := notesFor(t, body); len(notes) != 0 {
			t.Errorf("%q を食い違い扱いしています: %v", name, notes)
		}
	}
}

// notesForAs は閲覧者を指定して気づきを取り出します（見せ分けの検査用）。
func notesForAs(t *testing.T, viewer *auth.User, body string) []string {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatalf("パース: %v", err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" && isOrderItemsTable(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if found == nil {
		t.Fatalf("表がありません")
	}
	return orderLinkNotes(database.DB, viewer, found)
}

// TestLinkNoteRespectsVisibility は、⚠ **読めないページを名指ししない**ことを
// 固定します（見せ分け・C案）。
//
// ⚠ **これは変異試験で見つかった穴です**（2026-09-21）。見せ分けを外しても番人が
// 1つも落ちませんでした——「候補が出ない」ことを見る試験はいくつもありましたが、
// **出ない理由が「読めないから」なのか「そもそも仕掛けが動いていないから」なのかを
// 区別していなかった**ためです。**静けさを正しさと取り違えない。**
//
// だからここでは**同じ閲覧者で2枚を比べます**——読める1枚は名指しされ、読めない
// 1枚は名指しされない。片方だけ見ると、また同じ穴が開きます。
func TestLinkNoteRespectsVisibility(t *testing.T) {
	setupExtTest(t, "000081", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")

	// alice 専有（bob は読めない）と、公開（bob も読める）を1枚ずつ。
	addPage(t, 82, -1, "K120-01-211 受けブラケット", "alice", "300", false)
	addPage(t, 83, -1, "K120-01-999 カバー", "alice", "302", true)
	for _, c := range []struct {
		id int
		no string
	}{{82, "K120-01-211"}, {83, "K120-01-999"}} {
		if err := cms.SyncIndex(page.FormatID(c.id),
			`<h1>x</h1><dl data-type="tags"><dt>図面番号</dt><dd>`+c.no+`</dd></dl>`); err != nil {
			t.Fatalf("SyncIndex: %v", err)
		}
	}

	bob := &auth.User{Username: "bob"}
	notes := notesForAs(t, bob, orderBody(
		[2]string{"", "K120-01-211"}, // 読めない → 名指ししない
		[2]string{"", "K120-01-999"}, // 読める   → 名指しする
	))
	joined := strings.Join(notes, "\n")
	if strings.Contains(joined, "000082") {
		t.Errorf("⚠ 読めないページを名指ししています: %v", notes)
	}
	if !strings.Contains(joined, "000083") {
		t.Fatalf("読めるページまで名指ししていません（仕掛けが動いていない疑い）: %v", notes)
	}
}

// seedBody はページの本文ファイルを用意します。
//
// ⚠ `setupExtTest` はサイドカーと索引しか作りません——`RewriteBody` は**本文
// ファイルを読む**ので、無いと「ファイルがありません」で落ちます。
func seedBody(t *testing.T, id, body string) {
	t.Helper()
	if err := os.MkdirAll(page.GetPageDir(id), 0o755); err != nil {
		t.Fatalf("置き場: %v", err)
	}
	if err := os.WriteFile(page.BodyPath(id), []byte(body), 0o644); err != nil {
		t.Fatalf("本文の用意: %v", err)
	}
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
}

// TestLinkProductsToOrderFillsExisting は、⚠ **図面が先・発注書が後でも埋まる**ことを
// 固定します。
//
// ⚠ **実データで見つかった穴です**（2026-09-21）。引き金を「図面の整理」だけに置いて
// いたので、**図面が先に届いていると一度も走りませんでした**——そして**返り注文は
// 必ずこの順**です（図面は何か月も前に来ている）。
func TestLinkProductsToOrderFillsExisting(t *testing.T) {
	setupExtTest(t, "000090", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 91, "K120-01-211 受けブラケット", "K120-01-211")

	seedBody(t, "000090", orderBody([2]string{"", "K120-01-211"}))
	user := &auth.User{Username: "root", IsAdmin: true}
	if n := LinkProductsToOrder(user, "000090"); n != 1 {
		t.Fatalf("埋めた行が %d です（1を期待）", n)
	}
	body, err := cms.ReadPageBody("000090")
	if err != nil {
		t.Fatalf("読み直し: %v", err)
	}
	if !strings.Contains(body, "000091") {
		t.Errorf("本文に書き戻されていません:\n%s", body)
	}
	// ⚠ **2回目は何も起きない**（埋まっている行は触らない）。
	if n := LinkProductsToOrder(user, "000090"); n != 0 {
		t.Errorf("2回目で %d 行書き換えています（人の値も同じ経路で守られます）", n)
	}
}

// TestLinkProductsToOrderStaysSilentWhenAmbiguous は、⚠ **2件以上なら埋めない**ことを
// 固定します。
func TestLinkProductsToOrderStaysSilentWhenAmbiguous(t *testing.T) {
	setupExtTest(t, "000092", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedProductPage(t, 93, "K120-01-211 受けブラケット", "K120-01-211")
	seedProductPage(t, 94, "K120-01-211 別の品物", "K120-01-211")

	seedBody(t, "000092", orderBody([2]string{"", "K120-01-211"}))
	if n := LinkProductsToOrder(&auth.User{Username: "root", IsAdmin: true}, "000092"); n != 0 {
		t.Errorf("⚠ 候補が2件あるのに埋めています: %d行", n)
	}
}
