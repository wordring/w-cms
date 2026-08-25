package cms

import "golang.org/x/net/html"

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（ヘッダ・明細構造）: 弊社の発注書（材料購入・外注加工）
//
//   <section data-type="our-order">
//     <dl>…<dt>発注書番号</dt><dd>PO-OUR-1</dd>…</dl>  ← ヘッダ（論点A・案1）
//     <table data-type="our-order-items">…</table>        ← 明細
//   </section>
//   （容器 section[data-type="file"] の中に置いてもよい。
//     PDFのパスは容器から ClosestFileSrc で拾う）
//
//   → our_orders（ヘッダ） 1 : N our_order_items（明細）
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(ourOrderPlugin{})
}

type ourOrderPlugin struct{}

func (ourOrderPlugin) Name() string { return "our_order" }

func (ourOrderPlugin) Schema() []string {
	return []string{
		// 発注書番号はページ内の識別子（client_orders と同じ理由。設計総点検③）。
		`CREATE TABLE IF NOT EXISTS our_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT,
			supplier_name TEXT,
			pdf_path TEXT,
			page_id INTEGER,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
			UNIQUE (page_id, order_no)
		);`,
		`CREATE TABLE IF NOT EXISTS our_order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER,
			order_no TEXT,
			item_name TEXT,
			cost INTEGER,
			quantity INTEGER,
			status TEXT,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}
}

func (ourOrderPlugin) Tables() []string {
	return []string{"our_order_items", "our_orders"}
}

// Triggers は自社の発注書セクションだけを担当することを宣言します。
func (ourOrderPlugin) Triggers() []string { return []string{"our-order"} }

// OnPageStart は当該ページ分を洗い流します。
// 明細も page_id を持つので直接消せます（order_no のサブクエリだと、番号が重複した
// ときに他ページの明細まで巻き込む）。
func (ourOrderPlugin) OnPageStart(ctx *ObserveContext) error {
	if _, err := ctx.Tx.Exec(
		`DELETE FROM our_order_items WHERE page_id = ?`, ctx.PageID); err != nil {
		return err
	}
	_, err := ctx.Tx.Exec(`DELETE FROM our_orders WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement は1つの発注書セクションを読み、ヘッダと明細を書き込みます。
func (ourOrderPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data != "section" { // 論点A・案1: 業務ブロックは section が包む
		return true, nil
	}
	tx, pageID := ctx.Tx, ctx.PageID

	insertHeader := func(orderNo, supplierName, pdfPath, orderedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO our_orders (order_no, supplier_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(page_id, order_no) DO UPDATE SET
				supplier_name = excluded.supplier_name,
				pdf_path      = excluded.pdf_path,
				ordered_at    = excluded.ordered_at
		`, orderNo, supplierName, pdfPath, pageID, NullableString(orderedAt))
		return err
	}
	insertItem := func(orderNo, itemName string, cost, quantity int, status string) error {
		_, err := tx.Exec(`
			INSERT INTO our_order_items (page_id, order_no, item_name, cost, quantity, status)
			VALUES (?, ?, ?, ?, ?, ?)
		`, pageID, orderNo, itemName, cost, quantity, status)
		return err
	}

	def, _ := VocabDefByType("our-order")
	itemsDef, _ := VocabDefByType("our-order-items")
	header := VocabDLFields(FirstVocabChild(el, "dl", ""), def)
	orderNo := header["order-no"]
	// PDFのパスは容器 section[data-type="file"] が持つ（祖先から拾う）。
	if err := insertHeader(orderNo, header["supplier-name"], ClosestFileSrc(el), header["ordered-at"]); err != nil {
		return false, err
	}
	for _, row := range VocabTableRows(FirstVocabChild(el, "table", "our-order-items"), itemsDef) {
		quantity := 1
		if v := row["quantity"]; v != "" {
			quantity = vocabNumber(v)
		}
		if err := insertItem(orderNo, row["item-name"], vocabNumber(row["cost"]), quantity, row["status"]); err != nil {
			return false, err
		}
	}
	return false, nil
}
