package toho

// 拡張パッケージのテスト土台。
//
// `internal/cms` のテストヘルパはパッケージ境界を越えないので、必要な分だけ
// ここに持ちます（コアへ「テストのためだけの公開関数」を足さないための割り切り）。
// 中身は upload_test.go の setupUploadTest と同じで、**ページを1枚だけ用意して
// 索引まで通す**ところまで。

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// setupExtTest は一時ディレクトリ＋インメモリDBを用意し、ページを1枚作ります。
func setupExtTest(t *testing.T, id string, p page.PageMeta) {
	t.Helper()

	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	// ⚠ **試験から Gemini を呼ばせない**（2026-10-01）——開発機の環境に本物のキーがあると、守りが壊れた試験は試験のデータを
	// 外部（Gemini）へ送ってしまう（名前の関門を外す変異で、実際に送られた）。判定を差し替えない試験は「キーが無い」で止まる。
	t.Setenv("GEMINI_API_KEY", "")

	db, err := sql.Open("sqlite", ":memory:")
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
	if err := page.WriteSidecar(id, p); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}
	if err := cms.SyncIndex(id, "<h1>添付テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	seedBoxTemplates(t)
}

// seedBoxTemplates は置き場のテンプレートを用意します（2026-09-27 から、置き場は**同じ題の
// テンプレートからだけ**作られる・`cms.EnsureTopLevelBox`）。整理や社名の候補の試験が
// 取引先・受注・発注・連絡帳の置き場を作るので、その元を置いておきます。
// ID は試験のページと重ならない 0009xx を使います。
func seedBoxTemplates(t *testing.T) {
	t.Helper()
	write := func(id, parent, body string) {
		t.Helper()
		if err := os.MkdirAll(page.GetPageDir(id), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(page.BodyPath(id), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: parent}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(id, body); err != nil {
			t.Fatal(err)
		}
	}
	write("000900", cms.TopPageID, "<h1>"+cms.TemplateRootTitle+"</h1>")
	write("000901", "000900", "<h1>東邦</h1>")
	write("000902", "000901", "<h1>"+CustomerBoxTitle+"</h1>"+cms.ViewMarkerHTML(UnlinkedViewType))
	write("000903", "000901", "<h1>"+OrderBoxTitle+"</h1>"+cms.ViewMarkerHTML(BacklogViewType))
	write("000904", "000901", "<h1>"+PurchaseOrderBoxTitle+"</h1>"+
		cms.ViewMarkerHTML(UnorderedViewType)+cms.ViewMarkerHTML(UnsentOrdersViewType))
	write("000905", "000900", "<h1>通信</h1>")
	write("000906", "000905", "<h1>"+contacts.ContactsBoxTitle+"</h1>"+cms.ViewMarkerHTML(contacts.ContactsViewType))
	// 機械が作るページのテンプレート（2026-09-27〜・templates_test.go）。
	write("000907", "000901", testOrderPageTemplate())
	write("000908", "000901", testPurchaseOrderTemplate())
	write("000909", "000901", testProductTemplate())
	// 社名の下の「加工製品」の箱（2026-09-29・段をやめた）。
	write("000910", "000901", "<h1>"+ProductsBoxTemplate+"</h1>"+cms.ViewMarkerHTML(ProductListViewType))
	// 見積の置き場と見積書（2026-10-01）。
	write("000911", "000901", "<h1>"+EstimateBoxTitle+"</h1>")
	write("000912", "000901", testEstimateTemplate())
}
