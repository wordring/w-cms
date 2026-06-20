package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（ヘッダ・明細構造）: 顧客の発注書
//
//   <m-file tag="顧客の発注書" order-no=".." client-name=".." ordered-at="..">
//       <m-item item-id=".." item-name=".." price=".." quantity=".." status="..">
//   </m-file>
//
//   → client_orders（ヘッダ） 1 : N client_order_items（明細）
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(clientOrderPlugin{})
}

type clientOrderPlugin struct{}

func (clientOrderPlugin) Name() string { return "client_order" }

func (clientOrderPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS client_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT UNIQUE,
			client_name TEXT,
			pdf_path TEXT,
			page_id INTEGER,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS client_order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT,
			item_id TEXT,
			item_name TEXT,
			price INTEGER,
			quantity INTEGER,
			status TEXT,
			FOREIGN KEY (order_no) REFERENCES client_orders(order_no) ON DELETE CASCADE
		);`,
	}
}

// Tables は子→親の順（全削除時のFK安全な順序）。
func (clientOrderPlugin) Tables() []string {
	return []string{"client_order_items", "client_orders"}
}

func (clientOrderPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	// 洗い替え: 当該ページの明細→ヘッダの順で削除（明細にはpage_idが無いためサブクエリ）。
	if _, err := tx.Exec(
		`DELETE FROM client_order_items WHERE order_no IN (SELECT order_no FROM client_orders WHERE page_id = ?)`,
		pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM client_orders WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || n.Data != "m-file" || Attr(n, "tag") != "顧客の発注書" {
			return
		}
		orderNo := Attr(n, "order-no")
		if _, err := tx.Exec(`
			INSERT INTO client_orders (order_no, client_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(order_no) DO UPDATE SET
				client_name = excluded.client_name,
				pdf_path    = excluded.pdf_path,
				page_id     = excluded.page_id,
				ordered_at  = excluded.ordered_at
		`, orderNo, Attr(n, "client-name"), Attr(n, "src"), pageID, NullableString(Attr(n, "ordered-at"))); err != nil {
			firstErr = err
			return
		}
		// 明細（直下の <m-item>）を挿入
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "m-item" {
				continue
			}
			if _, err := tx.Exec(`
				INSERT INTO client_order_items (order_no, item_id, item_name, price, quantity, status)
				VALUES (?, ?, ?, ?, ?, ?)
			`, orderNo, Attr(c, "item-id"), Attr(c, "item-name"), AtoiSafe(Attr(c, "price")), Quantity(c), Attr(c, "status")); err != nil {
				firstErr = err
				return
			}
		}
	})
	return firstErr
}
