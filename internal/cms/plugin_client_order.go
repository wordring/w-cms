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
//       <dl>…<dd data-field="order-no">PO-A100</dd>…</dl>  ← ヘッダ（論点A・案1）
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

	insertHeader := func(orderNo, clientName, pdfPath, orderedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO client_orders (order_no, client_name, pdf_path, page_id, ordered_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(order_no) DO UPDATE SET
				client_name = excluded.client_name,
				pdf_path    = excluded.pdf_path,
				page_id     = excluded.page_id,
				ordered_at  = excluded.ordered_at
		`, orderNo, clientName, pdfPath, pageID, NullableString(orderedAt))
		return err
	}
	insertItem := func(orderNo, itemID, itemName string, price, quantity int, status string) error {
		_, err := tx.Exec(`
			INSERT INTO client_order_items (order_no, item_id, item_name, price, quantity, status)
			VALUES (?, ?, ?, ?, ?, ?)
		`, orderNo, itemID, itemName, price, quantity, status)
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		switch {
		case n.Data == "section" && Attr(n, "data-type") == "client-order": // 論点A・案1
			def, _ := VocabDefByType("client-order")
			itemsDef, _ := VocabDefByType("client-order-items")
			header := VocabDLFields(FirstVocabChild(n, "dl", ""), def)
			orderNo := header["order-no"]
			if err := insertHeader(orderNo, header["client-name"], ClosestFileSrc(n), header["ordered-at"]); err != nil {
				firstErr = err
				return
			}
			for _, row := range VocabTableRows(FirstVocabChild(n, "table", "client-order-items"), itemsDef) {
				quantity := 1 // 空セルの既定（旧 Quantity() と同じ）
				if v := row["quantity"]; v != "" {
					quantity = vocabNumber(v)
				}
				if err := insertItem(orderNo, row["item-id"], row["item-name"],
					vocabNumber(row["price"]), quantity, row["status"]); err != nil {
					firstErr = err
					return
				}
			}
		}
	})
	return firstErr
}
