package sheetmetal

// 手配集計（RequiredMaterials）のテスト。コアから移してきたもので、
// 集計そのものが拡張の持ち物になったため置き場もここへ移った。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// clientOrderHTML は受注ページの本文（ヘッダ dl ＋ 明細1行）を組み立てます。
func clientOrderHTML(orderNo, client, itemID string) string {
	return `<section data-type="client-order"><dl>` +
		`<dt>発注書番号</dt><dd>` + orderNo + `</dd>` +
		`<dt>発注元</dt><dd>` + client + `</dd>` +
		`<dt>発注日</dt><dd>2026-08-20</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>` + itemID + `</td><td>部品</td><td>100</td><td>1</td><td></td></tr>` +
		`</tbody></table></section>`
}

// seedOrderPages は受注ページ分のサイドカーを用意します。
func seedOrderPages(t *testing.T, ids ...string) {
	t.Helper()
	origWd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	db, err := sql.Open("sqlite", "file:"+dir+"/t.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := cms.ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	for _, id := range ids {
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330"}); err != nil {
			t.Fatalf("WriteSidecar(%s)エラー: %v", id, err)
		}
	}
}

// TestRequiredMaterialsDoesNotMixPages は、同じ番号を使う2ページの明細が
// 手配集計で混ざらないことを固定します（漏洩ではなく数字の誤り）。
func TestRequiredMaterialsDoesNotMixPages(t *testing.T) {
	seedOrderPages(t, "000051", "000052", "000053")

	// 部品定義ページ53: PART-A に部材が1つ紐づく
	materials := `<dl data-type="tags"><dt>部品番号</dt><dd>PART-A</dd></dl>` +
		`<table data-type="part-materials"><tbody>` +
		`<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>` +
		`<tr><td>鋼板</td><td>800</td><td>A商事</td><td>2</td></tr></tbody></table>`
	if err := cms.SyncIndex("000053", materials); err != nil {
		t.Fatalf("cms.SyncIndex(53)エラー: %v", err)
	}

	// 51と52が同じ番号 PO-9 で、どちらも PART-A を1個ずつ受注している。
	if err := cms.SyncIndex("000051", clientOrderHTML("PO-9", "得意先A", "PART-A")); err != nil {
		t.Fatalf("cms.SyncIndex(51)エラー: %v", err)
	}
	if err := cms.SyncIndex("000052", clientOrderHTML("PO-9", "得意先B", "PART-A")); err != nil {
		t.Fatalf("cms.SyncIndex(52)エラー: %v", err)
	}

	admin := &auth.User{Username: "root", IsAdmin: true}
	for _, pid := range []int{51, 52} {
		list, err := RequiredMaterials(admin, pid)
		if err != nil {
			t.Fatalf("RequiredMaterials(%d)エラー: %v", pid, err)
		}
		if len(list) != 1 {
			t.Fatalf("ページ%d の集計行数が想定と違います: %d (期待 1)", pid, len(list))
		}
		// 明細1行 × 数量1 × 部材数量2 ＝ 2。他ページが混ざれば 4 になる。
		if list[0].TotalRequired != 2 {
			t.Errorf("ページ%d の必要総数に他ページが混ざりました: %d (期待 2)",
				pid, list[0].TotalRequired)
		}
	}
}

