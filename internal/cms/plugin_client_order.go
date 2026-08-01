package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（ヘッダ・明細構造）: 顧客の発注書
//
//   <m-file src="PO-A100.pdf" name="顧客発注書.pdf">      ← 任意の容器（PDF原本）
//     <m-client-order order-no=".." client-name=".." ordered-at="..">
//       <m-item item-id=".." item-name=".." price=".." quantity=".." status="..">
//     </m-client-order>
//   </m-file>
//
//   → client_orders（ヘッダ） 1 : N client_order_items（明細）
//
// 意味は要素そのものが持つ（旧: <m-file tag="顧客の発注書">）。tag 文字列による
// 分岐は廃止した。<m-file> は任意なので、ファイルの無い受注は業務要素だけ置ける。
// PDFのパスは容器側にあるため ClosestAttr で親の m-file から取る。
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

// Tags は扱うカスタム要素の属性契約。Sync が読む属性は必ず含めること。
func (clientOrderPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-client-order", Attributes: []string{
			"order-no", "client-name", "ordered-at",
		}},
		{Element: "m-item", Attributes: []string{
			"item-id", "item-name", "price", "quantity", "status",
		}},
	}
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
		if firstErr != nil || n.Data != "m-client-order" {
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
		`, orderNo, Attr(n, "client-name"), ClosestAttr(n, "m-file", "src"), pageID, NullableString(Attr(n, "ordered-at"))); err != nil {
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
