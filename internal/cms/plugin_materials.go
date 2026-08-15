package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（特殊な値の注入 ＋ 集計API付き）: 部品の構成部材（BOM）
//
//   <m-tag name="部品番号" value="SHAFT-01">      ← ページ全体に効く「部品番号」
//   <m-material item-name=".." cost=".." supplier-name=".." quantity="..">
//
//   → part_materials（part_id はページの「部品番号」タグから全行に注入）
//
// さらに RouteProvider を実装し、GET /api/required-materials（部材手配計算API）を
// 提供します（Tier 2: 集計ロジックはコードプラグインとして持つ）。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(materialsPlugin{})
}

type materialsPlugin struct{}

func (materialsPlugin) Name() string { return "materials" }

func (materialsPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS part_materials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id TEXT,
			material_name TEXT,
			cost INTEGER,
			supplier_name TEXT,
			quantity INTEGER,
			page_id INTEGER,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}
}

func (materialsPlugin) Tables() []string {
	return []string{"part_materials"}
}

// Tags は扱うカスタム要素の属性契約。
// m-required-materials は同期先テーブルを持たない表示専用要素だが、page-id は
// フロントが集計APIを呼ぶのに必要なので保存されなければならない（属性契約に含める）。
func (materialsPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-material", Attributes: []string{
			"item-name", "cost", "supplier-name", "quantity",
		}},
		{Element: "m-required-materials", Attributes: []string{"page-id"}},
	}
}

func (materialsPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM part_materials WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	// part_id は <m-material> 自身の属性ではなく、ページ全体の「部品番号」タグから取得し、
	// ページ内のすべての部材行に一括で付与する。
	partID := TagValue(root, "部品番号")

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || n.Data != "m-material" {
			return
		}
		if _, err := tx.Exec(`
			INSERT INTO part_materials (part_id, material_name, cost, supplier_name, quantity, page_id)
			VALUES (?, ?, ?, ?, ?, ?)
		`, partID, Attr(n, "item-name"), AtoiSafe(Attr(n, "cost")), Attr(n, "supplier-name"), Quantity(n), pageID); err != nil {
			firstErr = err
		}
	})
	return firstErr
}

// Routes は部材手配計算APIのエンドポイントを提供します（RouteProvider実装）。
func (materialsPlugin) Routes() []Route {
	return []Route{
		{Pattern: "/api/required-materials", Handler: RequiredMaterialsAPIHandler},
	}
}

// RequiredMaterialResponse は部材手配の進捗状況を返却するためのJSON構造体です。
type RequiredMaterialResponse struct {
	MaterialName  string `json:"material_name"`
	SupplierName  string `json:"supplier_name"`
	Cost          int    `json:"cost"`
	TotalRequired int    `json:"total_required"`
	Ordered       int    `json:"ordered"`
	Remaining     int    `json:"remaining"`
}

// RequiredMaterialsAPIHandler は指定されたpage_id(受注ページ)に紐づく部材の
// 要手配数・発注済数を集計して返却します。
func RequiredMaterialsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pageID := r.URL.Query().Get("page_id")
	if pageID == "" {
		http.Error(w, "Missing page_id parameter", http.StatusBadRequest)
		return
	}
	// 集計対象ページの read 権限を要求する
	if !page.RequirePageRead(w, r, pageID) {
		return
	}

	pageIDInt, err := strconv.Atoi(pageID)
	if err != nil {
		http.Error(w, "Invalid page_id format", http.StatusBadRequest)
		return
	}

	// 1. そのページ内の受注 client_orders の明細を取得する
	rows, err := database.DB.Query(`
		SELECT item_id, quantity
		FROM client_order_items
		WHERE order_no IN (
			SELECT order_no FROM client_orders WHERE page_id = ?
		)
	`, pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type orderItem struct {
		ItemID   string
		Quantity int
	}
	var clientItems []orderItem
	for rows.Next() {
		var item orderItem
		if err := rows.Scan(&item.ItemID, &item.Quantity); err == nil {
			clientItems = append(clientItems, item)
		}
	}

	// 2. 各受注部品に対し、必要な部材の定義 part_materials を取得し、総必要数を集計する
	materialsMap := make(map[string]*RequiredMaterialResponse)

	for _, item := range clientItems {
		matRows, err := database.DB.Query(`
			SELECT material_name, cost, supplier_name, quantity
			FROM part_materials
			WHERE part_id = ?
		`, item.ItemID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for matRows.Next() {
			var matName, supplierName string
			var cost, unitQty int
			if err := matRows.Scan(&matName, &cost, &supplierName, &unitQty); err == nil {
				totalReq := unitQty * item.Quantity
				if existing, ok := materialsMap[matName]; ok {
					existing.TotalRequired += totalReq
				} else {
					materialsMap[matName] = &RequiredMaterialResponse{
						MaterialName:  matName,
						SupplierName:  supplierName,
						Cost:          cost,
						TotalRequired: totalReq,
						Ordered:       0,
					}
				}
			}
		}
		matRows.Close()
	}

	// 3. 同じ page_id に紐づく弊社の発注実績 our_orders の明細を取得し、発注済数を集計する
	ourRows, err := database.DB.Query(`
		SELECT ooi.item_name, ooi.quantity, oo.supplier_name
		FROM our_order_items ooi
		JOIN our_orders oo ON ooi.order_no = oo.order_no
		WHERE oo.page_id = ?
	`, pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer ourRows.Close()

	for ourRows.Next() {
		var itemName, supplierName string
		var quantity int
		if err := ourRows.Scan(&itemName, &quantity, &supplierName); err == nil {
			if existing, ok := materialsMap[itemName]; ok {
				existing.Ordered += quantity
			} else {
				materialsMap[itemName] = &RequiredMaterialResponse{
					MaterialName:  itemName,
					SupplierName:  supplierName,
					Cost:          0,
					TotalRequired: 0,
					Ordered:       quantity,
				}
			}
		}
	}

	// 4. 残要注文数を算出し、スライスに変換
	list := make([]RequiredMaterialResponse, 0)
	for _, m := range materialsMap {
		m.Remaining = m.TotalRequired - m.Ordered
		if m.Remaining < 0 {
			m.Remaining = 0
		}
		list = append(list, *m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
