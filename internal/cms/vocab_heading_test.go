package cms

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// 機能見出し（D-2・2026-08-30 決定）のテスト。
//
// セクションの直接の子である最初の h1〜h6 の表示文字がレジストリの表示名と一致すると、
// data-type を書かなくてもそのセクションは形式を持つ。「見える文字がデータの手掛かり」の
// セクションへの適用で、ワンノートの「■見出しの下に表」がそのまま形式宣言になる。
// 解決は vocabTypeOf（walk.go）の1箇所。data-type 属性は明示の正として引き続き勝つ。

// queryPageTags はページの可変タグを "name=value" の列で返します（本文の順）。
//
// ⚠ **ヘッダはタグになりました**（2026-09-18）。それまで機能見出しの節の中の素の `dl` は
// 業務ブロックのヘッダとして `vocab_index` に載っていましたが、やめました
// （`vocab_index.go` の `OnElement` に経緯）。**素の表の経路は残っています**——
// `■材料` のような見出しの下に表が並ぶワンノートの形が受け皿だからです。
func queryPageTags(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT name, value FROM page_tags WHERE page_id = ? ORDER BY seq`, pageID)
	if err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n, v string
		rows.Scan(&n, &v)
		out = append(out, n+"="+v)
	}
	return out
}

// queryIndex は指定形式の索引行を "field=value" の列で返します（文書順）。
func queryIndex(t *testing.T, pageID int, dataType string) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT field, value FROM vocab_index
		 WHERE page_id = ? AND data_type = ? ORDER BY block_no, row_no, field`, pageID, dataType)
	if err != nil {
		t.Fatalf("vocab_indexのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f, v string
		rows.Scan(&f, &v)
		out = append(out, f+"="+v)
	}
	return out
}

