package cms

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

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
		"<dt>" + DirectionTag + "</dt><dd>" + DirectionIn + "</dd>" +
		"<dt>" + ChannelTag + "</dt><dd>メール</dd>" +
		"<dt>受信日時</dt><dd>" + received + "</dd>" +
		"<dt>差出人</dt><dd>潮崎 光俊</dd>" +
		extraTags +
		"</dl>"
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

// TestUnhandledKeepsRecordsWithChildren は、**子ページを作っても記録が残る**ことを
// 固定します。
//
// もともとは逆でした——「メールを読んだ時点で受注ページや部品ページが作られるので
// 問題ない」（2026-09-03 ユーザー）として、子ページが在ること自体を「手を付けた」の
// 印にしていました。**同じ方が 2026-09-05 に撤回**しています——「**一つのメールに
// 複数の案件が含まれることもあるので、処理済みの判断は作業者自身がした方が良いと
// 思います**」。1通に3件あれば、受注ページを1枚作っても終わっていません。
//
// 年・月フォルダが並ばないのは、子の有無ではなく**題で見分けている**からです
// （IsDateFolderTitle——作る側の隣に置いてあるので、形を変えるなら両方を同時に直す）。
func TestUnhandledKeepsRecordsWithChildren(t *testing.T) {
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
	for _, want := range []string{"まだ見ていないメール", "受注ページを作ったメール", "受注 PO-1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q が出ていません（印を付けるまで残るはず）: %s", want, joined)
		}
	}
	if total < 3 {
		t.Errorf("総数が足りません: %d", total)
	}
}

// TestUnhandledExcludesDateFolders は、年・月フォルダが並ばないことを固定します。
// **入れ物は仕事ではありません。**
func TestUnhandledExcludesDateFolders(t *testing.T) {
	setupIntakeTest(t)
	// 通信箱 > 2026年 > 09月 > 記録、という取り込みが作る形。
	for _, f := range []struct{ id, parent, title string }{
		{"000210", "000100", "2026年"},
		{"000211", "000210", "09月"},
	} {
		if err := page.WriteSidecar(f.id, page.PageMeta{
			Owner: "alice", Mode: "330", ParentID: f.parent,
		}); err != nil {
			t.Fatalf("サイドカーの作成エラー: %v", err)
		}
		if err := SyncIndex(f.id, "<h1>"+f.title+"</h1>"); err != nil {
			t.Fatalf("SyncIndexエラー: %v", err)
		}
	}
	putIntakeRecord(t, "000212", "000211", "月フォルダの中の記録", "2026-09-02T10:00:00+09:00", "")

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatalf("一覧を作れません: %v", err)
	}
	for _, r := range rows {
		if r.Title == "2026年" || r.Title == "09月" {
			t.Errorf("年・月フォルダが並んでいます: %+v", rows)
		}
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
// （MailBoxPageID——名前が機能、という仕様）。
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
		{"000100", "000000", MailBoxTitle},
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

// TestUnhandledIncludesInternalJob は、**通信でない案件も作業待ちに並ぶ**ことを
// 固定します（2026-09-05 ユーザー:「自分が使う作業台の製造などは、どことも通信しません」）。
//
// タグを1つも持たないページでも、通信箱の下にあって子が無ければ未処理です
// ——社内発意の仕事を別の場所に置くと、受信箱と送信箱を統合した理由（1件の案件が
// 2か所に散る）がそのまま再発します。
func TestUnhandledIncludesInternalJob(t *testing.T) {
	setupIntakeTest(t)

	// 社内案件——**タグを1つも持たない**ページを通信箱の下に置くだけ。
	const id = "000401"
	if err := page.WriteSidecar(id, page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: "000100",
	}); err != nil {
		t.Fatalf("サイドカーの作成エラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>作業台を作る</h1><p>板金部の3号機の横に置く。</p>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows, total, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 100)
	if err != nil {
		t.Fatalf("UnhandledIntakesエラー: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.PageID == id {
			found = true
			if r.Channel != "" {
				t.Errorf("チャネルが無いはずです: %q", r.Channel)
			}
			if r.Received == "" {
				t.Errorf("並びの鍵が空です（受信日時が無いときは更新日時を使う）")
			}
		}
	}
	if !found {
		t.Errorf("社内案件が作業待ちに並びません（総数 %d・%d件）", total, len(rows))
	}
}

// TestUnhandledExcludesSentMailCopy は、**送信メールの控えが作業待ちに混じらない**
// ことを固定します。除くのは向きではなく `対応` の印です——控えを作った側
// （recordSentMail）が書きます。**送るという仕事はその場で終わっている**ので、
// 人に押させる意味がありません。
func TestUnhandledExcludesSentMailCopy(t *testing.T) {
	setupIntakeTest(t)

	const id = "000402"
	if err := page.WriteSidecar(id, page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: "000100",
	}); err != nil {
		t.Fatalf("サイドカーの作成エラー: %v", err)
	}
	body := `<h1>RE: お見積り依頼</h1><dl data-type="tags">` +
		"<dt>" + DirectionTag + "</dt><dd>" + DirectionOut + "</dd>" +
		"<dt>" + ChannelTag + "</dt><dd>メール</dd>" +
		"<dt>" + HandledTag + "</dt><dd>" + HandledNotNeeded + "</dd></dl>"
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 100)
	if err != nil {
		t.Fatalf("UnhandledIntakesエラー: %v", err)
	}
	for _, r := range rows {
		if r.PageID == id {
			t.Errorf("送信メールの控えが作業待ちに混じりました: %+v", r)
		}
	}
}

