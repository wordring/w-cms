package cms

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// setupSaveTest は data/master を一時ディレクトリへ切り替え、インメモリDBを用意します。
func setupSaveTest(t *testing.T) *sql.DB {
	t.Helper()

	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db

	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	return db
}

// postSave は SaveAPIHandler へ保存リクエストを送り、レスポンスJSONを返します。
func postSave(t *testing.T, id, htmlBody string) map[string]interface{} {
	t.Helper()

	payload, _ := json.Marshal(map[string]string{"page_id": id, "html": htmlBody})
	req := httptest.NewRequest("POST", "/api/save", strings.NewReader(string(payload)))
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})

	rr := httptest.NewRecorder()
	SaveAPIHandler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("保存が失敗しました: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("レスポンスJSONのパースに失敗: %v (body=%s)", err, rr.Body.String())
	}
	return got
}

// TestSaveSanitizesAndEchoesBack は、危険な本文を保存すると
//   - 正本ファイルにはサニタイズ済みが書かれ
//   - レスポンスの html がその内容と一致し
//   - sanitized=true で「除去が起きた」ことがエディタへ伝わる
//
// ことを検証します（保存時エコーバック方式の核）。
func TestSaveSanitizesAndEchoesBack(t *testing.T) {
	setupSaveTest(t)

	const id = "000042"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "tester", Mode: page.DefaultMode}); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}

	dangerous := `<h1>見出し</h1><script>alert(1)</script><p onclick="alert(2)">本文</p>`
	resp := postSave(t, id, dangerous)

	// レスポンスの sanitized フラグ
	if sanitized, _ := resp["sanitized"].(bool); !sanitized {
		t.Error("危険な本文を保存したのに sanitized=false になっています")
	}

	echoed, _ := resp["html"].(string)
	if strings.Contains(echoed, "script") || strings.Contains(echoed, "onclick") {
		t.Errorf("返却された html に危険な記述が残っています: %s", echoed)
	}
	if !strings.Contains(echoed, "<h1>見出し</h1>") || !strings.Contains(echoed, "<p>本文</p>") {
		t.Errorf("正常な本文まで失われています: %s", echoed)
	}

	// 正本ファイルの中身がサニタイズ済みで、返却内容と一致すること
	saved, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("保存ファイルの読み取りに失敗: %v", err)
	}
	if string(saved) != echoed {
		t.Errorf("保存内容と返却内容が一致しません:\nファイル: %s\n返却: %s", saved, echoed)
	}
	if strings.Contains(string(saved), "script") || strings.Contains(string(saved), "onclick") {
		t.Errorf("正本ファイルに危険な記述が残っています: %s", saved)
	}
}

// TestSaveKeepsEstimateAttributes は、見積もりの m-file を保存したときに、
// プラグインが読むマーカー・値が生き残って集計テーブルへ入ることを検証します
// （保存 → サニタイズ → ファイル → SyncIndex → supplier_estimates の一気通貫）。
//
// かつて item-name がサニタイズ許可リストから漏れており、保存のたびに消えて
// supplier_estimates.item_name が空になっていた。その回帰防止（現形式では
// data-type / data-src の許可が同じ役割を担う）。
func TestSaveKeepsEstimateAttributes(t *testing.T) {
	db := setupSaveTest(t)

	const id = "000044"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "tester", Mode: page.DefaultMode}); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}

	// 業務ブロックは容器 section[data-type="file"] の中身。pdf_path は容器側から拾われる。
	body := `<section data-type="file" data-src="m.pdf"><p>📎 <a href="/data/master/00/000044/m.pdf">見積書.pdf</a></p>` +
		`<dl data-type="supplier-estimate">` +
		`<dt>部材名</dt><dd>側板用鋼材</dd>` +
		`<dt>仕入先</dt><dd>東邦金属工業</dd>` +
		`<dt>見積金額</dt><dd>500</dd>` +
		`<dt>見積日</dt><dd>2026-06-16</dd>` +
		`</dl></section>`
	resp := postSave(t, id, body)

	if sanitized, _ := resp["sanitized"].(bool); sanitized {
		t.Errorf("見積もりの属性がサニタイズで除去されました: %v", resp["html"])
	}

	var itemName, supplierName, pdfPath string
	var cost int
	err := db.QueryRow(
		`SELECT item_name, supplier_name, cost, pdf_path FROM supplier_estimates WHERE page_id = ?`, 44,
	).Scan(&itemName, &supplierName, &cost, &pdfPath)
	if err != nil {
		t.Fatalf("supplier_estimates のクエリでエラー: %v", err)
	}
	// pdf_path は業務ブロックではなく容器の data-src から拾われる（ClosestFileSrc）
	if pdfPath != "m.pdf" {
		t.Errorf("容器の data-src が pdf_path に反映されていません: %q", pdfPath)
	}
	if itemName != "側板用鋼材" {
		t.Errorf("item_name が保存されていません: %q", itemName)
	}
	if supplierName != "東邦金属工業" {
		t.Errorf("supplier_name が保存されていません: %q", supplierName)
	}
	if cost != 500 {
		t.Errorf("cost が保存されていません: %d", cost)
	}
}

// TestSaveKeepsNormalContentIntact は、通常の編集で保存した本文が
// サニタイズによって変化しない（sanitized=false・内容そのまま）ことを検証します。
// エディタのシリアライザ updateHtmlPreview が出力する語彙は許可リストの範囲内なので、
// 普段の保存でエコーバックによる差し替えが起きてはいけません。
func TestSaveKeepsNormalContentIntact(t *testing.T) {
	setupSaveTest(t)

	const id = "000043"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "tester", Mode: page.DefaultMode}); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}

	// updateHtmlPreview が実際に出力する形（改行・インデント込み）を模した本文
	normal := "<dl data-type=\"tags\">\n" +
		"    <dt>発注元</dt>\n" +
		"    <dd>株式会社テスト</dd>\n" +
		"</dl>\n" +
		"<h1>各マシーン用部品の調達</h1>\n" +
		"<p>以下の通り発注しました。</p>\n" +
		"<section data-type=\"file\" data-src=\"po.pdf\">\n" +
		"    <p>📎 <a href=\"/data/master/00/000043/po.pdf\">発注書.pdf</a></p>\n" +
		"    <section data-type=\"client-order\">\n" +
		"        <dl>\n" +
		"            <dt>発注書番号</dt>\n" +
		"            <dd>PO-1</dd>\n" +
		"        </dl>\n" +
		"    </section>\n" +
		"</section>"

	resp := postSave(t, id, normal)

	if sanitized, _ := resp["sanitized"].(bool); sanitized {
		t.Errorf("通常の本文なのに sanitized=true になりました（誤検知）:\n返却: %v", resp["html"])
	}
	if echoed, _ := resp["html"].(string); echoed != normal {
		t.Errorf("通常の本文が変化しました:\n入力: %q\n返却: %q", normal, echoed)
	}
}