// TestHeadingDoesNotDeclareSectionType は、⚠ **節の見出しでは表の形式が決まらない**ことを固定します
// （2026-09-29 に廃止・DBの日本語化 5段目の4——それまではこの試験が「見出しの言葉だけで中の素の表が
// その形式として索引される」ことを確かめていました）。利用者:「最終的には節の中のPDFを節の外にも
// 動かせるようにしたい」——見出しで読むと、表を節の外へ動かしただけで黙って形式が変わる。
// 表は自分の caption で名乗り、そのときは列型もレジストリ宣言から解決される（検査日が date に畳まれる）。
func TestHeadingDoesNotDeclareSectionType(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>加工製品ページ</h1>` +
		`<section data-id="s1">` +
		`<h2>検査記録</h2>` + // ← 見出しは人が読む言葉。形式は決まらない
		`<table>` +
		`<tr><th>品番</th><th>判定</th><th>検査日</th></tr>` +
		`<tr><td>A-1</td><td>合格</td><td>2026/8/31</td></tr>` +
		`</table>` +
		`<table data-id="t2"><caption>検査記録</caption>` + // ← 表が自分で名乗る
		`<tr><th>品番</th><th>判定</th><th>検査日</th></tr>` +
		`<tr><td>B-2</td><td>不合格</td><td>2026/9/1</td></tr>` +
		`</table>` +
		`</section>`
	if err := SyncIndex("000050", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	got := queryIndex(t, 50, "inspection-record")
	want := []string{"判定=不合格", "品番=B-2", "検査日=2026/9/1"} // キャプションの表だけ
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("索引が期待と異なります（見出しの下の素の表が入っていないか）:\ngot  %v\nwant %v", got, want)
	}

	// レジストリ宣言（検査日=date）による正規化が効いていること。
	var norm string
	if err := database.DB.QueryRow(
		`SELECT COALESCE(norm_value,'') FROM vocab_index WHERE page_id = 50 AND field = '検査日'`).Scan(&norm); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if norm != "2026-09-01" {
		t.Errorf("列型がレジストリから解決されていません: norm_value=%q", norm)
	}
	// 由来のブロックIDは表自身のもの。
	var blockID string
	if err := database.DB.QueryRow(
		`SELECT block_id FROM vocab_index WHERE page_id = 50 AND field = '品番'`).Scan(&blockID); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if blockID != "t2" {
		t.Errorf("表の由来が表自身を指していません: block_id=%q", blockID)
	}
}

// TestHeadingSectionTags は、機能見出しのセクションの中の**タグ**が
// `page_tags` に載ることを固定します。
//
// ⚠ **素の `dl`（ヘッダ）は 2026-09-18 に索引から外しました**（ユーザー決定:「素の定義
// リストはDBから外しましょう」）。名前：値は `<dl data-type="tags">` で書きます——
// **見た目と振る舞いが一致する**ようになりました（素の定義リストは何も起こさない）。
// 番人は `TestPlainDLIsNotIndexed`。
func TestHeadingSectionTags(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注ページ</h1>` +
		`<section data-id="s2">` +
		`<h2>顧客の発注書</h2>` +
		`<dl data-type="tags"><dt>発注書番号</dt><dd>PO-H1</dd>` +
		`<dt>発注元</dt><dd>南北</dd></dl>` +
		`</section>`
	if err := SyncIndex("000051", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	got := queryPageTags(t, 51)
	want := []string{"発注書番号=PO-H1", "発注元=南北"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("タグの索引が期待と異なります:\ngot  %v\nwant %v", got, want)
	}
}

// TestDataTypeBeatsHeading は、data-type 属性と見出しが食い違うとき属性が勝つことを
// 検証します（明示は推測に勝つ。既存データの意味が見出し次第で変わらないための守り）。
//
// ⚠ **payload は表です**（2026-09-18 に素の定義リストを索引から外したため）。
func TestDataTypeBeatsHeading(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>混在</h1>` +
		`<section data-type="client-order">` +
		`<h2>検査記録</h2>` + // 見出しは別の形式の言葉
		`<table>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>PO-X</td><td>シャフト</td><td>100</td><td>1</td><td>未着手</td></tr>` +
		`</table>` +
		`</section>`
	if err := SyncIndex("000052", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 属性が勝つので、素の表は `client-order` の明細（Items 宣言）として載る。
	if got := queryIndex(t, 52, "client-order-items"); len(got) != 5 {
		t.Errorf("data-type が勝っていません: client-order-items側 %v", got)
	}
	if got := queryIndex(t, 52, "inspection-record"); len(got) != 0 {
		t.Errorf("見出し側の形式にも索引されています（二重）: %v", got)
	}
}

// TestUnregisteredHeadingIsInert は、未登録の見出し語がただのセクションに留まる
// （中の素の表は索引されない）ことを検証します。見出しは data-type と違って
// 全セクションが普通に持つものなので、未登録語は静かに何もしない。
func TestUnregisteredHeadingIsInert(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>雑記</h1>` +
		`<section>` +
		`<h2>作業メモ</h2>` +
		`<table><tr><th>日付</th></tr><tr><td>2026-08-31</td></tr></table>` +
		`</section>`
	if err := SyncIndex("000053", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = 53`).Scan(&n); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if n != 0 {
		t.Errorf("未登録の見出しのセクションが索引されています: %d 行", n)
	}
}

// TestMirrorMarkerKeepsContentAndHeadingDoesNotMirror は、**鏡の印（`data-mirror`）の中の人の
// 書き込みが消えず、鏡の中身はその下へ毎回描かれる**こと（語彙モデル §11.5-7）と、
// ⚠ **見出しの節では鏡を名乗れない**ことを検証します（2026-09-28 に見出しの節で名乗る鏡を廃止——
// 両方の環境を data-mirror へ移し終えた・利用者:「古い形の鏡を読む道具とコードを消してよいです」）。
// それまでこの試験は、見出しの節で鏡が動くことを確かめていました。
func TestMirrorMarkerKeepsContentAndHeadingDoesNotMirror(t *testing.T) {
	setupSaveTest(t)

	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path, parent_id) VALUES (61, '子のページ', '', 60)`); err != nil {
		t.Fatalf("子ページ作成エラー: %v", err)
	}
	req := httptest.NewRequest("GET", "/000060", nil)
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})

	marked := RenderComputedViews(req, 60, `<h1>親ページ</h1>`+
		`<section data-mirror="子ページ一覧"><p>この一覧は自動で更新されます。</p></section>`)
	for _, want := range []string{
		`この一覧は自動で更新されます。`,      // 人の注記は残る
		`class="vocab-chrome"`, // 鏡の中身はその下に描かれる
		`href="/000061"`,       // 実際に子が並ぶ
	} {
		if !strings.Contains(marked, want) {
			t.Errorf("印の描画結果に %q がありません:\n%s", want, marked)
		}
	}

	heading := RenderComputedViews(req, 60, `<h1>親ページ</h1>`+
		`<section><h2>子ページ一覧</h2><p>この一覧は自動で更新されます。</p></section>`)
	if strings.Contains(heading, "vocab-chrome") || strings.Contains(heading, `href="/000061"`) {
		t.Errorf("⚠ 廃止した見出しの節で鏡が描かれています:\n%s", heading)
	}
	if !strings.Contains(heading, `<h2>子ページ一覧</h2>`) {
		t.Errorf("見出しの節そのものは本文として残るはず:\n%s", heading)
	}
}

// TestCaptionRenameIsNotified は、キャプションで名乗る表の列の改名が告知されることを検証します
// （2026-09-29 まではこの試験が「見出しで名乗る節」で同じことを確かめていた——見出しの形は廃止）。
// ここが黙ると「改名で計算が読めなくなったのに告知ゼロ」という穴が再来する。
func TestCaptionRenameIsNotified(t *testing.T) {
	body := `<table><caption>受注明細</caption>` +
		`<tr><th>品番</th><th>品名（変更）</th><th>単価</th><th>数量</th><th>状態</th></tr>` + // 品名→品名（変更）
		`<tr><td>A</td><td>B</td><td>1</td><td>2</td><td>未着手</td></tr></table>`

	got := UnresolvedVocabFields(body)
	found := false
	for _, g := range got {
		if g == "受注明細: 品名" {
			found = true
		}
	}
	if !found {
		t.Errorf("改名告知に %q がありません: %v", "受注明細: 品名", got)
	}
}
