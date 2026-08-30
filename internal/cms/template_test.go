package cms

import (
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ── ページテンプレート 第1段: 判定と同期除外 ──────────────────────────
// 正本は docs/【考察】ページテンプレート.md §3・§6。

// clientOrderBody は「仮の発注書」を含むテンプレート的な本文です。
// テンプレート配下に置かれたときに **索引へ入ってはいけない** ことを
// 各テストで確かめます。
func clientOrderBody(title, orderNo string) string {
	return `<h1>` + title + `</h1>` +
		`<section data-type="client-order">` +
		`<dl><dt>発注書番号</dt><dd>` + orderNo + `</dd>` +
		`<dt>発注元</dt><dd>雛形商事</dd>` +
		`<dt>発注日</dt><dd>2026-08-20</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>SAMPLE-1</td><td>見本</td><td>100</td><td>1</td><td>未着手</td></tr>` +
		`</tbody></table></section>`
}

// countOrders はサイト全体の受注ヘッダ（<section data-type="client-order">）の
// ブロック数を索引から返します。
func countOrders(t *testing.T) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM (SELECT DISTINCT page_id, block_no FROM vocab_index
		 WHERE data_type = 'client-order')`).Scan(&n); err != nil {
		t.Fatalf("受注ヘッダの集計に失敗: %v", err)
	}
	return n
}

// newTemplateTree はトップ・テンプレートルート・分類ページを用意します。
// 戻り値は分類ページのIDで、その子がテンプレート（葉）になります。
func newTemplateTree(t *testing.T) string {
	t.Helper()
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000010", "<h1>"+TemplateRootTitle+"</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	newPage(t, "000011", "<h1>業務</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000010"})
	return "000011"
}

// TestTemplateSubtreeIsNotIndexed は、テンプレート領域のページが③計算テーブルへ
// 載らないこと、そして**普通のページは従来どおり載る**ことを検証します。
// これが無いと、テンプレートに書いた仮の発注書が手配集計・利益計算に出てきます。
func TestTemplateSubtreeIsNotIndexed(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)

	// 葉＝テンプレート本体。
	newPage(t, "000012", clientOrderBody("受注ページ", "PO-TMPL"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	// 分類ページ自身が業務ブロックを持っていても載らない（説明のつもりで書いた例など）。
	newPage(t, "000013", clientOrderBody("業務の説明", "PO-DOC"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000010"})
	// ルート自身も領域内。
	if err := SyncIndex("000010", clientOrderBody(TemplateRootTitle, "PO-ROOT")); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if n := countOrders(t); n != 0 {
		t.Fatalf("テンプレート領域が索引に載っています: 受注ヘッダ %d 件", n)
	}

	// 回帰: テンプレート領域の外にある普通のページは従来どおり載る。
	newPage(t, "000020", clientOrderBody("本物の受注", "PO-REAL"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	if n := countOrders(t); n != 1 {
		t.Fatalf("普通のページが索引に載っていません: 受注ヘッダ %d 件", n)
	}
}

// TestTemplateRootIsNotItselfATemplate は、ルート自身がテンプレートとして
// 選べないことを検証します（ルートは分類。配下が空のときルートが葉になるため
// 明示的に除外している）。
func TestTemplateRootIsNotItselfATemplate(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)
	newPage(t, "000012", "<h1>受注ページ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})

	if IsUnderTemplateRoot("000010") {
		t.Error("ルート自身が配下と判定されています")
	}
	if !IsTemplateArea("000010") {
		t.Error("ルート自身が領域外と判定されています（索引除外が効かない）")
	}
	if !IsUnderTemplateRoot("000012") || !IsUnderTemplateRoot(classify) {
		t.Error("配下のページが配下と判定されていません")
	}
	if IsTemplateArea(TopPageID) {
		t.Error("トップページが領域内と判定されています")
	}
}

// TestTemplateRootMustBeChildOfTop は、名前が「テンプレート」でも
// トップページの直下でなければルートにならないことを検証します。
func TestTemplateRootMustBeChildOfTop(t *testing.T) {
	setupSaveTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000030", "<h1>どこかのページ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	// 深い位置の「テンプレート」はルートではない。
	newPage(t, "000031", "<h1>"+TemplateRootTitle+"</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000030"})
	newPage(t, "000032", clientOrderBody("受注", "PO-DEEP"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000031"})

	if IsTemplateArea("000032") {
		t.Error("トップ直下でない「テンプレート」がルートとして効いています")
	}
	if n := countOrders(t); n != 1 {
		t.Fatalf("普通のページが索引に載っていません: 受注ヘッダ %d 件", n)
	}
}

// TestTemplateExclusionSurvivesRebuildOrder は**罠の回帰テスト**です
// （docs/【考察】ページテンプレート.md §6.1）。
//
// RebuildDatabase は空のDBへページを1つずつ同期するため、先祖判定を pages テーブルで
// 行うと「先祖がまだ入っていない順序」で誤判定します。ここでは**子のIDを親より小さく**して
// data/master の走査順で子が先に同期されるようにし、それでも除外が効くことを固定します。
func TestTemplateExclusionSurvivesRebuildOrder(t *testing.T) {
	setupSaveTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	// ルートを 000090、その子のテンプレートを 000002 にする＝走査順では子が先。
	newPage(t, "000090", "<h1>"+TemplateRootTitle+"</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	newPage(t, "000002", clientOrderBody("受注ページ", "PO-ORDER"), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000090"})

	if err := RebuildDatabase(); err != nil {
		t.Fatalf("RebuildDatabaseエラー: %v", err)
	}
	if n := countOrders(t); n != 0 {
		t.Fatalf("再構築の順序で除外が漏れました: 受注ヘッダ %d 件", n)
	}
	// 再構築後もページ自体は階層に残っている（コアは同期し続ける）。
	var title string
	if err := database.DB.QueryRow(`SELECT title FROM pages WHERE id = 2`).Scan(&title); err != nil {
		t.Fatalf("テンプレートページが pages から消えています: %v", err)
	}
	if title != "受注ページ" {
		t.Errorf("タイトルが失われています: %q", title)
	}
}

// TestMovingPageIntoTemplateAreaClearsRows は、普通のページをテンプレートフォルダへ
// 移したときに**古い行が残らない**ことを検証します。
//
// 除外を「プラグインを飛ばす」で実装すると洗い替えの DELETE も走らず古い行が残ります。
// 空の本文を渡す実装（sync.go 手順5）ならこのテストが通ります。
func TestMovingPageIntoTemplateAreaClearsRows(t *testing.T) {
	setupSaveTest(t)
	classify := newTemplateTree(t)

	body := clientOrderBody("受注ページ", "PO-MOVE")
	newPage(t, "000040", body, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	if n := countOrders(t); n != 1 {
		t.Fatalf("前提が崩れています: 受注ヘッダ %d 件", n)
	}

	// テンプレートフォルダの下へ移す（サイドカーが親の正本）。
	if _, err := page.SetSidecarParent("000040", classify); err != nil {
		t.Fatalf("親の付け替えに失敗: %v", err)
	}
	if err := SyncIndex("000040", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if n := countOrders(t); n != 0 {
		t.Fatalf("テンプレートへ移したのに古い行が残っています: 受注ヘッダ %d 件", n)
	}

	// 外へ戻せば再び載る（可逆であること）。
	if _, err := page.SetSidecarParent("000040", TopPageID); err != nil {
		t.Fatalf("親の付け替えに失敗: %v", err)
	}
	if err := SyncIndex("000040", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if n := countOrders(t); n != 1 {
		t.Fatalf("テンプレートから戻したのに載りません: 受注ヘッダ %d 件", n)
	}
}
