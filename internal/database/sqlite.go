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

	// 手順3: コアテーブル（pages / page_tags）を作成する。
	// ユースケース固有のテーブル（発注書・見積もり・部材など）は、
	// internal/cms の各プラグインが Schema() で定義し、main から cms.ApplySchema() で作成する。
	return CreateCoreTables(DB)
}

// CoreTables はどのユースケースにも共通する基盤テーブル（pages / page_tags）の定義です。
// pages は全ドキュメントの基本情報、page_tags は <m-tag> の可変属性を保持します。
// これらは外部キーの参照先となるため、プラグインのテーブルより先に作成する必要があります。
var CoreTables = []string{
	// 1. ドキュメントの基本インデックス情報（本文はファイル保存）
	`CREATE TABLE IF NOT EXISTS pages (
		id INTEGER PRIMARY KEY,
		title TEXT,
		parent_id INTEGER,
		file_path TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`,

	// 2. 可変タグテーブル（名前：値 の属性情報）
	`CREATE TABLE IF NOT EXISTS page_tags (
		page_id INTEGER,
		name TEXT,
		value TEXT,
		PRIMARY KEY (page_id, name),
		FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
	);`,

	// 3. ページ権限の検索インデックス（サイドカー <id>.meta.json から再生成される派生データ）。
	//    owner=所有ユーザー名, grp=所有グループ名, mode=3桁の権限（認証認可設計.md 3章）。
	`CREATE TABLE IF NOT EXISTS page_perms (
		page_id INTEGER PRIMARY KEY,
		owner TEXT,
		grp TEXT,
		mode TEXT,
		FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
	);`,
}

// CreateCoreTables はコアテーブル（pages / page_tags）を作成します。
// 本番では InitDB から、テストでは各テストのセットアップから呼び出します。
func CreateCoreTables(db *sql.DB) error {
	for _, q := range CoreTables {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
