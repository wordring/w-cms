package cms

import (
	"database/sql"
	"fmt"
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
func RebuildDatabase() error {
	// 1. すべてのテーブルのデータを消去する。
	//    プラグインの所有テーブル（子→親順）→ コアテーブル の順で削除する。
	tables := append([]string{}, pluginTables()...)
	tables = append(tables, "page_tags", "pages")
	for _, table := range tables {
		if _, err := database.DB.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}

	// 2. data/master 以下のすべての .html ファイルを探索して SyncIndex を実行する
	return filepath.Walk(MasterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			id := strings.TrimSuffix(info.Name(), ".html")
			// エラーが出ても他のファイルの処理は継続する
			_ = SyncIndex(id, string(content))
		}
		return nil
	})
}
