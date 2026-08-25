package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（ヘッダ・明細構造）: 顧客の発注書
//
//   <section data-type="file" data-src="PO-A100.pdf">     ← 任意の容器（PDF原本）
//     <section data-type="client-order">
//       <dl>…<dt>発注書番号</dt><dd>PO-A100</dd>…</dl>  ← ヘッダ（論点A・案1）
//       <table data-type="client-order-items">…</table>    ← 明細
//     </section>
//   </section>
//
//   → client_orders（ヘッダ） 1 : N client_order_items（明細）
//
// 意味は data-type が持つ。容器は任意なので、ファイルの無い受注は業務ブロック
// だけ置ける。PDFのパスは容器側にあるため ClosestFileSrc で祖先から取る。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(clientOrderPlugin{})
}

type clientOrderPlugin struct{}

func (clientOrderPlugin) Name() string { return "client_order" }

func (clientOrderPlugin) Schema() []string {
	return []string{
		// 発注書番号はページ内の識別子であって、サイト全体の主キーではない。
		// 横断UNIQUE だと、同じ番号を使った後勝ちのページが先のページの受注を奪う
		// （番号が空のときは '' 同士が衝突して、空番号の発注書が全域で1件しか持てない）。
		`CREATE TABLE IF NOT EXISTS client_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT,
			client_name TEXT,
			pdf_path TEXT,
			page_id INTEGER,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
			UNIQUE (page_id, order_no)
		);`,
		// 明細も page_id を持つ。order_no だけで親と結ぶと、番号が重複したときに
		// 洗い替えの DELETE が取りこぼし、両ページの明細が同じ番号の下に混ざる。
		`CREATE TABLE IF NOT EXISTS client_order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER,
			order_no TEXT,
			item_id TEXT,
			item_name TEXT,
			price INTEGER,
			quantity INTEGER,
			status TEXT,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}
}

// Tables は子→親の順（全削除時のFK安全な順序）。
func (clientOrderPlugin) Tables() []string {
	return []string{"client_order_items", "client_orders"}
}

// Triggers は顧客の発注書セクションだけを担当することを宣言します。
// 明細表（`client-order-items`）は自分で読むので、引き金には挙げません
// （OnElement が descend=false を返して丸ごと担当する）。
func (clientOrderPlugin) Triggers() []string { return []string{"client-order"} }

// OnPageStart は当該ページ分を洗い流します（洗い替えの前半）。
func (clientOrderPlugin) OnPageStart(ctx *ObserveContext) error {
	return clientOrderPurge(ctx.Tx, ctx.PageID)
}

// clientOrderPurge は当該ページの明細→ヘッダの順で削除します。明細も page_id を
// 持つので直接消せます（order_no のサブクエリだと、番号が重複したときに
// 他ページの明細まで巻き込む）。
func clientOrderPurge(tx *sql.Tx, pageID int) error {
	if _, err := tx.Exec(
		`DELETE FROM client_order_items WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM client_orders WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	return nil
}

// OnElement は1つの発注書セクションを読み、ヘッダと明細を書き込みます。
// 明細表もこのセクションの中から自分で読むので、**子孫へは降りません**
// （降りると ②汎用索引 以外の担当が二重に読むことになる）。
func (clientOrderPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data != "section" { // 論点A・案1: 業務ブロックは section が包む
		return true, nil
	}
	tx, pageID := ctx.Tx, ctx.PageID

	insertHeader := func(orderNo, clientName, pdfPath, orderedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO client_orders (order_no, client_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(page_id, order_no) DO UPDATE SET
				client_name = excluded.client_name,
				pdf_path    = excluded.pdf_path,
				ordered_at  = excluded.ordered_at
		`, orderNo, clientName, pdfPath, pageID, NullableString(orderedAt))
		return err
	}
	insertItem := func(orderNo, itemID, itemName string, price, quantity int, status string) error {
		_, err := tx.Exec(`
			INSERT INTO client_order_items (page_id, order_no, item_id, item_name, price, quantity, status)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, pageID, orderNo, itemID, itemName, price, quantity, status)
		return err
	}

	def, _ := VocabDefByType("client-order")
	itemsDef, _ := VocabDefByType("client-order-items")
	header := VocabDLFields(FirstVocabChild(el, "dl", ""), def)
	orderNo := header["order-no"]
	// PDFのパスは容器 section[data-type="file"] が持つ（祖先から拾う）。
	if err := insertHeader(orderNo, header["client-name"], ClosestFileSrc(el), header["ordered-at"]); err != nil {
		return false, err
	}
	for _, row := range VocabTableRows(FirstVocabChild(el, "table", "client-order-items"), itemsDef) {
		quantity := 1 // 空セルの既定（旧 Quantity() と同じ）
		if v := row["quantity"]; v != "" {
			quantity = vocabNumber(v)
		}
		if err := insertItem(orderNo, row["item-id"], row["item-name"],
			vocabNumber(row["price"]), quantity, row["status"]); err != nil {
			return false, err
		}
	}
	return false, nil
}
