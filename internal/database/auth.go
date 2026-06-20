package database

import (
	"database/sql"
	"path/filepath"
)

// AuthDB は認証・認可用の独立したデータベース接続（data/auth.db）です。
// 本体の cms.db（DB）とは別ライフサイクルで管理され、RebuildDatabase の対象外です。
// ここにはユーザー・グループ・セッションなど「ファイルから再生成できない」情報のみを保持します。
var AuthDB *sql.DB

// InitAuthDB は data/auth.db への接続を開き、認証用テーブルを作成します。
// data ディレクトリは InitDB が先に作成している前提です。
func InitAuthDB() error {
	dbPath := filepath.Join("data", "auth.db")
	var err error
	AuthDB, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	if _, err = AuthDB.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return err
	}
	return CreateAuthTables(AuthDB)
}

// AuthTables は認証・認可に用いるテーブル定義です。
var AuthTables = []string{
	// ユーザー。password_hash は外部認証のみのユーザーを想定して NULL 許容（認証認可設計.md 2.5節）。
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT,
		is_admin INTEGER NOT NULL DEFAULT 0,
		primary_group TEXT,
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`,

	// グループ（フラット、入れ子なし）。
	`CREATE TABLE IF NOT EXISTS groups (
		name TEXT PRIMARY KEY
	);`,

	// グループ所属（補助グループ。1ユーザーが複数グループに所属可）。
	`CREATE TABLE IF NOT EXISTS group_members (
		username TEXT NOT NULL,
		group_name TEXT NOT NULL,
		PRIMARY KEY (username, group_name)
	);`,

	// セッション。平文トークンは保存せず、SHA-256ハッシュをキーにする。
	`CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		expires_at DATETIME NOT NULL,
		last_seen DATETIME NOT NULL
	);`,

	// ログイン失敗の記録（総当たり対策）。
	`CREATE TABLE IF NOT EXISTS login_attempts (
		username TEXT PRIMARY KEY,
		fail_count INTEGER NOT NULL DEFAULT 0,
		last_fail DATETIME
	);`,

	// 監査ログ（書き込み・権限変更・管理操作を記録）。
	`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts DATETIME NOT NULL,
		username TEXT,
		action TEXT,
		target TEXT
	);`,
}

// CreateAuthTables は認証用テーブルを作成します（本番は InitAuthDB、テストは各セットアップから呼ぶ）。
func CreateAuthTables(db *sql.DB) error {
	for _, q := range AuthTables {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
