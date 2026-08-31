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

// TestHeadingDeclaresSectionType は、見出しの言葉だけで（属性ゼロで）中の素の表が
// その形式として索引されることを検証します。列型もレジストリ宣言から解決される
// （検査日が date として正規化される）ことまで見る。
func TestHeadingDeclaresSectionType(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>部品ページ</h1>` +
		`<section data-id="s1">` +
		`<h2>検査記録</h2>` + // ← この言葉が機能を宣言する。data-type は無い
		`<table>` +
		`<tr><th>品番</th><th>判定</th><th>検査日</th></tr>` +
		`<tr><td>A-1</td><td>合格</td><td>2026/8/31</td></tr>` +
		`</table>` +
		`</section>`
	if err := SyncIndex("000050", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	got := queryIndex(t, 50, "inspection-record")
	want := []string{"判定=合格", "品番=A-1", "検査日=2026/8/31"} // field のバイト順
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("見出し駆動の索引が期待と異なります:\ngot  %v\nwant %v", got, want)
	}

	// レジストリ宣言（検査日=date）による正規化が効いていること。
	var norm string
	if err := database.DB.QueryRow(
		`SELECT COALESCE(norm_value,'') FROM vocab_index WHERE page_id = 50 AND field = '検査日'`).Scan(&norm); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if norm != "2026-08-31" {
		t.Errorf("列型がレジストリから解決されていません: norm_value=%q", norm)
	}

	// 由来のブロックIDは包んでいる section のもの。
	var blockID string
	if err := database.DB.QueryRow(
		`SELECT block_id FROM vocab_index WHERE page_id = 50 AND field = '品番'`).Scan(&blockID); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if blockID != "s1" {
		t.Errorf("素の表の由来が section を指していません: block_id=%q", blockID)
	}
}

// TestHeadingSectionWithHeaderDL は、見出し＋素の dl（ヘッダ）の形が業務文書ブロックと
// 同じに索引されることを検証します（<section><h2>顧客の発注書</h2><dl>…）。
func TestHeadingSectionWithHeaderDL(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注ページ</h1>` +
		`<section>` +
		`<h2>顧客の発注書</h2>` +
		`<dl><dt>発注書番号</dt><dd>PO-H1</dd><dt>発注元</dt><dd>トーア</dd></dl>` +
		`</section>`
	if err := SyncIndex("000051", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	got := queryIndex(t, 51, "client-order")
	want := []string{"発注書番号=PO-H1", "発注元=トーア"} // dl は row_no＝文書順
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("見出し駆動のヘッダ索引が期待と異なります:\ngot  %v\nwant %v", got, want)
	}
}

// TestDataTypeBeatsHeading は、data-type 属性と見出しが食い違うとき属性が勝つことを
// 検証します（明示は推測に勝つ。既存データの意味が見出し次第で変わらないための守り）。
func TestDataTypeBeatsHeading(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>混在</h1>` +
		`<section data-type="client-order">` +
		`<h2>検査記録</h2>` + // 見出しは別の形式の言葉
		`<dl><dt>発注書番号</dt><dd>PO-X</dd></dl>` +
		`</section>`
	if err := SyncIndex("000052", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if got := queryIndex(t, 52, "client-order"); len(got) != 1 || got[0] != "発注書番号=PO-X" {
		t.Errorf("data-type が勝っていません: client-order側 %v", got)
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

// TestNestedHeadingIsOwnFunction は、入れ子のセクションの見出しが入れ子自身の機能で
// あって親には効かないことを検証します（§11.5-1: 一番近い祖先が勝つ）。
func TestNestedHeadingIsOwnFunction(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>入れ子</h1>` +
		`<section>` + // 見出しなし＝ただの区切り
		`<section><h2>検査記録</h2>` +
		`<table><tr><th>品番</th></tr><tr><td>B-2</td></tr></table>` +
		`</section>` +
		`</section>`
	if err := SyncIndex("000054", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if got := queryIndex(t, 54, "inspection-record"); len(got) != 1 || got[0] != "品番=B-2" {
		t.Errorf("入れ子の見出し駆動が働いていません: %v", got)
	}
}

// TestHeadingMirrorRendersAndKeepsContent は、見出しの言葉だけで鏡（計算ビュー）が
// 動き、かつ**人の書き込み（見出し・注記）が消えない**ことを検証します
// （語彙モデル §11.5-7:「見出しが鏡を呼び、人の書き込みは保存されて残り、
// 鏡の中身はその下へ毎回描かれる」）。
func TestHeadingMirrorRendersAndKeepsContent(t *testing.T) {
	setupSaveTest(t)

	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path, parent_id) VALUES (61, '子のページ', '', 60)`); err != nil {
		t.Fatalf("子ページ作成エラー: %v", err)
	}

	body := `<h1>親ページ</h1>` +
		`<section>` +
		`<h2>子ページ一覧</h2>` + // ← 表示名がそのまま鏡の引き金
		`<p>この一覧は自動で更新されます。</p>` + // 人の注記
		`</section>`

	req := httptest.NewRequest("GET", "/000060", nil)
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})
	out := RenderComputedViews(req, 60, body)

	for _, want := range []string{
		`<h2>子ページ一覧</h2>`,     // 見出しは残る
		`この一覧は自動で更新されます。`,        // 注記も残る
		`class="vocab-chrome"`,  // 鏡の中身はその下に描かれる
		`href="/000061"`,        // 実際に子が並ぶ
	} {
		if !strings.Contains(out, want) {
			t.Errorf("描画結果に %q がありません:\n%s", want, out)
		}
	}
}
