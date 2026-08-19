package cms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ── 移行第3段: 受発注・見積・容器（論点A=案1） ─────────────────────────

// TestClientOrderFromSection は新形式 <section data-type="client-order"> が
// client_orders / client_order_items へ同期されることを検証します
// （ヘッダ dl の data-field・明細表・容器からの pdf_path・数値の正規化）。
func TestClientOrderFromSection(t *testing.T) {
	setupSaveTest(t)

	const id = "000050"
	body := `<section data-type="file" data-src="po.pdf"><p>📎 <a href="/data/master/00/000050/po.pdf">発注書.pdf</a></p>` +
		`<section data-type="client-order">` +
		`<dl><dt>発注書番号</dt><dd data-field="order-no">PO-A100</dd>` +
		`<dt>発注元</dt><dd data-field="client-name">トーア</dd>` +
		`<dt>発注日</dt><dd data-field="ordered-at">2026-06-18</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th data-field="item-id">品番</th><th data-field="item-name">品名</th><th data-field="price">単価</th><th data-field="quantity">数量</th><th data-field="status">状態</th></tr>` +
		`<tr><td>SHAFT-01</td><td>シャフト</td><td>¥8,000</td><td>10</td><td>未着手</td></tr>` +
		`<tr><td>GEAR-2</td><td>ギア</td><td>500</td><td></td><td>加工中</td></tr>` +
		`</tbody></table></section></section>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var clientName, pdfPath string
	if err := database.DB.QueryRow(
		`SELECT client_name, pdf_path FROM client_orders WHERE order_no = 'PO-A100'`).
		Scan(&clientName, &pdfPath); err != nil {
		t.Fatalf("client_orders に入っていません: %v", err)
	}
	if clientName != "トーア" || pdfPath != "po.pdf" {
		t.Errorf("ヘッダの値が期待と異なります: %q %q", clientName, pdfPath)
	}

	rows, _ := database.DB.Query(
		`SELECT item_id, price, quantity FROM client_order_items WHERE order_no = 'PO-A100' ORDER BY item_id`)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var itemID string
		var price, qty int
		rows.Scan(&itemID, &price, &qty)
		got = append(got, itemID+"|"+itoa(price)+"|"+itoa(qty))
	}
	want := "GEAR-2|500|1\nSHAFT-01|8000|10" // ¥8,000→8000・数量空→1
	if strings.Join(got, "\n") != want {
		t.Errorf("明細が期待と異なります:\ngot  %v\nwant %v", got, want)
	}
}

// TestEstimatesFromDL は新形式 <dl data-type="our-estimate"> 等が
// 見積テーブルへ同期されることを検証します。
func TestEstimatesFromDL(t *testing.T) {
	setupSaveTest(t)

	const id = "000051"
	body := `<dl data-type="our-estimate"><dt>品番</dt><dd data-field="item-id">SHAFT-01</dd>` +
		`<dt>顧客</dt><dd data-field="client-name">トーア</dd>` +
		`<dt>見積金額</dt><dd data-field="price">¥12,000</dd>` +
		`<dt>見積日</dt><dd data-field="estimated-at">2026-06-01</dd></dl>` +
		`<dl data-type="supplier-estimate"><dt>部材名</dt><dd data-field="item-name">丸鋼材</dd>` +
		`<dt>仕入先</dt><dd data-field="supplier-name">大同</dd>` +
		`<dt>見積金額</dt><dd data-field="cost">3000</dd></dl>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var price int
	if err := database.DB.QueryRow(
		`SELECT price FROM our_estimates WHERE item_id = 'SHAFT-01' AND page_id = 51`).Scan(&price); err != nil {
		t.Fatalf("our_estimates に入っていません: %v", err)
	}
	if price != 12000 {
		t.Errorf("見積金額の正規化が効いていません: %d", price)
	}
	var cost int
	if err := database.DB.QueryRow(
		`SELECT cost FROM supplier_estimates WHERE item_name = '丸鋼材' AND page_id = 51`).Scan(&cost); err != nil {
		t.Fatalf("supplier_estimates に入っていません: %v", err)
	}
	if cost != 3000 {
		t.Errorf("原価が期待と異なります: %d", cost)
	}
}

