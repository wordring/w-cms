package database

import "database/sql"

// AuthDB は認証・認可用の独立したデータベース接続（data/auth.db）です。
// 本体の cms.db（DB）とは別ライフサイクルで管理され、RebuildDatabase の対象外です。
// ここにはユーザー・グループ・セッションなど「ファイルから再生成できない」情報のみを保持します。
var AuthDB *sql.DB

// InitAuthDB は data/auth.db への接続を開き、認証用テーブルを作成します。
// data ディレクトリは InitDB が先に作成している前提です。
// 接続ごとに効く設定は cms.db と**同じく DSN の _pragma で**指定します
// （2026-09-03。それまでは素のパスで開き、foreign_keys だけを Exec で立てていた）。
//
// 直した実害は2つ:
//
//   - **busy_timeout が無く、競合が即 SQLITE_BUSY になっていた。** auth.db は
//     読むだけの器ではありません——セッションの確かめ（internal/auth の resolveSession。当時は ResolveSession）が
//     **認証のたびに** last_seen を UPDATE し、監査記録も書き込みます（2026-10-14 からは、last_seen を書くのは利用者の
//     操作のときだけ・前の記録から1分以上経ったときだけ——internal/auth/session.go）。同時アクセスで衝突すると
//     `database is locked` が返り、セッションの確かめはそれを「セッション無効」と
//     区別できないので **401**——つまり**利用者が黙ってログアウトさせられていた**
//     （E2Eの間欠的な 401 として観測。実測で SQLITE_BUSY を確認）。
//   - **`PRAGMA foreign_keys` を Exec で立てていた。** Exec はプールの
//     1接続にしか効かないので、他の接続では外部キーが**静かに効いていません**
//     でした（cms.db 側が同じ罠を先に踏んで DSN 方式へ直してある）。
//
// journal_mode(WAL) は読み取りと書き込みの並行性を上げます（DB全体の永続設定）。
func InitAuthDB() error {
	var err error
	AuthDB, err = openSQLite("auth.db")
	if err != nil {
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
		last_seen DATETIME NOT NULL,
		device_id INTEGER
	);`,

	// 登録した端末（2026-10-31・internal/auth/device.go）。印はハッシュだけを置く。
	`CREATE TABLE IF NOT EXISTS devices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		last_seen DATETIME NOT NULL,
		last_ip TEXT NOT NULL DEFAULT ''
	);`,

	// ほかの端末を足すための一回限りの番号（1人に1つ）。
	`CREATE TABLE IF NOT EXISTS device_codes (
		username TEXT PRIMARY KEY,
		code_hash TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		fails INTEGER NOT NULL DEFAULT 0
	);`,

	// ログイン失敗の記録（総当たり対策）。
	`CREATE TABLE IF NOT EXISTS login_attempts (
		username TEXT PRIMARY KEY,
		fail_count INTEGER NOT NULL DEFAULT 0,
		last_fail DATETIME
	);`,

	// 利用者ごとの、メールにサインインしてよいアドレス（2026-10-27・internal/auth/mail_address.go）。決めるのは管理者。
	`CREATE TABLE IF NOT EXISTS user_mail_addresses (
		username TEXT NOT NULL,
		address TEXT NOT NULL,
		PRIMARY KEY (username, address)
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
	return addSessionDeviceColumn(db)
}

// addSessionDeviceColumn は、前からある auth.db の sessions に device_id を足します（2026-10-31。CREATE TABLE IF NOT EXISTS では足されない）。
func addSessionDeviceColumn(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'device_id'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE sessions ADD COLUMN device_id INTEGER`)
	return err
}