func TestRequiredMaterialsCalculation(t *testing.T) {
	// 1. テスト用のインメモリDB初期化
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()

	// アプリ全体のグローバル DB 接続を一時差し替え
	database.DB = db

	// テーブル初期化（本番と同じスキーマ: コア + 全プラグイン）
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := cms.ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// 種まきは実本文を SyncIndex へ通す（索引の書き込みヘルパはコアの非公開）。
	// サイドカーが要るのは、SyncIndex が権限を正本から引き直すため。
	sync := func(id int, body string) {
		t.Helper()
		pid := fmt.Sprintf("%06d", id)
		if err := page.WriteSidecar(pid, page.PageMeta{Owner: "alice", Mode: "330"}); err != nil {
			t.Fatalf("WriteSidecar(%d)エラー: %v", id, err)
		}
		if err := cms.SyncIndex(pid, body); err != nil {
			t.Fatalf("SyncIndex(%d)エラー: %v", id, err)
		}
	}

	// 2. 部品ページ(000003)の材料マスタ
	// SHAFT-01 という部品は、鋼材(S45C)が1本、高周波焼入れが1個必要
	sync(3, `<h1>部品</h1>`+
		`<dl data-type="tags"><dt>部品番号</dt><dd>SHAFT-01</dd></dl>`+
		`<table data-type="part-materials"><tbody>`+
		`<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>`+
		`<tr><td>シャフト用鋼材 (S45C)</td><td>2500</td><td>東邦金属工業</td><td>1</td></tr>`+
		`<tr><td>外注高周波焼入れ</td><td>1500</td><td>山下熱処理</td><td>1</td></tr>`+
		`</tbody></table>`)

	// 3. 受注ページ(000002)：SHAFT-01 を 10本。自社発注で鋼材を10本発注済み。
	sync(2, `<h1>受注</h1>`+
		`<section data-type="client-order"><dl>`+
		`<dt>発注書番号</dt><dd>PO-A100</dd><dt>発注元</dt><dd>トーア</dd></dl>`+
		`<table data-type="client-order-items"><tbody>`+
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>`+
		`<tr><td>SHAFT-01</td><td>シャフトA</td><td>8000</td><td>10</td><td>加工中</td></tr>`+
		`</tbody></table></section>`+
		`<section data-type="our-order"><dl>`+
		`<dt>発注書番号</dt><dd>PO-OUR-001</dd><dt>発注先</dt><dd>東邦金属工業</dd></dl>`+
		`<table data-type="our-order-items"><tbody>`+
		`<tr><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>`+
		`<tr><td>シャフト用鋼材 (S45C)</td><td>2500</td><td>10</td><td>未納品</td></tr>`+
		`</tbody></table></section>`)

	// 4. APIハンドラーにHTTPリクエストを送ってテスト（adminユーザーで権限チェックを通す）
	req, err := http.NewRequest("GET", "/api/required-materials?page_id=00002", nil)
	if err != nil {
		t.Fatalf("リクエスト作成エラー: %v", err)
	}
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(RequiredMaterialsAPIHandler)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("ステータスコードが期待と異なります: got %v want %v", status, http.StatusOK)
	}

	var results []RequiredMaterialResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("JSONのパースに失敗しました: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("結果の部材数が異なります: got %d want 2", len(results))
	}

	// 結果の検証
	// 'シャフト用鋼材 (S45C)': 必要数10, 発注済10 -> 残0
	// '外注高周波焼入れ': 必要数10, 発注済0 -> 残10
	var foundSteel, foundHeat bool
	for _, res := range results {
		if res.MaterialName == "シャフト用鋼材 (S45C)" {
			foundSteel = true
			if res.TotalRequired != 10 || res.Ordered != 10 || res.Remaining != 0 {
				t.Errorf("シャフト用鋼材の計算結果が不正です: %+v", res)
			}
		}
		if res.MaterialName == "外注高周波焼入れ" {
			foundHeat = true
			if res.TotalRequired != 10 || res.Ordered != 0 || res.Remaining != 10 {
				t.Errorf("外注高周波焼入れの計算結果が不正です: %+v", res)
			}
		}
	}

	if !foundSteel {
		t.Error("シャフト用鋼材 (S45C) の結果が見つかりません")
	}
	if !foundHeat {
		t.Error("外注高周波焼入れ の結果が見つかりません")
	}
}

// TestPluginTablesConsistency は、各プラグインの Tables() が宣言するテーブルが
// すべて Schema() で実際に作成されることを検証します（Schema/Tablesのdrift防止）。
