// Package database は SQLite への接続とコアテーブルの作成を担います。
//
// データベースは**2つ**あり、寿命がまったく違います。
//
//   - [DB]（data/cms.db）……**使い捨ての派生インデックス**。正本は data/master 配下の
//     本文HTMLと属性サイドカーで、cms.db はそこから丸ごと再生成できます
//     （cms.RebuildDatabase）。バックアップの対象ではありません。
//   - [AuthDB]（data/auth.db）……利用者・グループ・セッション。**ファイルから再生成
//     できない**情報なので、data/master とは別に必ずバックアップします。
//
// このパッケージが作るのは**コアテーブルだけ**です（pages / page_perms と、認証側の
// 一式）。本文の形式（data-type）に対応するテーブルは各プラグインが Schema() で宣言し、
// main が cms.ApplySchema でまとめて作ります——語彙の解釈をコアが持たない、という
// 分担がここにも現れています。
//
// 依存は最下層で、このパッケージは w-cms の他のパッケージを一切 import しません。
package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

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

	// 手順2: データベースファイル (data/cms.db) への接続を開く。
	// 接続ごとに効く設定（foreign_keys / busy_timeout）は DSN の _pragma で指定し、
	// プール内のどの接続にも確実に適用されるようにする（Exec は1接続にしか効かないため）。
	//   - foreign_keys(1): 外部キー制約を有効化
	//   - busy_timeout(5000): 書き込みロック衝突時に最大5秒リトライ待ち（database is locked 緩和）
	//   - journal_mode(WAL): 読み取りと書き込みの並行性を上げる（DB全体の永続設定）
	// 同時編集の堅牢化（[docs/【考察】同時編集の競合対策.md] シナリオD）。
	dbPath := filepath.Join("data", "cms.db")
	dsn := filepath.ToSlash(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	var err error
	DB, err = sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}

	// 手順3: コアテーブル（pages / page_perms）を作成する。
	// カスタム要素由来のテーブル（可変タグ・発注書・見積もり・部材など）は、
	// internal/cms の各プラグインが Schema() で定義し、main から cms.ApplySchema() で作成する。
	return CreateCoreTables(DB)
}

// CoreTables はどのユースケースにも依存しない基盤テーブル（pages / page_perms）の定義です。
// pages は全ドキュメントの基本情報、page_perms は権限の検索インデックス。
// これらは外部キーの参照先となるため、プラグインのテーブルより先に作成する必要があります。
//
// 本文の形式（data-type）に対応するテーブルはここには置きません。
// dl[data-type="tags"]（可変タグ）も専用テーブルを持ちません——②汎用索引
// （cms/vocab_index.go）が他の形式と同じ縦持ちで索引します（2026-08-30・D-1）。
var CoreTables = []string{
	// 1. ドキュメントの基本インデックス情報（本文はファイル保存）。
	//    parent_id / created_at / created_by / updated_at はサイドカー
	//    <id>.meta.json（正本）から同期される派生値（DB再構築でも失われない）。
	//    title のみHTML本文（内容）由来。
	`CREATE TABLE IF NOT EXISTS pages (
		id INTEGER PRIMARY KEY,
		title TEXT,
		parent_id INTEGER,
		file_path TEXT,
		created_at DATETIME,
		created_by TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`,

	// 2. ページ権限の検索インデックス（サイドカー <id>.meta.json から再生成される派生データ）。
	//    owner=所有ユーザー名, grp=所有グループ名, mode=3桁の権限（認証認可設計.md 3章）。
	//    public=匿名公開フラグ（0/1。認証認可設計.md 10章）。実効公開は親チェーンとの AND で別途判定。
	`CREATE TABLE IF NOT EXISTS page_perms (
		page_id INTEGER PRIMARY KEY,
		owner TEXT,
		grp TEXT,
		mode TEXT,
		public INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
	);`,
}

// coreMigrations は、既存DB（CREATE TABLE IF NOT EXISTS では更新されない）に
// 後から追加された列を補うための冪等なマイグレーションです。
// 列が既に存在する場合の "duplicate column name" エラーは無視します。
var coreMigrations = []string{
	`ALTER TABLE pages ADD COLUMN created_at DATETIME`,
	`ALTER TABLE pages ADD COLUMN created_by TEXT`,
	`ALTER TABLE page_perms ADD COLUMN public INTEGER NOT NULL DEFAULT 0`,
}

// CreateCoreTables はコアテーブル（pages / page_perms）を作成します。
// 本番では InitDB から、テストでは各テストのセットアップから呼び出します。
func CreateCoreTables(db *sql.DB) error {
	for _, q := range CoreTables {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	// 既存DBへの列追加（新規DBでは CREATE TABLE 済みのため冪等にスキップ）。
	for _, q := range coreMigrations {
		if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}
