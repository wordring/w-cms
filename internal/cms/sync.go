package cms

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
// コア（pages / page_perms）を同期したあと、登録済みの全プラグインを走査して
// プラグインのテーブル（可変タグ・発注書・部材など）を同期します。
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

	// 手順2: HTML本文（内容）からタイトル・タグを抽出する
	core := ParseCore(root)

	// 手順3: 物理ファイルの保存先パスを構築
	filePath := filepath.Join(page.GetPageDir(id), id+".html")

	// ページ属性（親ページID・作成日時・作成者・更新日時）はサイドカー（正本）から読み取る。
	// サイドカーが無い場合は親なし・作成情報なしとして扱い、更新日時は CURRENT_TIMESTAMP に
	// フォールバックする。サイドカーが正本なのでDB再構築でも属性が失われない。
	meta, metaOK := page.ReadSidecar(id)

	var parentIDInt sql.NullInt64
	if meta.ParentID != "" {
		if pid, e := strconv.Atoi(meta.ParentID); e == nil {
			parentIDInt = sql.NullInt64{Int64: int64(pid), Valid: true}
		}
	} else if !metaOK {
		// サイドカーが読めない＝親が「無い」のではなく「分からない」。
		// created_at / created_by は COALESCE で守られているのに parent_id だけ
		// 無条件上書きだったため、ここで索引に残る最後の1コピーを潰していた
		// （ページがツリーから消え、DB再構築でも戻らない）。
		//
		// 一方、サイドカーが**読めたうえで**親が空なのは admin による
		// トップレベルへの付け替えなので、そちらは従来どおり NULL を書く。
		var cur sql.NullInt64
		if e := database.DB.QueryRow(
			`SELECT parent_id FROM pages WHERE id = ?`, pageIDInt).Scan(&cur); e == nil {
			parentIDInt = cur
		}
	}

	var createdAt, createdBy, updatedAt sql.NullString
	if meta.CreatedAt != "" {
		createdAt = sql.NullString{String: meta.CreatedAt, Valid: true}
	}
	if meta.CreatedBy != "" {
		createdBy = sql.NullString{String: meta.CreatedBy, Valid: true}
	}
	if meta.UpdatedAt != "" {
		updatedAt = sql.NullString{String: meta.UpdatedAt, Valid: true}
	}

	// 手順4: トランザクション開始
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// コア1: pages テーブルへの upsert
	if _, err = tx.Exec(`
		INSERT INTO pages (id, title, parent_id, file_path, created_at, created_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, COALESCE(?, CURRENT_TIMESTAMP))
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			parent_id = excluded.parent_id,
			file_path = excluded.file_path,
			created_at = COALESCE(excluded.created_at, pages.created_at),
			created_by = COALESCE(excluded.created_by, pages.created_by),
			updated_at = COALESCE(excluded.updated_at, CURRENT_TIMESTAMP)
	`, pageIDInt, core.Title, parentIDInt, filePath, createdAt, createdBy, updatedAt); err != nil {
		return err
	}

	// コア2: ページ権限インデックス（サイドカー <id>.meta.json → page_perms）の同期
	// （dl[data-type="tags"] → page_tags はコアではなく plugin_page_tags.go が担う）
	if err = page.SyncPageMeta(tx, pageIDInt, id); err != nil {
		return err
	}

	// 手順5: 各プラグインがユースケース固有テーブルを洗い替え。
	//
	// ただし**テンプレート配下のページは②索引・③計算へ載せません**
	// （docs/【考察】ページテンプレート.md §6）。載せると、テンプレートに書かれた
	// 仮の発注書が client_orders に入り、手配集計・利益計算に出てきてしまいます。
	//
	// 除外は「飛ばす」のではなく**空の本文を渡す**形で行います。全プラグインの Sync は
	// 冒頭で当該ページの行を DELETE する洗い替えなので、空を渡せば
	// 「古い行は消える・新しい行は入らない」となり、**普通のページをテンプレートフォルダへ
	// 移したときに古い行が残りません**（飛ばすと残る）。冪等で自己修復もします。
	//
	// コア（pages / page_perms）は従来どおり同期します——テンプレートページも
	// 普通に存在し、閲覧も権限判定もページ階層への表示も従来どおり動く必要があるため。
	pluginRoot := root
	if IsTemplateArea(id) {
		if pluginRoot, err = html.Parse(strings.NewReader("")); err != nil {
			return err
		}
	}
	for _, p := range Plugins() {
		if err = p.Sync(tx, pageIDInt, pluginRoot); err != nil {
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
	// 0. 「これから再構築する」印を残す。途中で止まったら次の起動でやり直すため
	//    （印が無いと、途中まで入ったDBは再構築済みと見分けが付かない）。
	started := time.Now()
	if err := markRebuildStarted(); err != nil {
		return err
	}

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
	pages, err := resyncAllPages()
	if err != nil {
		return err // 印は残したまま＝次の起動でやり直す
	}

	// 4. 終わった印を、件数と所要時間つきで残す。
	ms := time.Since(started).Milliseconds()
	if err := markRebuildFinished(pages, ms); err != nil {
		return err
	}
	log.Printf("索引を再構築しました: %dページ / %dミリ秒", pages, ms)
	return nil
}

// rebuildStateTable は再構築の進行状況を残す1行テーブルです。
//
// 再構築は data/master を1ページずつ読み直す長い処理で、途中で止まると（強制終了・停電）
// **次の起動は何事もなく成功し、取り込めなかったページは開いても「ありません」のまま**に
// なります。pages が空かどうかだけでは、途中まで入ったDBと再構築済みのDBを見分けられません。
// そこで「始めた印」を残し、印が残ったまま起動したら必ずやり直します。
//
// このテーブルだけは dropAllTables の対象外です（再構築の最中に自分を消せない）。
const rebuildStateTable = "rebuild_state"

// ensureRebuildStateTable は進行状況テーブルを用意します。
func ensureRebuildStateTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ` + rebuildStateTable + ` (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		started_at TEXT,
		finished_at TEXT,
		pages INTEGER,
		duration_ms INTEGER
	);`)
	return err
}

// markRebuildStarted は「これから再構築する」印を残します（終了時刻は空のまま）。
func markRebuildStarted() error {
	if err := ensureRebuildStateTable(database.DB); err != nil {
		return err
	}
	_, err := database.DB.Exec(`
		INSERT INTO `+rebuildStateTable+` (id, started_at, finished_at, pages, duration_ms)
		VALUES (1, ?, NULL, NULL, NULL)
		ON CONFLICT(id) DO UPDATE SET
			started_at = excluded.started_at, finished_at = NULL,
			pages = NULL, duration_ms = NULL
	`, time.Now().UTC().Format(time.RFC3339))
	return err
}

// markRebuildFinished は再構築の完了を、件数と所要時間つきで記録します。
func markRebuildFinished(pages int, durationMS int64) error {
	if err := ensureRebuildStateTable(database.DB); err != nil {
		return err
	}
	_, err := database.DB.Exec(`
		UPDATE `+rebuildStateTable+`
		SET finished_at = ?, pages = ?, duration_ms = ?
		WHERE id = 1
	`, time.Now().UTC().Format(time.RFC3339), pages, durationMS)
	return err
}

// rebuildUnfinished は「始めた印はあるが終わっていない」状態かを返します。
func rebuildUnfinished() (bool, error) {
	if err := ensureRebuildStateTable(database.DB); err != nil {
		return false, err
	}
	var started sql.NullString
	var finished sql.NullString
	err := database.DB.QueryRow(
		`SELECT started_at, finished_at FROM ` + rebuildStateTable + ` WHERE id = 1`).
		Scan(&started, &finished)
	if err != nil {
		return false, nil // 行が無い＝再構築を始めたことがない
	}
	return started.Valid && started.String != "" && !finished.Valid, nil
}

// lastRebuildResult は直近の再構築の件数と所要時間（ミリ秒）を返します。
func lastRebuildResult() (pages int, durationMS int64, ok bool) {
	if err := ensureRebuildStateTable(database.DB); err != nil {
		return 0, 0, false
	}
	var p, d sql.NullInt64
	err := database.DB.QueryRow(
		`SELECT pages, duration_ms FROM ` + rebuildStateTable + ` WHERE id = 1`).Scan(&p, &d)
	if err != nil || !p.Valid {
		return 0, 0, false
	}
	return int(p.Int64), d.Int64, true
}

// dropAllTables は sqlite_master を列挙して、ユーザー定義の全テーブルをDROPします。
// 進行状況テーブル（rebuild_state）だけは残します——再構築の最中に「始めた印」を
// 自分で消してしまうと、途中で止まったことを次の起動で知る手段が無くなるためです。
func dropAllTables(db *sql.DB) error {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != '` + rebuildStateTable + `'`)
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

