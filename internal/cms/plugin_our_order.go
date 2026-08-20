package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

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
		`CREATE TABLE IF NOT EXISTS our_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT UNIQUE,
			supplier_name TEXT,
			pdf_path TEXT,
			page_id INTEGER,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS our_order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT,
			item_name TEXT,
			cost INTEGER,
			quantity INTEGER,
			status TEXT,
			FOREIGN KEY (order_no) REFERENCES our_orders(order_no) ON DELETE CASCADE
		);`,
	}
}

func (ourOrderPlugin) Tables() []string {
	return []string{"our_order_items", "our_orders"}
}

func (ourOrderPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(
		`DELETE FROM our_order_items WHERE order_no IN (SELECT order_no FROM our_orders WHERE page_id = ?)`,
		pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM our_orders WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	insertHeader := func(orderNo, supplierName, pdfPath, orderedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO our_orders (order_no, supplier_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(order_no) DO UPDATE SET
				supplier_name = excluded.supplier_name,
				pdf_path      = excluded.pdf_path,
				page_id       = excluded.page_id,
				ordered_at    = excluded.ordered_at
		`, orderNo, supplierName, pdfPath, pageID, NullableString(orderedAt))
		return err
	}
	insertItem := func(orderNo, itemName string, cost, quantity int, status string) error {
		_, err := tx.Exec(`
			INSERT INTO our_order_items (order_no, item_name, cost, quantity, status)
			VALUES (?, ?, ?, ?, ?)
		`, orderNo, itemName, cost, quantity, status)
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		switch {
		case n.Data == "section" && Attr(n, "data-type") == "our-order": // 論点A・案1
			def, _ := VocabDefByType("our-order")
			itemsDef, _ := VocabDefByType("our-order-items")
			header := VocabDLFields(FirstVocabChild(n, "dl", ""), def)
			orderNo := header["order-no"]
			if err := insertHeader(orderNo, header["supplier-name"], ClosestFileSrc(n), header["ordered-at"]); err != nil {
				firstErr = err
				return
			}
			for _, row := range VocabTableRows(FirstVocabChild(n, "table", "our-order-items"), itemsDef) {
				quantity := 1
				if v := row["quantity"]; v != "" {
					quantity = vocabNumber(v)
				}
				if err := insertItem(orderNo, row["item-name"], vocabNumber(row["cost"]), quantity, row["status"]); err != nil {
					firstErr = err
					return
				}
			}
		}
	})
	return firstErr
}