// TestMigrateStage3Conversion は受発注・見積・容器を含む旧形式ページの一括変換を
// 検証します——旧要素は変換されるまで索引に乗らず（読み取りの保険は除去済み）、
// 変換後は旧 Sync と同じ抽出結果が4テーブルに入り、正本は新形式の外形になる。
func TestMigrateStage3Conversion(t *testing.T) {
	setupSaveTest(t)

	const id = "000052"
	body := `<h1>受注ページ</h1>` +
		`<m-file src="po.pdf" name="発注書.pdf">` +
		`<m-client-order order-no="PO-B200" client-name="トーア" ordered-at="2026-06-18">` +
		`<m-item item-id="SHAFT-01" item-name="シャフト" price="8000" quantity="10" status="未着手"></m-item>` +
		`<m-item item-id="GEAR-2" item-name="ギア" price="500" status="加工中"></m-item>` +
		`</m-client-order></m-file>` +
		`<m-supplier-order order-no="PO-OUR-1" supplier-name="大同" ordered-at="2026-06-20">` +
		`<m-item item-name="丸鋼材" cost="3000" quantity="5" status="未納品"></m-item>` +
		`</m-supplier-order>` +
		`<m-our-estimate item-id="SHAFT-01" client-name="トーア" price="12000" estimated-at="2026-06-01"></m-our-estimate>` +
		`<m-supplier-estimate item-name="丸鋼材" supplier-name="大同" cost="3000"></m-supplier-estimate>`

	postSave(t, id, body)

	// 旧要素は索引に乗らない（保険の除去を固定）
	var before int
	database.DB.QueryRow(`SELECT COUNT(*) FROM client_orders WHERE page_id = 52`).Scan(&before)
	if before != 0 {
		t.Fatalf("変換前の旧要素が索引に乗っています: %d件", before)
	}

	converted, _, err := MigrateVocab()
	if err != nil {
		t.Fatalf("MigrateVocabエラー: %v", err)
	}
	if converted != 1 {
		t.Errorf("変換ページ数: got %d want 1", converted)
	}

	// 変換後の抽出結果（旧 Sync が返していた値と同じ）
	var clientName, pdfPath string
	if err := database.DB.QueryRow(
		`SELECT client_name, pdf_path FROM client_orders WHERE order_no = 'PO-B200'`).
		Scan(&clientName, &pdfPath); err != nil {
		t.Fatalf("client_orders に入っていません: %v", err)
	}
	if clientName != "トーア" || pdfPath != "po.pdf" {
		t.Errorf("受注ヘッダが期待と異なります: %q %q", clientName, pdfPath)
	}
	rows, _ := database.DB.Query(
		`SELECT item_id, price, quantity FROM client_order_items WHERE order_no = 'PO-B200' ORDER BY item_id`)
	defer rows.Close()
	var items []string
	for rows.Next() {
		var itemID string
		var price, qty int
		rows.Scan(&itemID, &price, &qty)
		items = append(items, itemID+"|"+itoa(price)+"|"+itoa(qty))
	}
	// quantity 省略は旧既定の 1
	if want := "GEAR-2|500|1\nSHAFT-01|8000|10"; strings.Join(items, "\n") != want {
		t.Errorf("受注明細が期待と異なります:\ngot  %v\nwant %v", items, want)
	}
	var cost, qty int
	if err := database.DB.QueryRow(
		`SELECT cost, quantity FROM our_order_items WHERE order_no = 'PO-OUR-1'`).Scan(&cost, &qty); err != nil {
		t.Fatalf("our_order_items に入っていません: %v", err)
	}
	if cost != 3000 || qty != 5 {
		t.Errorf("発注明細が期待と異なります: cost=%d qty=%d", cost, qty)
	}
	var price int
	if err := database.DB.QueryRow(
		`SELECT price FROM our_estimates WHERE page_id = 52`).Scan(&price); err != nil {
		t.Fatalf("our_estimates に入っていません: %v", err)
	}
	if price != 12000 {
		t.Errorf("見積金額が期待と異なります: %d", price)
	}
	var sCost int
	if err := database.DB.QueryRow(
		`SELECT cost FROM supplier_estimates WHERE page_id = 52`).Scan(&sCost); err != nil {
		t.Fatalf("supplier_estimates に入っていません: %v", err)
	}
	if sCost != 3000 {
		t.Errorf("原価が期待と異なります: %d", sCost)
	}

	// 変換後の正本の形（容器→リンク・受発注→section・見積→dl）
	raw, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("正本の読み込みエラー: %v", err)
	}
	content := string(raw)
	for _, want := range []string{
		`data-type="file"`, `data-src="po.pdf"`, `href="/data/master/00/000052/po.pdf"`,
		`data-type="client-order"`, `data-field="order-no"`, `data-type="client-order-items"`,
		`data-type="our-order"`, `data-type="our-estimate"`, `data-type="supplier-estimate"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("変換後の正本に %q がありません", want)
		}
	}
	if strings.Contains(content, "<m-") {
		t.Errorf("旧要素が残っています:\n%s", content)
	}
}

