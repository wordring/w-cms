package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"w-cms/internal/database"

	"golang.org/x/net/html"
	_ "modernc.org/sqlite"
)

func TestGetPageDir(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "5桁のID",
			id:   "00001",
			want: filepath.Join("data/master", "00", "00001"),
		},
		{
			name: "1桁の短いID",
			id:   "1",
			want: filepath.Join("data/master", "00", "1"),
		},
		{
			name: "2桁のID",
			id:   "01",
			want: filepath.Join("data/master", "01", "01"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPageDir(tt.id)
			if got != tt.want {
				t.Errorf("GetPageDir(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestGenerateNextID(t *testing.T) {
	// 1. インメモリのSQLiteデータベースを初期化
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("データベース接続エラー: %v", err)
	}
	defer db.Close()

	// 2. テスト用テーブルの作成
	query := `
	CREATE TABLE pages (
		id INTEGER PRIMARY KEY,
		title TEXT,
		file_path TEXT
	);`
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("テーブル作成エラー: %v", err)
	}

	// 3. データが何もない場合のテスト（初期値 "000000" のはず）
	t.Run("空のDBでのID生成", func(t *testing.T) {
		got := GenerateNextID(db)
		want := "000000"
		if got != want {
			t.Errorf("GenerateNextID() = %v, want %v", got, want)
		}
	})

	// 4. "000001" を追加した後のテスト（"000002" のはず）
	t.Run("000001存在時の次のID生成", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO pages (id, title) VALUES (1, 'test')")
		if err != nil {
			t.Fatal(err)
		}
		got := GenerateNextID(db)
		want := "000002"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 5. 10進数の桁上がりのテスト
	t.Run("10進数での桁上がり（繰り上げ）テスト", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO pages (id, title) VALUES (9, 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "000010"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 6. 大きな値のテスト
	t.Run("より大きな数値IDの連番テスト", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO pages (id, title) VALUES (12345, 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "012346"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})
}

func TestParseAndSyncNestedOrders(t *testing.T) {
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
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// 2. パース対象のテストHTML
	htmlContent := `
	<!DOCTYPE html>
	<html>
	<body>
		<h1>試作受注の記録</h1>
		<m-tag name="親ページ" value="00001"></m-tag>
		<m-tag name="担当者" value="山田"></m-tag>

		<m-file src="attachments/po_test.pdf" tag="顧客の発注書" order-no="PO-T100" client-name="トーア" ordered-at="2026-06-18">
			<m-item item-id="SHAFT-01" item-name="シャフトA" price="8000" quantity="10" status="未着手"></m-item>
			<m-item item-id="SHAFT-02" item-name="シャフトB" price="12000" quantity="5" status="加工中"></m-item>
		</m-file>
	</body>
	</html>
	`

	// 3. コア情報のパーステスト（タイトル・タグ）
	root, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		t.Fatalf("HTMLパースエラー: %v", err)
	}
	core := ParseCore(root)
	if core.Title != "試作受注の記録" {
		t.Errorf("期待値と異なるタイトル: %s", core.Title)
	}
	if len(core.Tags) != 2 {
		t.Errorf("タグ数が合いません: %d", len(core.Tags))
	}

	// 4. 同期処理のテスト（プラグイン経由でDBに反映される）
	pageID := "00002"
	err = SyncIndex(pageID, htmlContent)
	if err != nil {
		t.Fatalf("SyncIndexでエラー: %v", err)
	}

	// データベースの値の確認
	var title string
	err = db.QueryRow("SELECT title FROM pages WHERE id = ?", pageID).Scan(&title)
	if err != nil {
		t.Fatalf("pagesのクエリでエラー: %v", err)
	}
	if title != "試作受注の記録" {
		t.Errorf("データベースのページタイトルが違います: %s", title)
	}

	// page_tags が2件同期されていることを確認
	var tagCount int
	err = db.QueryRow("SELECT COUNT(*) FROM page_tags WHERE page_id = ?", pageID).Scan(&tagCount)
	if err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	if tagCount != 2 {
		t.Errorf("page_tagsの件数が違います: %d", tagCount)
	}

	// client_orders / client_order_items の確認
	var clientName, pdfPath string
	err = db.QueryRow("SELECT client_name, pdf_path FROM client_orders WHERE order_no = ?", "PO-T100").Scan(&clientName, &pdfPath)
	if err != nil {
		t.Fatalf("client_ordersのクエリでエラー: %v", err)
	}
	if clientName != "トーア" || pdfPath != "attachments/po_test.pdf" {
		t.Errorf("発注ヘッダーの値が違います: client=%s, pdf=%s", clientName, pdfPath)
	}

	// 部品点数の集計確認
	var totalQty int
	err = db.QueryRow("SELECT SUM(quantity) FROM client_order_items WHERE order_no = ?", "PO-T100").Scan(&totalQty)
	if err != nil {
		t.Fatalf("client_order_itemsのクエリでエラー: %v", err)
	}
	if totalQty != 15 {
		t.Errorf("合計数量が違います: %d", totalQty)
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
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// 2. 部品ページ(材料マスタ)の登録をシミュレート
	// SHAFT-01 という部品は、鋼材(S45C)が1本、高周波焼入れが1個必要
	_, err = db.Exec(`
		INSERT INTO part_materials (part_id, material_name, cost, supplier_name, quantity, page_id)
		VALUES 
		('SHAFT-01', 'シャフト用鋼材 (S45C)', 2500, '東邦金属工業', 1, '00003'),
		('SHAFT-01', '外注高周波焼入れ', 1500, '山下熱処理', 1, '00003')
	`)
	if err != nil {
		t.Fatalf("部材マスタ登録エラー: %v", err)
	}

	// 3. 受注ページ(00002)の登録をシミュレート
	// 受注：SHAFT-01 を 10本
	_, err = db.Exec(`
		INSERT INTO client_orders (order_no, client_name, page_id) VALUES ('PO-A100', 'トーア', 2)
	`)
	if err != nil {
		t.Fatalf("受注ヘッダー登録エラー: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO client_order_items (order_no, item_id, item_name, price, quantity, status)
		VALUES ('PO-A100', 'SHAFT-01', 'シャフトA', 8000, 10, '加工中')
	`)
	if err != nil {
		t.Fatalf("受注明細登録エラー: %v", err)
	}

	// 自社発注実績：鋼材をすでに10本発注済み
	_, err = db.Exec(`
		INSERT INTO our_orders (order_no, supplier_name, page_id) VALUES ('PO-OUR-001', '東邦金属工業', 2)
	`)
	if err != nil {
		t.Fatalf("自社発注ヘッダー登録エラー: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO our_order_items (order_no, item_name, cost, quantity, status)
		VALUES ('PO-OUR-001', 'シャフト用鋼材 (S45C)', 2500, 10, '未納品')
	`)
	if err != nil {
		t.Fatalf("自社発注明細登録エラー: %v", err)
	}

	// 4. APIハンドラーにHTTPリクエストを送ってテスト
	req, err := http.NewRequest("GET", "/api/required-materials?page_id=00002", nil)
	if err != nil {
		t.Fatalf("リクエスト作成エラー: %v", err)
	}

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
