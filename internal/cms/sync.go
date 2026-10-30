package cms

import (
	"errors"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
// コア（pages / page_perms）を同期したあと、配送係（walk.go）が本文を1回歩いて
// 観察係（汎用索引 vocab_index）へ要素を届けます。
//
// 失敗したら（表の写しだけの失敗も）、少し待って直し直します（index_retry.go）。
func SyncIndex(id string, htmlContent string) error {
	var tablesErr error
	err := syncPageIndex(id, htmlContent, &tablesErr)
	if err != nil || tablesErr != nil {
		scheduleIndexRetry(id)
	}
	return err
}

// syncPageIndex は SyncIndex の中身です。表の写しの失敗は tablesErr に入れます（誤りとしては返さない）。
func syncPageIndex(id string, htmlContent string, tablesErr *error) error {
	// DB の作り直しが表を消して作り直しているあいだは待つ（rebuildGate）。
	rebuildGate.RLock()
	defer rebuildGate.RUnlock()
	markIndexWritten() // オートコンプリートの候補を、暇になったら数え直す（autocomplete.go）
	// 手順1: HTMLをノード木にパースする
	root, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return err
	}

	pageIDInt, err := strconv.Atoi(id)
	if err != nil {
		return err
	}

	// 手順2: HTML本文（内容）からタイトルを抽出する
	title := PageTitle(root)

	// 手順3: 物理ファイルの保存先パスを構築
	filePath := page.BodyPath(id)

	// ページ属性（親ページID・作成日時・作成者・更新日時）はサイドカー（正本）から読み取る。
	// サイドカーが無い場合は親なし・作成情報なしとして扱い、更新日時は「今」（RFC3339 UTC）に
	// フォールバックする。サイドカーが正本なのでDB再構築でも属性が失われない。
	// かつては SQLite の CURRENT_TIMESTAMP（空白区切り＝ISO 8601 ではない）だったが、
	// 「日時は ISO 8601 を全域の正とする」（2026-08-30・要件定義書 §3）に合わせ、
	// サイドカー由来の値と同じ T 区切りへ揃えた。
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
	} else {
		updatedAt = sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true}
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
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			parent_id = excluded.parent_id,
			file_path = excluded.file_path,
			created_at = COALESCE(excluded.created_at, pages.created_at),
			created_by = COALESCE(excluded.created_by, pages.created_by),
			updated_at = excluded.updated_at
	`, pageIDInt, title, parentIDInt, filePath, createdAt, createdBy, updatedAt); err != nil {
		return err
	}

	// コア2: ページ権限インデックス（サイドカー <id>.meta.json → page_perms）の同期
	// （dl[data-type="tags"] も含め、マーカーの索引は②汎用索引 vocab_index が担う）
	if err = page.SyncPageMeta(tx, pageIDInt, id); err != nil {
		return err
	}

	// コア3: 本文のリンクとファイル表示（ほかのページを指す所）の索引——消す前の確かめが引く（page_links.go・2026-10-23）
	if err = syncPageLinks(tx, pageIDInt, root, IsTemplateArea(id)); err != nil {
		return err
	}

	// 手順5: 回覧（観察係の段）——各プラグインがユースケース固有テーブルを洗い替え。
	//
	// 走査はコアの配送係が**1回だけ**行い、引き金（`data-type`）に当たった要素を
	// 担当へ届けます（walk.go）。かつては各プラグインが木を丸ごと受け取って自前で
	// 歩いていました。
	//
	// ただし**テンプレート配下のページは②索引・③計算へ載せません**
	// （docs/考察/【考察】ページテンプレート.md §6）。載せると、テンプレートに書かれた
	// 仮の発注書が②汎用索引に入り、手配集計に出てきてしまいます。
	//
	// 除外は「飛ばす」のではなく**要素イベントを1つも発火しない**形で行います。
	// OnPageStart（＝当該ページ分の DELETE）は呼ぶので「古い行は消える・新しい行は
	// 入らない」となり、**普通のページをテンプレートフォルダへ移したときに古い行が
	// 残りません**（飛ばすと残る）。冪等で自己修復もします。
	//
	// コア（pages / page_perms）は従来どおり同期します——テンプレートページも
	// 普通に存在し、閲覧も権限判定もページ階層への表示も従来どおり動く必要があるため。
	obsCtx := &ObserveContext{
		Tx:     tx,
		PageID: pageIDInt,
		Meta: func() (page.PageMeta, error) {
			m, ok := page.ReadSidecar(id)
			if !ok {
				return page.PageMeta{}, fmt.Errorf("ページ属性ファイルを読めません: %s", id)
			}
			return m, nil
		},
		// ページ横断メタは文書順では拾えない（タグが表より後ろにあってもよい）ので、
		// コアが木から1度だけ引く口を用意する。
		Tag: func(name string) string { return TagValue(root, name) },
	}

	for _, o := range Observers() {
		if err = o.OnPageStart(obsCtx); err != nil {
			return fmt.Errorf("プラグイン %q の洗い替えに失敗: %w", o.Name(), err)
		}
	}
	if !IsTemplateArea(id) {
		if err = walkers.walkObserve(obsCtx, []*html.Node{root}); err != nil {
			return err // 失敗したプラグイン名は observerAdapter が包んでいる
		}
	}
	for _, o := range Observers() {
		f, ok := o.(PageFinisher)
		if !ok {
			continue
		}
		if err = f.OnPageEnd(obsCtx); err != nil {
			return fmt.Errorf("プラグイン %q の確定に失敗: %w", o.Name(), err)
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	// 手順6: 表の写し（data/tables.db・キャプションの名前の表）——tables_db.go。
	// ⚠ **派生なので、失敗しても保存は止めません**（運用者が SQL の道具で書き込みロックを
	// 握っていることがある）。ログに残し、少し待って直し直します（index_retry.go）。
	if err := syncCaptionTables(id, pageIDInt, root); err != nil {
		log.Printf("表の写し（tables.db）の同期に失敗 page=%s: %v", id, err)
		*tablesErr = err
	}
	return nil
}

// rebuildGate は、DB の作り直しが表を消してから作り直すまでのあいだ、索引への書き込み（SyncIndex）を待たせる関門です
// （2026-10-29）。それまでは、その一瞬に保存すると、本文は書けても索引づけが「表が無い」で落ちて 500 が返った
// （【考察】コードの矛盾 §5.1）。⚠ 閉じるのは表を消して作り直す短いあいだだけ——ページを入れ直す歩き（resyncAllPages）は
// 自分も SyncIndex を呼ぶので、その前に開ける（閉じたまま呼ぶと止まる）。
var rebuildGate sync.RWMutex

// rebuildMu は、DB の作り直しを1本ずつ通すロックです。
var rebuildMu sync.Mutex

// RebuildDatabase は、HTMLファイル群（data/master配下）を正として、
// データベースのインデックスを完全に再構築します。
//
// 既存の全テーブルをDROPしてスキーマごと作り直すため、廃止されたプラグインの
// 残存テーブルなどスキーマのdriftも含めて完全に初期化されます。DBファイル自体は
// 削除せず、接続を開いたまま実行することで、Windowsでのファイルロックや
// リビルド中の他リクエストとの競合を避けています。処理は冪等です。
func RebuildDatabase() error {
	// 1本ずつ通す（2026-10-30。二度押すと、2本目が1本目の歩きの途中で表を消した）。
	if !rebuildMu.TryLock() {
		return errors.New("いま DB を作り直しています。終わってからもう一度押してください")
	}
	defer rebuildMu.Unlock()
	// 0. 設定を読み直す。**語→型の推論辞書がここに入っている**ので、先に読まないと
	//    「辞書へ語を足してDB再構築」（ユーザー決定 2026-08-30）が効かない
	//    ——再起動なしで反映されるのは、この1行のおかげ。
	if err := LoadSettings(); err != nil {
		return err
	}

	// 0b. 「これから再構築する」印を残す。途中で止まったら次の起動でやり直すため
	//    （印が無いと、途中まで入ったDBは再構築済みと見分けが付かない）。
	started := time.Now()
	if err := markRebuildStarted(); err != nil {
		return err
	}

	// 1〜2. 表を消して作り直す（そのあいだは、保存の索引づけを待たせる——rebuildGate）。
	if err := recreateTables(); err != nil {
		return err
	}

	// 3. data/master 以下のすべての .html ファイルを探索して SyncIndex を実行する。
	pages, err := resyncAllPages()
	if err != nil {
		return err // 印は残したまま＝次の起動でやり直す
	}
	// 3b. 作り直しのあいだに書かれたページを、もう一度入れる（2026-10-26）。歩きが通り過ぎたあとに保存されたページは、
	//    保存の索引づけが表を消した瞬間に重なって失敗すると、古い索引のまま残った（本文は書けている）。
	resyncPagesChangedSince(started)

	// 4. 終わった印を、件数と所要時間つきで残す。
	ms := time.Since(started).Milliseconds()
	if err := markRebuildFinished(pages, ms); err != nil {
		return err
	}
	log.Printf("索引を再構築しました: %dページ / %dミリ秒", pages, ms)
	return nil
}

// recreateTables は、作り直しの 1〜2 の手順です（関門を閉じたまま）。
//
//  1. 現在DBに存在する全テーブルを sqlite_master から列挙してDROPする。
//     表の写し（data/tables.db）も空にする（ページを入れ直すときに作り直される）。
//  2. コアテーブルと全プラグインのテーブルを作り直す。
func recreateTables() error {
	rebuildGate.Lock()
	defer rebuildGate.Unlock()
	if err := dropAllTables(database.DB); err != nil {
		return err
	}
	if err := ResetTablesDB(); err != nil {
		return err
	}
	if err := database.CreateCoreTables(database.DB); err != nil {
		return err
	}
	return ApplySchema(database.DB)
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
		`SELECT started_at, finished_at FROM `+rebuildStateTable+` WHERE id = 1`).
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
		`SELECT pages, duration_ms FROM `+rebuildStateTable+` WHERE id = 1`).Scan(&p, &d)
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

// RebuildIfNeeded は、data/master にHTMLファイルがあり、次のいずれかのときにデータベースを全再構築します。
//   - 前回の再構築が完了していない（中断の印が残っている）
//   - 表の写し（data/tables.db）が今の作り方で作られていない（tablesDBOutdated）
//   - pages テーブルが空
//
// バックアップからファイル（data/master）だけを復元した状態で
// アプリを起動するだけでDBが自動再生成されるようにするための、起動時フックです。
func RebuildIfNeeded() error {
	if !hasHTMLFiles(page.MasterDir()) {
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

	// 表の写し（data/tables.db）が今の作り方で作られていなければ作り直す
	// （この仕組みが入った版で初めて起動したとき・作り方を変えたとき）。
	if tablesDBOutdated() {
		log.Println("表の写し（data/tables.db）を今の作り方で作り直します: 再構築します")
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
func resyncAllPages() (int, error) { return resyncPages(time.Time{}) }

// resyncPagesChangedSince は、本文かサイドカーが since より後に書かれたページだけを入れ直します（失敗は数えない）。
func resyncPagesChangedSince(since time.Time) int {
	n, _ := resyncPages(since)
	return n
}

// resyncPages は data/master の本文を歩いて索引へ入れます。since が空でなければ、本文かサイドカーが since 以降に
// 書かれたページだけ。
func resyncPages(since time.Time) (int, error) {
	count, failed := 0, 0
	err := filepath.Walk(page.MasterDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// data/master が存在しない場合などは、再構築対象なしとして正常終了する。
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		// 版置き場は本文ではないので降りない（version.go）。いまの版は `.html.gz` なので
		// 下の `.html` 判定には掛からないが、**ここで明示的に閉じておく**——手で置かれた
		// `versions/foo.html` が「ページ」として索引に載るのを防ぐため。
		if info.IsDir() && info.Name() == versionsDirName {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			if !since.IsZero() && info.ModTime().Before(since) {
				if mi, err := os.Stat(strings.TrimSuffix(path, ".html") + ".meta.json"); err != nil || mi.ModTime().Before(since) {
					return nil
				}
			}
			content, err := os.ReadFile(path)
			if err != nil {
				if !since.IsZero() {
					return nil // 2周目は、作り直しのあいだにごみ箱へ移ったページなどを飛ばす
				}
				return err
			}
			id := strings.TrimSuffix(info.Name(), ".html")
			// 入らなかったページはログに書き、数えない（2026-10-30。それまでは誤りを捨てて「入った」と数えていた）。
			if err := SyncIndex(id, string(content)); err != nil {
				log.Printf("⚠ 索引に入れられませんでした page=%s: %v", id, err)
				failed++
				return nil
			}
			count++
		}
		return nil
	})
	if failed > 0 {
		log.Printf("⚠ 索引に入れられなかったページが %d 枚あります（入ったのは %d 枚）", failed, count)
	}
	return count, err
}

// PurgePageIndex はページの索引を消します（正本ファイルには触れません）。
//
// 観察係は「page_id の行を削除してから入れ直す」形なので、
// **空の本文で同期を走らせる**と索引の行が洗い流されます。
// そのあとコアテーブル（pages / page_perms）の行を消します
// （SyncIndex は pages を upsert するため、順序はこの通りでなければなりません）。
func PurgePageIndex(id string) error {
	tablesErr, err := purgePageIndex(id)
	if err != nil || tablesErr != nil {
		scheduleIndexRetry(id) // 本文はもうごみ箱にあるので、索引だけが残っている——直し直す（index_retry.go）
	}
	return err
}

// purgePageIndex は PurgePageIndex の中身です（表の写しの失敗は tablesErr に返す）。
func purgePageIndex(id string) (tablesErr, err error) {
	if err := syncPageIndex(id, "", &tablesErr); err != nil {
		return tablesErr, err
	}
	pageID, err := strconv.Atoi(id)
	if err != nil {
		return tablesErr, err
	}
	if _, err := database.DB.Exec(`DELETE FROM page_perms WHERE page_id = ?`, pageID); err != nil {
		return tablesErr, err
	}
	_, err = database.DB.Exec(`DELETE FROM pages WHERE id = ?`, pageID)
	return tablesErr, err
}
