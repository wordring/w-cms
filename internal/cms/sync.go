package cms

import (
	"database/sql"
	"path/filepath"
	"w-cms/internal/database"
)

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
func SyncIndex(id string, htmlContent string) error {
	// 手順1: HTML文字列をパースして、各種データを抽出する
	parsed := ParseHTMLMaster(id, htmlContent)

	// 手順2: 物理ファイルの保存先パスを構築
	pageDir := GetPageDir(id)
	filePath := filepath.Join(pageDir, id+".html")

	// 手順3: データベース同期トランザクションを開始
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. pagesテーブルへのupsert
	_, err = tx.Exec(`
		INSERT INTO pages (id, title, parent_id, file_path) 
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			parent_id = excluded.parent_id,
			file_path = excluded.file_path,
			updated_at = CURRENT_TIMESTAMP
	`, parsed.ID, parsed.Title, parsed.ParentID, filePath)
	if err != nil {
		return err
	}

	tablesToDelete := []string{
		"page_tags",
		"client_orders",
		"our_orders",
		"our_estimates",
		"supplier_estimates",
		"part_materials",
	}
	for _, table := range tablesToDelete {
		_, err = tx.Exec("DELETE FROM "+table+" WHERE page_id = ?", parsed.ID)
		if err != nil {
			return err
		}
	}

	// 3. page_tags の挿入
	for _, tag := range parsed.Tags {
		_, err = tx.Exec(`
			INSERT INTO page_tags (page_id, name, value) 
			VALUES (?, ?, ?)
		`, parsed.ID, tag.Name, tag.Value)
		if err != nil {
			return err
		}
	}

	// 4. our_estimates の挿入
	for _, est := range parsed.OurEstimates {
		var estimatedAt sql.NullString
		if est.EstimatedAt != "" {
			estimatedAt.String = est.EstimatedAt
			estimatedAt.Valid = true
		}
		_, err = tx.Exec(`
			INSERT INTO our_estimates (item_id, client_name, price, pdf_path, page_id, estimated_at) 
			VALUES (?, ?, ?, ?, ?, ?)
		`, est.ItemID, est.ClientName, est.Price, est.PDFPath, parsed.ID, estimatedAt)
		if err != nil {
			return err
		}
	}

	// 5. supplier_estimates の挿入
	for _, est := range parsed.SupplierEstimates {
		var estimatedAt sql.NullString
		if est.EstimatedAt != "" {
			estimatedAt.String = est.EstimatedAt
			estimatedAt.Valid = true
		}
		_, err = tx.Exec(`
			INSERT INTO supplier_estimates (item_name, supplier_name, cost, pdf_path, page_id, estimated_at) 
			VALUES (?, ?, ?, ?, ?, ?)
		`, est.ItemName, est.SupplierName, est.Cost, est.PDFPath, parsed.ID, estimatedAt)
		if err != nil {
			return err
		}
	}

	// 6. client_orders と client_order_items の挿入
	for _, order := range parsed.ClientOrders {
		var orderedAt sql.NullString
		if order.OrderedAt != "" {
			orderedAt.String = order.OrderedAt
			orderedAt.Valid = true
		}
		_, err = tx.Exec(`
			INSERT INTO client_orders (order_no, client_name, pdf_path, page_id, ordered_at) 
			VALUES (?, ?, ?, ?, ?)
		`, order.OrderNo, order.ClientName, order.PDFPath, parsed.ID, orderedAt)
		if err != nil {
			return err
		}

		for _, item := range order.Items {
			_, err = tx.Exec(`
				INSERT INTO client_order_items (order_no, item_id, item_name, price, quantity, status) 
				VALUES (?, ?, ?, ?, ?, ?)
			`, order.OrderNo, item.ItemID, item.ItemName, item.Price, item.Quantity, item.Status)
			if err != nil {
				return err
			}
		}
	}

	// 7. our_orders と our_order_items の挿入
	for _, order := range parsed.OurOrders {
		var orderedAt sql.NullString
		if order.OrderedAt != "" {
			orderedAt.String = order.OrderedAt
			orderedAt.Valid = true
		}
		_, err = tx.Exec(`
			INSERT INTO our_orders (order_no, supplier_name, pdf_path, page_id, ordered_at) 
			VALUES (?, ?, ?, ?, ?)
		`, order.OrderNo, order.SupplierName, order.PDFPath, parsed.ID, orderedAt)
		if err != nil {
			return err
		}

		for _, item := range order.Items {
			_, err = tx.Exec(`
				INSERT INTO our_order_items (order_no, item_name, cost, quantity, status) 
				VALUES (?, ?, ?, ?, ?)
			`, order.OrderNo, item.ItemName, item.Cost, item.Quantity, item.Status)
			if err != nil {
				return err
			}
		}
	}

	// 8. part_materials の挿入
	for _, mat := range parsed.Materials {
		_, err = tx.Exec(`
			INSERT INTO part_materials (part_id, material_name, cost, supplier_name, quantity, page_id) 
			VALUES (?, ?, ?, ?, ?, ?)
		`, mat.PartID, mat.MaterialName, mat.Cost, mat.SupplierName, mat.Quantity, parsed.ID)
		if err != nil {
			return err
		}
	}

	// コミット
	return tx.Commit()
}
