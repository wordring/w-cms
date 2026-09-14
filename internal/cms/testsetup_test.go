package cms

// ─────────────────────────────────────────────────────────────────────────
// 試験の下ごしらえ、その芯（2026-09-14）
//
// **同じ21行が8ファイルに写されていました**——一時ディレクトリへ移り、DBを開き、
// コアテーブルとプラグインのスキーマを作る。**スキーマを変えた日に直す場所が
// 8つある**形で、実際 `page_tags` を足したときは危うく取り残すところでした。
//
// **各ファイルの `setupXxxTest` は残します。** 名前が「何を用意したか」を語って
// いるうえ（受信箱がある・取引先の木がある・公開ページがある）、足すものが
// それぞれ違うからです。ここが引き受けるのは**どれにも共通する芯だけ**です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"os"
	"testing"

	"w-cms/internal/database"
)

// newTestDB は一時ディレクトリへ移り、空のDBを用意して返します。
//
// **メモリDBです。** 速くて後片付けが要りません——が、`visibleChildren` や
// `page.CanView` を通る試験では**使えません**（`:memory:` は接続ごとに別のDBで、
// 行を読みながら中で別のクエリを投げると**空の別DBに当たり、絞り込みが静かに
// 全部落ちます**。2026-09-03 に本番コードで踏んだ罠）。そういう試験は
// `newTestFileDB` を使ってください。
//
// 戻り値の `*sql.DB` は `database.DB` にも入ります（本番コードがそこを見るため）。
func newTestDB(t *testing.T) *sql.DB { return openTestDB(t, ":memory:") }

// newTestFileDB は同じ下ごしらえを**ファイルDB**で行います。
//
// `visibleChildren`・`page.CanView` を通る試験はこちらを使うこと
// （理由は `newTestDB` のコメント）。置き場は一時ディレクトリなので、
// 後片付けは `t.TempDir()` が引き受けます。
func newTestFileDB(t *testing.T) *sql.DB { return openTestDB(t, "test.db") }

func openTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	// **プラグインのスキーマも作ります。** ここを忘れると、索引に書く試験だけが
	// 「no such table」で落ちます——しかも落ちるのは足した当人ではなく、次に
	// その表を読む試験です。
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	return db
}