// RebuildIfNeeded は、次のいずれかのときにデータベースを全再構築します。
//   - 前回の再構築が完了していない（中断の印が残っている）
//   - pages テーブルが空でかつ data/master にHTMLファイルが存在する場合に、
// データベースを全再構築します。バックアップからファイル（data/master）だけを復元した状態で
// アプリを起動するだけでDBが自動再生成されるようにするための、起動時フックです。
func RebuildIfNeeded() error {
	if !hasHTMLFiles(page.MasterDir) {
		return nil // 元になるファイルが無いなら再構築のしようがない
	}

	// 中断の検出を先に見る。pages が空でなくても、途中まで入っただけかもしれない
	// ——取り込めなかったページは開いても「ありません」のままで、記録も残らない。
	unfinished, err := rebuildUnfinished()
	if err != nil {
		return err
	}
	if unfinished {
		log.Println("前回の索引再構築が完了していません: やり直します")
		return RebuildDatabase()
	}

	var count int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM pages").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
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
// 取り込んだページ数を返します（完了の記録に使う）。
func resyncAllPages() (int, error) {
	count := 0
	err := filepath.Walk(page.MasterDir, func(path string, info os.FileInfo, err error) error {
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
			count++
		}
		return nil
	})
	return count, err
}

// PurgePageIndex はページの索引を消します（正本ファイルには触れません）。
//
// プラグインの Sync はどれも「page_id の行を削除してから入れ直す」形なので、
// **空の本文で同期を走らせる**と型付きテーブルの行が洗い流されます。
// そのあとコアテーブル（pages / page_perms）の行を消します
// （SyncIndex は pages を upsert するため、順序はこの通りでなければなりません）。
func PurgePageIndex(id string) error {
	if err := SyncIndex(id, ""); err != nil {
		return err
	}
	pageID, err := strconv.Atoi(id)
	if err != nil {
		return err
	}
	if _, err := database.DB.Exec(`DELETE FROM page_perms WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	_, err = database.DB.Exec(`DELETE FROM pages WHERE id = ?`, pageID)
	return err
}
