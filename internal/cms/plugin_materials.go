package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（特殊な値の注入 ＋ 集計API付き）: 部品の構成部材（BOM）
//
//   <table data-type="part-materials"> の行（③計算形式。見出しから機械キー
//   item-name / cost / supplier-name / quantity へ解決。語彙モデル §8.1）
//   部品番号はページ横断メタ（可変タグ）から TagValue で取得
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

func (materialsPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM part_materials WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	// part_id は部材行自身の値ではなく、ページ全体の「部品番号」タグ（可変タグ）から
	// 取得し、ページ内のすべての部材行に一括で付与する。
	partID := TagValue(root, "部品番号")

	insert := func(itemName string, cost int, supplierName string, quantity int) error {
		_, err := tx.Exec(`
			INSERT INTO part_materials (part_id, material_name, cost, supplier_name, quantity, page_id)
			VALUES (?, ?, ?, ?, ?, ?)
		`, partID, itemName, cost, supplierName, quantity, pageID)
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		if n.Data == "table" && Attr(n, "data-type") == "part-materials" {
			firstErr = syncMaterialsTable(n, insert)
		}
	})
	return firstErr
}

// syncMaterialsTable は <table data-type="part-materials"> のデータ行を insert へ流し込みます。
//
// 列の対応は文書自身の見出し行から解決する（語彙モデル §5.1）: 鍵は見出しの表示文字で、
// レジストリ宣言（part-materials の Label）を通じて機械キーへ正規化される。
// 見出しを「単価（税抜）」等へ改名すると解決できなくなり、その列は読まれない
// （保存時に UnresolvedVocabFields が告知する）。
// 数値（cost / quantity）は語彙の正規化（¥・桁区切り・全角の吸収）を通して読む。
// quantity の空セルは 1 として扱う（旧 <m-material> の既定を引き継いだ値）。
func syncMaterialsTable(table *html.Node, insert func(string, int, string, int) error) error {
	def, _ := VocabDefByType("part-materials")
	rows := tableRows(table)
	if len(rows) < 2 {
		return nil // 見出しだけ（またはデータ行なし）
	}

	// 見出し行 → 各列の機械キー（解決できない列は空のまま＝読まない）
	fields := make([]string, 0, 4)
	for _, cell := range rowCells(rows[0]) {
		key := strings.TrimSpace(nodeText(cell))
		field := ""
		if col, ok := def.columnFor(key); ok {
			field = col.Field
		}
		fields = append(fields, field)
	}

	for _, row := range rows[1:] {
		values := map[string]string{}
		for i, cell := range rowCells(row) {
			if i < len(fields) && fields[i] != "" {
				values[fields[i]] = strings.TrimSpace(nodeText(cell))
			}
		}
		quantity := 1 // 空セルの既定（旧 Quantity() と同じ）
		if v := values["quantity"]; v != "" {
			quantity = vocabNumber(v)
		}
		if err := insert(values["item-name"], vocabNumber(values["cost"]), values["supplier-name"], quantity); err != nil {
			return err
		}
	}
	return nil
}

// vocabNumber はセルの数値を語彙の正規化（¥8,000 → 8000 等）を通して整数で返します。
// 解釈できなければ 0（旧 AtoiSafe と同じ安全側）。
func vocabNumber(raw string) int {
	if norm, ok := NormalizeValue(ColNumber, raw); ok {
		return AtoiSafe(norm)
	}
	return AtoiSafe(raw)
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

	list, err := RequiredMaterials(pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// RequiredMaterials は指定ページ（受注ページ）に紐づく部材の要手配数・発注済数を
// 集計します。/api/required-materials と計算ビューのサーバー事前描画
// （view_render.go）が共用する。
func RequiredMaterials(pageIDInt int) ([]RequiredMaterialResponse, error) {
	// 1. そのページ内の受注 client_orders の明細を取得する
	rows, err := database.DB.Query(`
		SELECT item_id, quantity
		FROM client_order_items
		WHERE order_no IN (
			SELECT order_no FROM client_orders WHERE page_id = ?
		)
	`, pageIDInt)
	if err != nil {
		return nil, err
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
			return nil, err
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
		return nil, err
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

	// 表示・応答が呼び出しごとに変わらないよう部材名順に揃える（map の走査順は不定）。
	sort.Slice(list, func(i, j int) bool { return list[i].MaterialName < list[j].MaterialName })
	return list, nil
}
