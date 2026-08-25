package cms

import "golang.org/x/net/html"

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

// Triggers は販売見積・購入見積の2つを担当することを宣言します
// （1つの観察係が複数の引き金を持つ例）。
func (estimatesPlugin) Triggers() []string {
	return []string{"our-estimate", "supplier-estimate"}
}

// OnPageStart は当該ページ分を洗い流します。
func (estimatesPlugin) OnPageStart(ctx *ObserveContext) error {
	if _, err := ctx.Tx.Exec(`DELETE FROM our_estimates WHERE page_id = ?`, ctx.PageID); err != nil {
		return err
	}
	_, err := ctx.Tx.Exec(`DELETE FROM supplier_estimates WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement は見積もりの定義リストを読みます。
// PDFのパスは容器 section[data-type="file"] が持つので祖先から拾います。
func (estimatesPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data != "dl" {
		return true, nil
	}
	tx, pageID := ctx.Tx, ctx.PageID

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

	switch Attr(el, "data-type") {
	case "our-estimate":
		def, _ := VocabDefByType("our-estimate")
		f := VocabDLFields(el, def)
		return false, insertOur(f["item-id"], f["client-name"], vocabNumber(f["price"]),
			ClosestFileSrc(el), f["estimated-at"])
	case "supplier-estimate":
		def, _ := VocabDefByType("supplier-estimate")
		f := VocabDLFields(el, def)
		return false, insertSupplier(f["item-name"], f["supplier-name"], vocabNumber(f["cost"]),
			ClosestFileSrc(el), f["estimated-at"])
	}
	return true, nil
}
