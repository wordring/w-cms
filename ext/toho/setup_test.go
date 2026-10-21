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
// 製造・受注・部材発注・連絡帳の置き場を作るので、その元を置いておきます。
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
		cms.ViewMarkerHTML(UnsentOrdersViewType))
	write("000905", "000900", "<h1>通信</h1>")
	write("000906", "000905", "<h1>"+contacts.ContactsBoxTitle+"</h1>"+cms.ViewMarkerHTML(contacts.ContactsViewType))
	// 機械が作るページのテンプレート（2026-09-27〜・templates_test.go）。
	write("000907", "000901", testOrderPageTemplate())
	write("000908", "000901", testPurchaseOrderTemplate())
	write("000909", "000901", testProductTemplate())
	// 000910 は社名の下の「加工製品」の箱のテンプレートでした（2026-09-29〜10-16。箱をなくしたので置かない）。
	// 見積の置き場と見積書（2026-10-01）。
	write("000911", "000901", "<h1>"+EstimateBoxTitle+"</h1>")
	write("000912", "000901", testEstimateTemplate())
	// 部材見積依頼の置き場と部材見積依頼書（2026-10-03）。
	write("000913", "000901", "<h1>"+RFQBoxTitle+"</h1>")
	write("000914", "000901", testRFQTemplate())
	// 部材見積依頼の返事（2026-10-05・rfq_reply_page.go）。
	write("000915", "000901", testRFQReplyTemplate())
	// 書類の置き場「記録」（2026-10-16・record_box.go——記録／年／月／依頼・製造記録）。
	write("000916", "000901", "<h1>"+RecordBoxTitle+"</h1>")
	// 見積計算書（2026-10-16・estimate_calc_doc.go）。
	write("000917", "000901", "<h1>"+EstimateCalcTemplate+`</h1><dl data-type="tags"><dt>`+OurItemNoTag+`</dt><dd><br/></dd><dt>`+
		EstimateCalcDateTag+`</dt><dd><br/></dd><dt>`+PageKindTag+`</dt><dd>`+PageKindOurEstimateCalc+`</dd></dl>`)
	// 納品書（2026-10-17・delivery.go）。
	write("000918", "000901", "<h1>"+DeliveryTemplate+"</h1>"+kindTagsDL(PageKindOurDelivery, DeliveryNoTag, DeliveryClientTag, DeliveredAtTag, DeliveryIssuedTag)+emptyVocabTable(DeliveryItemsType))
	// 請求書（2026-10-17・invoice.go）。
	write("000919", "000901", "<h1>"+InvoiceTemplate+"</h1>"+kindTagsDL(PageKindOurInvoice, InvoiceNoTag, DeliveryClientTag, InvoiceDateTag, InvoicePeriodTag)+emptyVocabTable(InvoiceItemsType))
	// 製造の置き場と製造ページ（2026-10-19・mfg.go）。
	write("000920", "000901", "<h1>"+MfgBoxTitle+"</h1>")
	write("000921", "000901", "<h1>"+MfgTemplate+"</h1>"+kindTagsDL(PageKindMfg, MfgNoTag, DeliveryClientTag, MfgStartTag)+emptyVocabTable(MfgItemsType))
}
