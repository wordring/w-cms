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

	// 手順3: コアテーブル（pages / page_tags）を作成する。
	// ユースケース固有のテーブル（発注書・見積もり・部材など）は、
	// internal/cms の各プラグインが Schema() で定義し、main から cms.ApplySchema() で作成する。
	return CreateCoreTables(DB)
}

// CoreTables はどのユースケースにも共通する基盤テーブル（pages / page_tags）の定義です。
// pages は全ドキュメントの基本情報、page_tags は <m-tag> の可変属性を保持します。
// これらは外部キーの参照先となるため、プラグインのテーブルより先に作成する必要があります。
var CoreTables = []string{
	// 1. ドキュメントの基本インデックス情報（本文はファイル保存）。
	//    parent_id / created_at / created_by / updated_at はサイドカー
	//    <id>.meta.json（正本）から同期される派生値（DB再構築でも失われない）。
	//    title と page_tags のみHTML本文（内容）由来。
	`CREATE TABLE IF NOT EXISTS pages (
		id INTEGER PRIMARY KEY,
		title TEXT,
		parent_id INTEGER,
		file_path TEXT,
		created_at DATETIME,
		created_by TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`,

	// 2. 可変タグテーブル（名前：値 の属性情報）
	//    name は自由語で、**同じ name が同一ページに複数あってよい**（担当者が2人、
	//    関連部品番号が複数、といった多値属性を表現できる）。そのため (page_id, name) を
	//    主キーにはせず、検索用の非一意インデックスだけを張る。
	//    かつては主キーだったため、同名タグを2つ置くと保存が UNIQUE 制約違反で失敗していた。
	//    値を1つだけ使いたい用途（例: <m-tag name="部品番号">）は、HTML木から先頭を採る
	//    ヘルパ cms.TagValue が担う（DBのこの表は現状クエリされていない検索用インデックス）。
	`CREATE TABLE IF NOT EXISTS page_tags (
		page_id INTEGER,
		name TEXT,
		value TEXT,
		FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
	);`,

	// 3. ページ権限の検索インデックス（サイドカー <id>.meta.json から再生成される派生データ）。
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

// coreIndexes はコアテーブルに張る検索用インデックスです。
// page_tags は主キーを持たないため、ページ単位・タグ名単位の絞り込み用にここで補います。
var coreIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_page_tags_page_name ON page_tags(page_id, name);`,
}

// coreMigrations は、既存DB（CREATE TABLE IF NOT EXISTS では更新されない）に
// 後から追加された列を補うための冪等なマイグレーションです。
// 列が既に存在する場合の "duplicate column name" エラーは無視します。
var coreMigrations = []string{
	`ALTER TABLE pages ADD COLUMN created_at DATETIME`,
	`ALTER TABLE pages ADD COLUMN created_by TEXT`,
	`ALTER TABLE page_perms ADD COLUMN public INTEGER NOT NULL DEFAULT 0`,
}

// CreateCoreTables はコアテーブル（pages / page_tags）を作成します。
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
	for _, q := range coreIndexes {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
