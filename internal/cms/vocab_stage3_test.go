package cms

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// ── 移行第3段: 受発注・見積・容器（論点A=案1） ─────────────────────────

// TestClientOrderFromSection は新形式 <section data-type="client-order"> が
// 汎用索引へ載ることを検証します（ヘッダ dl の項目・明細表・数値の正規化）。
//
// 硬いドメイン表（client_orders / client_order_items）は D-1 で廃したので、
// 行き先は vocab_index だけです。**容器の data-src（旧 pdf_path）は索引しません**
// ——配線＝属性であって表示される値ではなく、読む者もいませんでした。
func TestClientOrderFromSection(t *testing.T) {
	setupSaveTest(t)

	const id = "000050"
	body := `<section data-type="file" data-src="po.pdf"><p>📎 <a href="/data/master/00/000050/po.pdf">発注書.pdf</a></p>` +
		`<section data-type="client-order">` +
		`<dl><dt>発注書番号</dt><dd>PO-A100</dd>` +
		`<dt>発注元</dt><dd>トーア</dd>` +
		`<dt>発注日</dt><dd>2026-06-18</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>SHAFT-01</td><td>シャフト</td><td>¥8,000</td><td>10</td><td>未着手</td></tr>` +
		`<tr><td>GEAR-2</td><td>ギア</td><td>500</td><td></td><td>加工中</td></tr>` +
		`</tbody></table></section></section>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// ヘッダ（包む section の data-type の下に載る）
	if v := vocabValueOf(t, 50, "client-order", "発注書番号"); v != "PO-A100" {
		t.Errorf("発注書番号が索引と異なります: %q", v)
	}
	if v := vocabValueOf(t, 50, "client-order", "発注元"); v != "トーア" {
		t.Errorf("発注元が索引と異なります: %q", v)
	}

	// 明細（生テキストが正本。¥・桁区切りの吸収は norm_num 側で見る）
	if got := strings.Join(vocabValuesOf(t, 50, "client-order-items", "品番"), ","); got != "SHAFT-01,GEAR-2" {
		t.Errorf("明細の品番が文書順と異なります: %q", got)
	}

	rows, err := VocabTableRowsOf(database.DB, 50, "client-order-items")
	if err != nil {
		t.Fatalf("索引の読み出しエラー: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("明細の行数が違います: %d (期待 2)", len(rows))
	}
	// ¥8,000 → 8000（正規化）／数量が空のときは 1（読み取り側の既定）
	if got := rows[0].Num("price"); got != 8000 {
		t.Errorf("単価の正規化が効いていません: %d", got)
	}
	if got := vocabQuantity(rows[0]); got != 10 {
		t.Errorf("数量が期待と異なります: %d", got)
	}
	if got := vocabQuantity(rows[1]); got != 1 {
		t.Errorf("空の数量が既定の1になりません: %d", got)
	}
}

// TestEstimatesFromDL は新形式 <dl data-type="our-estimate"> 等が
// 汎用索引へ載ることを検証します（見積専用テーブルは D-1 で廃止）。
func TestEstimatesFromDL(t *testing.T) {
	setupSaveTest(t)

	const id = "000051"
	body := `<dl data-type="our-estimate"><dt>品番</dt><dd>SHAFT-01</dd>` +
		`<dt>顧客</dt><dd>トーア</dd>` +
		`<dt>見積金額</dt><dd>¥12,000</dd>` +
		`<dt>見積日</dt><dd>2026-06-01</dd></dl>` +
		`<dl data-type="supplier-estimate"><dt>部材名</dt><dd>丸鋼材</dd>` +
		`<dt>仕入先</dt><dd>大同</dd>` +
		`<dt>見積金額</dt><dd>3000</dd></dl>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	if v := vocabValueOf(t, 51, "our-estimate", "品番"); v != "SHAFT-01" {
		t.Errorf("品番が索引に入っていません: %q", v)
	}
	// ¥12,000 → 12000（正規化値は併記され、生テキストはそのまま残る）
	var norm float64
	if err := database.DB.QueryRow(
		`SELECT norm_num FROM vocab_index
		 WHERE page_id = 51 AND data_type = 'our-estimate' AND field = '見積金額'`).Scan(&norm); err != nil {
		t.Fatalf("見積金額の正規化値が引けません: %v", err)
	}
	if norm != 12000 {
		t.Errorf("見積金額の正規化が効いていません: %v", norm)
	}
	if v := vocabValueOf(t, 51, "our-estimate", "見積金額"); v != "¥12,000" {
		t.Errorf("生テキストが書き換えられています: %q", v)
	}

	if v := vocabValueOf(t, 51, "supplier-estimate", "部材名"); v != "丸鋼材" {
		t.Errorf("部材名が索引に入っていません: %q", v)
	}
	if v := vocabValueOf(t, 51, "supplier-estimate", "見積金額"); v != "3000" {
		t.Errorf("見積金額が索引に入っていません: %q", v)
	}
}
