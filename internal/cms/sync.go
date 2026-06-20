package cms

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/database"
)

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
// コア（pages / page_tags）を同期したあと、登録済みの全プラグインを走査して
// ユースケース固有のテーブルを同期します。
func SyncIndex(id string, htmlContent string) error {
	// 手順1: HTMLをノード木にパースする
	root, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return err
	}

	pageIDInt, err := strconv.Atoi(id)
	if err != nil {
		return err
	}

	// 手順2: コア情報（タイトル・親ページID・タグ）を抽出する
	core := ParseCore(root)

	// 手順3: 物理ファイルの保存先パスを構築
	filePath := filepath.Join(GetPageDir(id), id+".html")

	var parentIDInt sql.NullInt64
	if core.ParentID != "" {
		if pid, e := strconv.Atoi(core.ParentID); e == nil {
			parentIDInt = sql.NullInt64{Int64: int64(pid), Valid: true}
		}
	}

	// 手順4: トランザクション開始
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// コア1: pages テーブルへの upsert
	if _, err = tx.Exec(`
		INSERT INTO pages (id, title, parent_id, file_path)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			parent_id = excluded.parent_id,
			file_path = excluded.file_path,
			updated_at = CURRENT_TIMESTAMP
	`, pageIDInt, core.Title, parentIDInt, filePath); err != nil {
		return err
	}

	// コア2: page_tags の洗い替え
	if _, err = tx.Exec(`DELETE FROM page_tags WHERE page_id = ?`, pageIDInt); err != nil {
		return err
	}
	for _, tag := range core.Tags {
		if _, err = tx.Exec(
			`INSERT INTO page_tags (page_id, name, value) VALUES (?, ?, ?)`,
			pageIDInt, tag.Name, tag.Value); err != nil {
			return err
		}
	}

	// 手順5: 各プラグインがユースケース固有テーブルを洗い替え
	for _, p := range Plugins() {
		if err = p.Sync(tx, pageIDInt, root); err != nil {
			return fmt.Errorf("プラグイン %q の同期に失敗: %w", p.Name(), err)
		}
	}

	return tx.Commit()
}

// RebuildDatabase は、HTMLファイル群（data/master配下）を正として、
// データベースのインデックスを完全に再構築します。
//
// 既存の全テーブルをDROPしてスキーマごと作り直すため、廃止されたプラグインの
// 残存テーブルなどスキーマのdriftも含めて完全に初期化されます。DBファイル自体は
// 削除せず、接続を開いたまま実行することで、Windowsでのファイルロックや
// リビルド中の他リクエストとの競合を避けています。処理は冪等です。
func RebuildDatabase() error {
	// 1. 現在DBに存在する全テーブルを sqlite_master から列挙してDROPする。
	if err := dropAllTables(database.DB); err != nil {
		return err
	}

	// 2. コアテーブルと全プラグインのテーブルを作り直す。
	if err := database.CreateCoreTables(database.DB); err != nil {
		return err
	}
	if err := ApplySchema(database.DB); err != nil {
		return err
	}

	// 3. data/master 以下のすべての .html ファイルを探索して SyncIndex を実行する。
	return resyncAllPages()
}

// dropAllTables は sqlite_master を列挙して、ユーザー定義の全テーブルをDROPします。
func dropAllTables(db *sql.DB) error {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()

	// 外部キー制約による削除順の問題を避けるため、DROP中は一時的にFKを無効化する。
	if _, err := db.Exec("PRAGMA foreign_keys = OFF;"); err != nil {
		return err
	}
	defer db.Exec("PRAGMA foreign_keys = ON;")

	for _, name := range names {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + name); err != nil {
			return err
		}
	}
	return nil
}

// RebuildIfEmpty は、pages テーブルが空でかつ data/master にHTMLファイルが存在する場合に、
// データベースを全再構築します。バックアップからファイル（data/master）だけを復元した状態で
// アプリを起動するだけでDBが自動再生成されるようにするための、起動時フックです。
func RebuildIfEmpty() error {
	var count int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM pages").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if !hasHTMLFiles(MasterDir) {
		return nil
	}
	log.Println("空のDBとHTMLファイルを検出: データベースを自動再構築します")
	return RebuildDatabase()
}

// hasHTMLFiles は dir 配下に .html ファイルが1つでも存在するかを返します。
func hasHTMLFiles(dir string) bool {
	found := false
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// resyncAllPages は data/master 配下の全 .html を走査して SyncIndex を実行します。
// 個々のファイルのエラーは握り潰して他ファイルの処理を継続します（冪等な再実行で回復可能）。
func resyncAllPages() error {
	return filepath.Walk(MasterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// data/master が存在しない場合などは、再構築対象なしとして正常終了する。
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			id := strings.TrimSuffix(info.Name(), ".html")
			_ = SyncIndex(id, string(content))
		}
		return nil
	})
}
