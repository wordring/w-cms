package cms

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 未処理の受信のテスト（2026-09-03）。
//
// ユーザー:「未処理のメールやFAXを一覧できる方法が必要かも」。
// 固定するのは判定そのもの——**子ページが在ること自体が「手を付けた」の印**で、
// 人に状態を打たせるのは「対応：不要」の例外だけ、という線です。

// putIntakeRecord は通信記録ページを1枚作ります（受信箱の子として）。
func putIntakeRecord(t *testing.T, id, parent, subject, received string, extraTags string) {
	t.Helper()
	if err := page.WriteSidecar(id, page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: parent,
	}); err != nil {
		t.Fatalf("サイドカーの作成エラー: %v", err)
	}
	body := "<h1>" + subject + "</h1>" +
		`<dl data-type="tags">` +
		"<dt>" + ChannelTag + "</dt><dd>メール</dd>" +
		"<dt>受信日時</dt><dd>" + received + "</dd>" +
		"<dt>差出人</dt><dd>潮崎 光俊</dd>" +
		extraTags +
		"</dl>"
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

// TestUnhandledExcludesRecordsWithChildren は、**子ページを持つ記録が消える**ことを
// 固定します。ユーザー:「メールを読んだ時点で受注ページや部品ページが作られるので
// 問題ない」——手を付けた印を人に打たせないための仕掛けです。
func TestUnhandledExcludesRecordsWithChildren(t *testing.T) {
	setupIntakeTest(t)
	putIntakeRecord(t, "000201", "000100", "まだ見ていないメール", "2026-09-01T10:00:00+09:00", "")
	putIntakeRecord(t, "000202", "000100", "受注ページを作ったメール", "2026-09-02T10:00:00+09:00", "")
	// 000202 に子ページ（受注ページ）を1枚ぶら下げる。
	putIntakeRecord(t, "000203", "000202", "受注 PO-1", "2026-09-02T11:00:00+09:00", "")

	rows, total, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatalf("一覧を作れません: %v", err)
	}
	titles := make([]string, 0, len(rows))
	for _, r := range rows {
		titles = append(titles, r.Title)
	}
	joined := strings.Join(titles, " / ")
	if !strings.Contains(joined, "まだ見ていないメール") {
		t.Errorf("未処理の記録が出ていません: %s", joined)
	}
	if strings.Contains(joined, "受注ページを作ったメール") {
		t.Errorf("子ページを持つ記録が残っています: %s", joined)
	}
	// 子ページ（受注 PO-1）自身も通信記録の形をしているが、子が無いので出る。
	// **判定は「子が無い」だけ**なので、これは仕様どおり。
	if total < 1 {
		t.Errorf("総数が数えられていません: %d", total)
	}
}

// TestUnhandledExcludesMarkedNotNeeded は「対応：不要」の印で消えることを固定します。
// 案内やお礼のように**何も作らなくてよいメール**が永久に残らないための唯一の手動入力です。
func TestUnhandledExcludesMarkedNotNeeded(t *testing.T) {
	setupIntakeTest(t)
	putIntakeRecord(t, "000201", "000100", "案内メール", "2026-09-01T10:00:00+09:00",
		"<dt>"+HandledTag+"</dt><dd>"+HandledNotNeeded+"</dd>")
	putIntakeRecord(t, "000202", "000100", "普通のメール", "2026-09-02T10:00:00+09:00", "")

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatalf("一覧を作れません: %v", err)
	}
	for _, r := range rows {
		if r.Title == "案内メール" {
			t.Errorf("「%s：%s」が効いていません", HandledTag, HandledNotNeeded)
		}
	}
	if len(rows) != 1 || rows[0].Title != "普通のメール" {
		t.Errorf("残るべき記録が違います: %+v", rows)
	}
}

// TestUnhandledIsNewestFirst は新しい順であることを固定します
// （古い順だと、過去メールを取り込んだ直後に何年も前のものが先頭に来る）。
func TestUnhandledIsNewestFirst(t *testing.T) {
	setupIntakeTest(t)
	putIntakeRecord(t, "000201", "000100", "古い", "2024-01-01T10:00:00+09:00", "")
	putIntakeRecord(t, "000202", "000100", "新しい", "2026-09-02T10:00:00+09:00", "")

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatalf("一覧を作れません: %v", err)
	}
	if len(rows) != 2 || rows[0].Title != "新しい" {
		t.Errorf("新しい順になっていません: %+v", rows)
	}
}

// TestUnhandledExcludesOutsideInbox は、受信箱の外にある記録が混ざらないことを
// 固定します（本文へタグを書けば誰でも「通信記録の形」を作れるため）。
func TestUnhandledExcludesOutsideInbox(t *testing.T) {
	setupIntakeTest(t)
	// 受信箱の外（トップ直下）に、通信記録の形をしたページを置く。
	putIntakeRecord(t, "000301", "000000", "受信箱の外の記録", "2026-09-02T10:00:00+09:00", "")
	putIntakeRecord(t, "000202", "000100", "受信箱の中の記録", "2026-09-01T10:00:00+09:00", "")

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatalf("一覧を作れません: %v", err)
	}
	for _, r := range rows {
		if r.Title == "受信箱の外の記録" {
			t.Errorf("受信箱の外の記録が混ざっています: %+v", rows)
		}
	}
}

// setupIntakeTest は一時ディレクトリ＋インメモリDBに、トップページと受信箱を
// 用意します。受信箱は**トップ直下で題が「受信箱」**というだけで決まります
// （InboxPageID——名前が機能、という仕様）。
func setupIntakeTest(t *testing.T) {
	t.Helper()

	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	// **ファイルDBを使います。** `:memory:` は接続ごとに別のDBになり、
	// 行を読みながら中で引く処理（権限・先祖辿り）が空のDBに当たります
	// （作業引き継ぎに記録済みの罠）。
	db, err := sql.Open("sqlite", "test.db")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	for _, p := range []struct{ id, parent, title string }{
		{"000000", "", "トップ"},
		{"000100", "000000", InboxTitle},
	} {
		if err := page.WriteSidecar(p.id, page.PageMeta{
			Owner: "alice", Mode: "330", ParentID: p.parent,
		}); err != nil {
			t.Fatalf("サイドカーの作成エラー: %v", err)
		}
		if err := SyncIndex(p.id, "<h1>"+p.title+"</h1>"); err != nil {
			t.Fatalf("SyncIndexエラー: %v", err)
		}
	}
}
