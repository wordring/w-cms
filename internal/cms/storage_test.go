package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
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

// TestSidecarAttributesSync は、ページ属性（親ページID・作成日時・作成者・更新日時）が
// サイドカー（正本）から SyncIndex によって pages テーブルへ反映されること、および
// HTML本文中の旧「親ページID」タグがユーザータグとして混入しないことを確認します。
func TestSidecarAttributesSync(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	defer os.Chdir(origWd)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// サイドカーに属性を書いてから同期する（サイドカーが正本）。
	if err := WriteSidecar("000002", PageMeta{
		Owner: "admin", Mode: DefaultMode,
		ParentID: "000001", CreatedAt: "2026-05-01T10:00:00Z", CreatedBy: "admin",
		UpdatedAt: "2026-06-20T19:03:00Z",
	}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex("000002", "<h1>テストページ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var parent sql.NullInt64
	var createdAt, createdBy, updatedAt string
	if err := db.QueryRow(
		"SELECT parent_id, created_at, created_by, updated_at FROM pages WHERE id = ?", "000002",
	).Scan(&parent, &createdAt, &createdBy, &updatedAt); err != nil {
		t.Fatalf("pagesのクエリでエラー: %v", err)
	}
	if !parent.Valid || parent.Int64 != 1 {
		t.Errorf("親ページIDがサイドカーから反映されていません: %+v", parent)
	}
	if createdAt != "2026-05-01T10:00:00Z" || createdBy != "admin" {
		t.Errorf("作成情報が反映されていません: at=%q by=%q", createdAt, createdBy)
	}
	if updatedAt != "2026-06-20T19:03:00Z" {
		t.Errorf("更新日時がサイドカーから反映されていません: %q", updatedAt)
	}

	// HTML本文に旧「親ページID」タグがあっても、ユーザータグとして混入せず、
	// サイドカーの親ページIDも上書きしない。
	if err := SyncIndex("000002", `<h1>x</h1><m-tag name="親ページID" value="999"></m-tag>`); err != nil {
		t.Fatalf("再同期でエラー: %v", err)
	}
	if err := db.QueryRow("SELECT parent_id FROM pages WHERE id = ?", "000002").Scan(&parent); err != nil {
		t.Fatalf("再クエリエラー: %v", err)
	}
	if !parent.Valid || parent.Int64 != 1 {
		t.Errorf("HTMLの親IDタグがサイドカーの親を上書きしました: %+v", parent)
	}
	var tagCount int
	db.QueryRow(`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = '親ページID'`, "000002").Scan(&tagCount)
	if tagCount != 0 {
		t.Errorf("親ページIDタグがユーザータグとして混入しています: %d", tagCount)
	}
}

// TestSidecarMutators は BumpUpdatedAt / SetSidecarParent が、所有権・作成情報を
// 保持したまま更新日時・親ページIDだけを変更することを確認します（改竄防止の規律）。
func TestSidecarMutators(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	defer os.Chdir(origWd)

	orig := PageMeta{
		Owner: "alice", Group: "sales", Mode: "330",
		CreatedAt: "2026-01-01T00:00:00Z", CreatedBy: "alice", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	if err := WriteSidecar("000003", orig); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}

	// BumpUpdatedAt：更新日時だけ進み、所有権・作成情報は保持される。
	newUpdated, err := BumpUpdatedAt("000003")
	if err != nil {
		t.Fatalf("BumpUpdatedAtエラー: %v", err)
	}
	got, _ := ReadSidecar("000003")
	if got.Owner != "alice" || got.CreatedAt != "2026-01-01T00:00:00Z" || got.CreatedBy != "alice" {
		t.Errorf("BumpUpdatedAtが所有権/作成情報を変更しました: %+v", got)
	}
	if got.UpdatedAt != newUpdated || got.UpdatedAt == "2026-01-01T00:00:00Z" {
		t.Errorf("更新日時が進んでいません: %q", got.UpdatedAt)
	}

	// SetSidecarParent：親ページIDを設定。所有権・作成者は保持される。
	if _, err := SetSidecarParent("000003", "000001"); err != nil {
		t.Fatalf("SetSidecarParentエラー: %v", err)
	}
	got, _ = ReadSidecar("000003")
	if got.ParentID != "000001" {
		t.Errorf("親ページIDが設定されていません: %q", got.ParentID)
	}
	if got.Owner != "alice" || got.CreatedBy != "alice" {
		t.Errorf("SetSidecarParentが所有権/作成者を変更しました: %+v", got)
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
func TestPluginTablesConsistency(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()

	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	existing := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("sqlite_masterのクエリでエラー: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			existing[name] = true
		}
	}
	rows.Close()

	for _, p := range Plugins() {
		for _, tbl := range p.Tables() {
			if !existing[tbl] {
				t.Errorf("プラグイン %q の Tables() が宣言する %q が Schema() で作成されていません", p.Name(), tbl)
			}
		}
	}
}

// TestRebuildDatabase は、全テーブルDROP方式の再構築が
// (1) スキーマのdrift（廃止テーブル）を除去し、
// (2) DBに残ったファイル無しの孤児ページを一掃し、
// (3) data/master のHTMLファイルから正しく再同期する
// ことを検証します。
func TestRebuildDatabase(t *testing.T) {
	// data/master を一時ディレクトリに切り替える（カレントディレクトリを変更）。
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("カレントディレクトリ取得エラー: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	defer os.Chdir(origWd)

	// インメモリDBを準備
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// (a) 廃止プラグインの残存テーブルを模した stale_table を作る
	if _, err := db.Exec(`CREATE TABLE stale_table (x TEXT)`); err != nil {
		t.Fatalf("stale_table作成エラー: %v", err)
	}
	// (b) ファイルが存在しない孤児ページをDBに直接挿入する
	if _, err := db.Exec(`INSERT INTO pages (id, title, file_path) VALUES (999, '孤児', '')`); err != nil {
		t.Fatalf("孤児ページ挿入エラー: %v", err)
	}

	// (c) data/master に実ファイルを1つ作成する（顧客の発注書を含む）
	pageDir := GetPageDir("000001")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("ディレクトリ作成エラー: %v", err)
	}
	htmlContent := `<h1>受注ページ</h1>
<m-file tag="顧客の発注書" order-no="PO-RB1" client-name="トーア">
	<m-item item-id="SHAFT-01" item-name="シャフトA" price="8000" quantity="3" status="未着手"></m-item>
</m-file>`
	if err := os.WriteFile(filepath.Join(pageDir, "000001.html"), []byte(htmlContent), 0644); err != nil {
		t.Fatalf("HTMLファイル作成エラー: %v", err)
	}

	// 再構築を実行
	if err := RebuildDatabase(); err != nil {
		t.Fatalf("RebuildDatabaseでエラー: %v", err)
	}

	// (1) stale_table が消えていること
	var staleCount int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stale_table'`).Scan(&staleCount)
	if staleCount != 0 {
		t.Errorf("廃止テーブル stale_table が再構築後も残っています")
	}

	// (2) 孤児ページ(999)が消え、(3) ファイル由来のページ(1)が存在すること
	var orphan, real int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = 999`).Scan(&orphan)
	if orphan != 0 {
		t.Errorf("ファイル無しの孤児ページが再構築後も残っています")
	}
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = 1`).Scan(&real)
	if real != 1 {
		t.Errorf("ファイル由来のページが再構築されていません")
	}

	// (3) 発注書明細がファイルから再同期されていること
	var qty int
	db.QueryRow(`SELECT quantity FROM client_order_items WHERE order_no = 'PO-RB1'`).Scan(&qty)
	if qty != 3 {
		t.Errorf("発注明細が再同期されていません: quantity=%d", qty)
	}
}

// TestRebuildIfEmpty は、DBが空でファイルが存在するとき自動再構築が走り、
// DBに既存データがあるときは何もしないことを検証します。
func TestRebuildIfEmpty(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("カレントディレクトリ取得エラー: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	defer os.Chdir(origWd)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// data/master にファイルを用意するが、DBは空のまま
	pageDir := GetPageDir("000002")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("ディレクトリ作成エラー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "000002.html"), []byte(`<h1>復元テスト</h1>`), 0644); err != nil {
		t.Fatalf("HTMLファイル作成エラー: %v", err)
	}

	// 空DB＋ファイルあり → 自動再構築が走る
	if err := RebuildIfEmpty(); err != nil {
		t.Fatalf("RebuildIfEmptyでエラー: %v", err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = 2`).Scan(&count)
	if count != 1 {
		t.Errorf("空DBからの自動再構築でページが復元されていません")
	}

	// 既存データあり → 何もしない（タイトルが書き換わらないことで確認）
	db.Exec(`UPDATE pages SET title = '手動編集' WHERE id = 2`)
	if err := RebuildIfEmpty(); err != nil {
		t.Fatalf("RebuildIfEmpty(2回目)でエラー: %v", err)
	}
	var title string
	db.QueryRow(`SELECT title FROM pages WHERE id = 2`).Scan(&title)
	if title != "手動編集" {
		t.Errorf("DBが空でないのに再構築が走りました: title=%s", title)
	}
}
