package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（1プラグインで複数テーブルを所有）: 見積もり
//
//   <dl data-type="our-estimate">…<dt>品番</dt><dd>SHAFT-01</dd>…</dl>
//       → our_estimates（売上予定）
//   <dl data-type="supplier-estimate">…<dt>部材名</dt><dd>丸鋼材</dd>…</dl>
//       → supplier_estimates（原価予定）
//
// どちらも明細を持たないフラットな1行データなので、1つのプラグインで両方を扱います。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(estimatesPlugin{})
}

type estimatesPlugin struct{}

func (estimatesPlugin) Name() string { return "estimates" }

func (estimatesPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS our_estimates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id TEXT,
			client_name TEXT,
			price INTEGER,
			pdf_path TEXT,
			page_id INTEGER,
			estimated_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS supplier_estimates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_name TEXT,
			supplier_name TEXT,
			cost INTEGER,
			pdf_path TEXT,
			page_id INTEGER,
			estimated_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}
}

func (estimatesPlugin) Tables() []string {
	return []string{"our_estimates", "supplier_estimates"}
}

func (estimatesPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM our_estimates WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM supplier_estimates WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	insertOur := func(itemID, clientName string, price int, pdfPath, estimatedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO our_estimates (item_id, client_name, price, pdf_path, page_id, estimated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, itemID, clientName, price, pdfPath, pageID, NullableString(estimatedAt))
		return err
	}
	insertSupplier := func(itemName, supplierName string, cost int, pdfPath, estimatedAt string) error {
		_, err := tx.Exec(`
			INSERT INTO supplier_estimates (item_name, supplier_name, cost, pdf_path, page_id, estimated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, itemName, supplierName, cost, pdfPath, pageID, NullableString(estimatedAt))
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		// PDFのパスは容器 section[data-type="file"] が持つ。
		switch {
		case n.Data == "dl" && Attr(n, "data-type") == "our-estimate":
			def, _ := VocabDefByType("our-estimate")
			f := VocabDLFields(n, def)
			firstErr = insertOur(f["item-id"], f["client-name"], vocabNumber(f["price"]), ClosestFileSrc(n), f["estimated-at"])
		case n.Data == "dl" && Attr(n, "data-type") == "supplier-estimate":
			def, _ := VocabDefByType("supplier-estimate")
			f := VocabDLFields(n, def)
			firstErr = insertSupplier(f["item-name"], f["supplier-name"], vocabNumber(f["cost"]), ClosestFileSrc(n), f["estimated-at"])
		}
	})
	return firstErr
}
