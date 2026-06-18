package database

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // WindowsでもCコンパイラ不要で動くPure Goドライバ
)

// DB はアプリケーション全体で共有されるデータベース接続のインスタンスです。
var DB *sql.DB

// InitDB はデータ保存用フォルダの確保と、SQLiteデータベースの初期化・テーブル作成を行います。
func InitDB() error {
	// 手順1: 物理ファイル群を保存する "data" ディレクトリを作成する
	if err := os.MkdirAll("data", 0755); err != nil {
		return err
	}

	// 手順2: データベースファイル (data/cms.db) への接続を開く
	dbPath := filepath.Join("data", "cms.db")
	var err error
	DB, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}

	// SQLite の外部キー（Foreign Key）制約を有効化
	if _, err = DB.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return err
	}

	// 手順3: データベーススキーマ（各種テーブル）の作成
	queries := []string{
		// 1. ドキュメントの基本インデックス情報（本文はファイル保存）
		`CREATE TABLE IF NOT EXISTS pages (
			id TEXT PRIMARY KEY,
			title TEXT,
			parent_id TEXT DEFAULT '',
			file_path TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// 2. 可変タグテーブル（名前：値 の属性情報）
		`CREATE TABLE IF NOT EXISTS page_tags (
			page_id TEXT,
			name TEXT,
			value TEXT,
			PRIMARY KEY (page_id, name),
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,

		// 3. 弊社の見積もりデータ
		`CREATE TABLE IF NOT EXISTS our_estimates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id TEXT,
			client_name TEXT,
			price INTEGER,
			pdf_path TEXT,
			page_id TEXT,
			estimated_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,

		// 4. 顧客の発注書データ (Header)
		`CREATE TABLE IF NOT EXISTS client_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT UNIQUE,
			client_name TEXT,
			pdf_path TEXT,
			page_id TEXT,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,

		// 4-2. 顧客の発注部品明細 (Items)
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

		// 5. 材料屋・加工業者の見積もりデータ
		`CREATE TABLE IF NOT EXISTS supplier_estimates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_name TEXT,
			supplier_name TEXT,
			cost INTEGER,
			pdf_path TEXT,
			page_id TEXT,
			estimated_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,

		// 6. 弊社の発注書データ (Header)
		`CREATE TABLE IF NOT EXISTS our_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT UNIQUE,
			supplier_name TEXT,
			pdf_path TEXT,
			page_id TEXT,
			ordered_at DATE,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,

		// 6-2. 弊社の発注部品明細 (Items)
		`CREATE TABLE IF NOT EXISTS our_order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_no TEXT,
			item_name TEXT,
			cost INTEGER,
			quantity INTEGER,
			status TEXT,
			FOREIGN KEY (order_no) REFERENCES our_orders(order_no) ON DELETE CASCADE
		);`,

		// 7. 部品構成・材料構成データ (マスタ情報)
		`CREATE TABLE IF NOT EXISTS part_materials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id TEXT,
			material_name TEXT,
			cost INTEGER,
			supplier_name TEXT,
			quantity INTEGER,
			page_id TEXT,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}

	for _, q := range queries {
		if _, err = DB.Exec(q); err != nil {
			return err
		}
	}

	// 既存DBにparent_idを追加するマイグレーション
	DB.Exec(`ALTER TABLE pages ADD COLUMN parent_id TEXT DEFAULT '';`)

	return nil
}
