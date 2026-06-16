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

	// 手順3: 検索インデックス用の pages テーブルを作成する
	// ★追加: 物理的な保存場所（00/00A1B など）とは別に、
	// ユーザーが画面上で「どのフォルダに整理されているか」を判別するための
	// 仮想階層を格納する「path」カラムを追加しました。
	query := `
	CREATE TABLE IF NOT EXISTS pages (
		id TEXT PRIMARY KEY,
		type TEXT,
		path TEXT,
		title TEXT,
		summary TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = DB.Exec(query)
	return err
}
