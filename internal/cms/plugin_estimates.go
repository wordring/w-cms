package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（1プラグインで複数テーブルを所有）: 見積もり
//
//   <m-our-estimate item-id=".." client-name=".." price=".." estimated-at="..">
//       → our_estimates（売上予定）
//   <m-supplier-estimate item-name=".." supplier-name=".." cost=".." estimated-at="..">
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

// Tags は扱うカスタム要素の属性契約。Sync が読む属性は必ず含めること。
// 旧 <m-file tag="..."> の2種類を、意味の異なる2つの要素へ分離した。
func (estimatesPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-our-estimate", Attributes: []string{
			"item-id", "client-name", "price", "estimated-at",
		}},
		{Element: "m-supplier-estimate", Attributes: []string{
			"item-name", "supplier-name", "cost", "estimated-at",
		}},
	}
}

func (estimatesPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM our_estimates WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM supplier_estimates WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		// PDFのパスは容器である <m-file> が持つ（業務要素は単独でも置ける）。
		switch n.Data {
		case "m-our-estimate":
			if _, err := tx.Exec(`
				INSERT INTO our_estimates (item_id, client_name, price, pdf_path, page_id, estimated_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, Attr(n, "item-id"), Attr(n, "client-name"), AtoiSafe(Attr(n, "price")),
				ClosestAttr(n, "m-file", "src"), pageID, NullableString(Attr(n, "estimated-at"))); err != nil {
				firstErr = err
			}
		case "m-supplier-estimate":
			if _, err := tx.Exec(`
				INSERT INTO supplier_estimates (item_name, supplier_name, cost, pdf_path, page_id, estimated_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, Attr(n, "item-name"), Attr(n, "supplier-name"), AtoiSafe(Attr(n, "cost")),
				ClosestAttr(n, "m-file", "src"), pageID, NullableString(Attr(n, "estimated-at"))); err != nil {
				firstErr = err
			}
		}
	})
	return firstErr
}
