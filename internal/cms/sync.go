package cms

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/database"
)

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
func SyncIndex(id string, htmlContent string) error {
	// 手順1: HTML文字列をパースして、各種データを抽出する
	parsed := ParseHTMLMaster(id, htmlContent)

	// 手順2: 物理ファイルの保存先パスを構築
	pageDir := GetPageDir(id)
	filePath := filepath.Join(pageDir, id+".html")

	// 整数型への変換
	pageIDInt, err := strconv.Atoi(parsed.ID)
	if err != nil {
		return err
	}

	var parentIDInt sql.NullInt64
	if parsed.ParentID != "" {
		pid, err := strconv.Atoi(parsed.ParentID)
		if err == nil {
			parentIDInt.Int64 = int64(pid)
			parentIDInt.Valid = true
		}
	}

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
	`, pageIDInt, parsed.Title, parentIDInt, filePath)
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
		_, err = tx.Exec("DELETE FROM "+table+" WHERE page_id = ?", pageIDInt)
		if err != nil {
			return err
		}
	}

	// 3. page_tags の挿入
	for _, tag := range parsed.Tags {
		_, err = tx.Exec(`
			INSERT INTO page_tags (page_id, name, value) 
			VALUES (?, ?, ?)
		`, pageIDInt, tag.Name, tag.Value)
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
		`, est.ItemID, est.ClientName, est.Price, est.PDFPath, pageIDInt, estimatedAt)
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
		`, est.ItemName, est.SupplierName, est.Cost, est.PDFPath, pageIDInt, estimatedAt)
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
			ON CONFLICT(order_no) DO UPDATE SET
				client_name = excluded.client_name,
				pdf_path = excluded.pdf_path,
				page_id = excluded.page_id,
				ordered_at = excluded.ordered_at
		`, order.OrderNo, order.ClientName, order.PDFPath, pageIDInt, orderedAt)
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
			ON CONFLICT(order_no) DO UPDATE SET
				supplier_name = excluded.supplier_name,
				pdf_path = excluded.pdf_path,
				page_id = excluded.page_id,
				ordered_at = excluded.ordered_at
		`, order.OrderNo, order.SupplierName, order.PDFPath, pageIDInt, orderedAt)
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
		`, mat.PartID, mat.MaterialName, mat.Cost, mat.SupplierName, mat.Quantity, pageIDInt)
		if err != nil {
			return err
		}
	}

	// コミット
	return tx.Commit()
}

// RebuildDatabase は、HTMLファイル群（data/master配下）を正として、データベースのインデックスを完全に再構築します。
func RebuildDatabase() error {
	// 1. すべてのテーブルのデータを消去
	tables := []string{
		"part_materials",
		"our_order_items",
		"our_orders",
		"client_order_items",
		"client_orders",
		"page_tags",
		"pages",
	}
	
	// 外部キー制約を一時的に無効化して削除（必要に応じて）するか、子から順番に削除する。
	// ここでは子テーブルから順番に削除する安全なアプローチをとります。
	for _, table := range tables {
		_, err := database.DB.Exec("DELETE FROM " + table)
		if err != nil {
			return err
		}
	}

	// 2. data/master 以下のすべての .html ファイルを探索して SyncIndex を実行
	err := filepath.Walk(MasterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			id := strings.TrimSuffix(info.Name(), ".html")
			// 再度パースしてUPSERTを行う
			err = SyncIndex(id, string(content))
			if err != nil {
				// エラーが出ても他のファイルのパースは継続する
				return nil
			}
		}
		return nil
	})

	return err
}
