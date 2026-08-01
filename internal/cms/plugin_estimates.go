package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（1プラグインで複数テーブルを所有）: 見積もり
//
//   <m-file tag="弊社の見積もり" item-id=".." client-name=".." price=".." estimated-at="..">
//       → our_estimates（売上予定）
//   <m-file tag="材料屋・加工業者の見積もり" item-name=".." supplier-name=".." cost=".." estimated-at="..">
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
// item-name は「材料屋・加工業者の見積もり」で supplier_estimates.item_name として読む
// （過去にこの宣言が無かったため、保存のたびに値が消える不具合になっていた）。
func (estimatesPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-file", Attributes: []string{
			"src", "name", "tag",
			"item-id", "client-name", "price", // 弊社の見積もり
			"item-name", "supplier-name", "cost", // 材料屋・加工業者の見積もり
			"estimated-at",
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
		if firstErr != nil || n.Data != "m-file" {
			return
		}
		switch Attr(n, "tag") {
		case "弊社の見積もり":
			if _, err := tx.Exec(`
				INSERT INTO our_estimates (item_id, client_name, price, pdf_path, page_id, estimated_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, Attr(n, "item-id"), Attr(n, "client-name"), AtoiSafe(Attr(n, "price")),
				Attr(n, "src"), pageID, NullableString(Attr(n, "estimated-at"))); err != nil {
				firstErr = err
			}
		case "材料屋・加工業者の見積もり":
			if _, err := tx.Exec(`
				INSERT INTO supplier_estimates (item_name, supplier_name, cost, pdf_path, page_id, estimated_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, Attr(n, "item-name"), Attr(n, "supplier-name"), AtoiSafe(Attr(n, "cost")),
				Attr(n, "src"), pageID, NullableString(Attr(n, "estimated-at"))); err != nil {
				firstErr = err
			}
		}
	})
	return firstErr
}
