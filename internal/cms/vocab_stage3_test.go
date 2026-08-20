package cms

import (
	"strings"
	"testing"

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