// TestUnhandledIncludesOutgoingCall は、**こちらからかけた電話が作業待ちに残る**ことを
// 固定します。
//
// 当初は「送信＝済んだこと」として `向き：送信` を一律に外していましたが、
// ユーザーの指摘で崩れました——「**かけた電話で受注することもあるので、処理は
// すんでいません。やはり、人間がチェックするべきでは**」（2026-09-05）。
// **向きは「どちら向きの出来事か」しか語りません。** 済んだかどうかは別の軸です。
func TestUnhandledIncludesOutgoingCall(t *testing.T) {
	setupIntakeTest(t)

	const id = "000403"
	if err := page.WriteSidecar(id, page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: "000100",
	}); err != nil {
		t.Fatalf("サイドカーの作成エラー: %v", err)
	}
	body := `<h1>株式会社トーアスポーツマシーン</h1><dl data-type="tags">` +
		"<dt>" + DirectionTag + "</dt><dd>" + DirectionOut + "</dd>" +
		"<dt>" + ChannelTag + "</dt><dd>電話</dd></dl>"
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 100)
	if err != nil {
		t.Fatalf("UnhandledIntakesエラー: %v", err)
	}
	for _, r := range rows {
		if r.PageID == id {
			return // 並んでいる＝期待どおり
		}
	}
	t.Errorf("かけた電話が作業待ちに並びません（受注しているかもしれない）")
}

// TestShortTimeConvertsToLocal は、一覧の時刻が**必ず地方時**になることを固定します。
//
// 保存は UTC（`…Z`）で正しいのですが、文字列を切り出すだけだと9時間ずれます。
// 受信日時のタグは `+09:00` を持つので気づかず、**タグの無い記録（メモ・社内案件）
// だけが UTC で並んでいました**（2026-09-05 ユーザー指摘）。
func TestShortTimeConvertsToLocal(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	want := time.Date(2026, 9, 5, 16, 13, 0, 0, jst).In(time.Local).Format("01-02 15:04")

	for _, iso := range []string{
		"2026-09-05T07:13:00Z",      // サイドカー由来（UTC）
		"2026-09-05T16:13:00+09:00", // 受信日時のタグ由来
	} {
		if got := shortTime(iso); got != want {
			t.Errorf("shortTime(%q) = %q, want %q", iso, got, want)
		}
	}
	// 読めない値は捏造せずそのまま返す。
	if got := shortTime("いつか"); got != "いつか" {
		t.Errorf("読めない値を書き換えました: %q", got)
	}
}
