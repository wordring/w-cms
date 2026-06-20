package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（ヘッダ・明細構造）: 弊社の発注書（材料購入・外注加工）
//
//   <m-file tag="弊社の発注書" order-no=".." supplier-name=".." ordered-at="..">
//       <m-item item-name=".." cost=".." quantity=".." status="..">
//   </m-file>
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

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || n.Data != "m-file" || Attr(n, "tag") != "弊社の発注書" {
			return
		}
		orderNo := Attr(n, "order-no")
		if _, err := tx.Exec(`
			INSERT INTO our_orders (order_no, supplier_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(order_no) DO UPDATE SET
				supplier_name = excluded.supplier_name,
				pdf_path      = excluded.pdf_path,
				page_id       = excluded.page_id,
				ordered_at    = excluded.ordered_at
		`, orderNo, Attr(n, "supplier-name"), Attr(n, "src"), pageID, NullableString(Attr(n, "ordered-at"))); err != nil {
			firstErr = err
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "m-item" {
				continue
			}
			if _, err := tx.Exec(`
				INSERT INTO our_order_items (order_no, item_name, cost, quantity, status)
				VALUES (?, ?, ?, ?, ?)
			`, orderNo, Attr(c, "item-name"), AtoiSafe(Attr(c, "cost")), Quantity(c), Attr(c, "status")); err != nil {
				firstErr = err
				return
			}
		}
	})
	return firstErr
}
