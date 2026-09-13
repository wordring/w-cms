package cms

// タグと業務ブロックを別の表に分けた、その境目の試験（2026-09-13）。
//
// ユーザー:「HTMLのTableとタグはDBのTableを分けたいと思います」。
//
// 2026-09-06 の考察では「分けない」と結論しましたが、**根拠のうち2つが崩れました**:
//
//   - 「タグは付箋であってブロックではない」→ アドレス帳がタグになりました
//     （連絡先・担当者の実体は `メールアドレス`・`電話番号`・`役職` のタグ）
//   - 「読む側で `data_type='tags'` を足せば足りる（C-1）」→ 足りませんでした。
//     決定から1週間で13箇所が絞り込み無しのまま残り、2026-09-13 には3箇所増えました
//     （連絡先の実装で、書いた本人が3回忘れた）
//
// ここで固定するのは「**間違った問いが書けなくなった**」ことです。

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// TestTagsAndBlocksGoToDifferentTables は、書き込み先が分かれることを固定します。
//
// **書き手は1か所**（`insertVocabEntry`）なので、ここが守られていれば
// タグが業務ブロックの表へ紛れることも、その逆も起きません。
func TestTagsAndBlocksGoToDifferentTables(t *testing.T) {
	setupSaveTest(t)

	// 同じページに、タグと業務ブロックの両方を置く。
	// **わざと同じ名前（`図面番号`）を両方に入れます**——分ける前は、この形で
	// 片方を引いたつもりが両方取れました（C-1 が塞ごうとしていた穴）。
	//
	// 業務ブロックはコアのレジストリにある形式を使います（板金部の `図面` は
	// 拡張側の宣言なので、コアの試験では索引に載りません）。
	body := `<h1>部品</h1>` +
		`<dl data-type="tags"><dt>図面番号</dt><dd>タグ側</dd></dl>` +
		`<table data-type="inspection-record">` +
		`<tr><th>図面番号</th></tr><tr><td>ブロック側</td></tr></table>`
	if err := SyncIndex("000070", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var tagValue string
	if err := database.DB.QueryRow(
		`SELECT value FROM page_tags WHERE page_id = 70 AND name = '図面番号'`,
	).Scan(&tagValue); err != nil {
		t.Fatalf("page_tags から読めません: %v", err)
	}
	if tagValue != "タグ側" {
		t.Errorf("page_tags に業務ブロックの値が入っています: %q", tagValue)
	}

	var blockValue string
	if err := database.DB.QueryRow(
		`SELECT value FROM vocab_index WHERE page_id = 70 AND field = '図面番号'`,
	).Scan(&blockValue); err != nil {
		t.Fatalf("vocab_index から読めません: %v", err)
	}
	if blockValue != "ブロック側" {
		t.Errorf("vocab_index にタグの値が入っています: %q", blockValue)
	}

	// **タグの表に data_type は無い**（入るものが全部タグだから）。
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = 70 AND data_type = 'tags'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("タグが vocab_index にも残っています: %d 行", n)
	}
}

// TestPageTagsClearedOnResync は、**片方だけ洗い残さない**ことを固定します。
//
// 洗い流しは `OnPageStart` の1か所ですが、表が2つになったので消すのも2回です。
// 片方を忘れると、本文から消したタグが索引に居残り、逆引きが幽霊を返します。
func TestPageTagsClearedOnResync(t *testing.T) {
	setupSaveTest(t)

	if err := SyncIndex("000071",
		`<h1>記録</h1><dl data-type="tags"><dt>差出人</dt><dd>小澤</dd></dl>`); err != nil {
		t.Fatal(err)
	}
	// 本文からタグを消して、もう一度同期する。
	if err := SyncIndex("000071", `<h1>記録</h1><p>消しました</p>`); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = 71`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("本文から消したタグが索引に残っています: %d 行", n)
	}
}

// TestPagesByTagReadsTagTableOnly は、逆引きが**タグだけ**を見ることを固定します。
//
// 分ける前は `data_type='tags'` を書き忘れると業務ブロックの列まで拾えました。
// いまは表そのものが違うので、書き忘れようがありません。
func TestPagesByTagReadsTagTableOnly(t *testing.T) {
	setupSaveTest(t)

	// 業務ブロックにだけ `発注書番号` があるページ。
	body := `<h1>受注</h1><section><h2>受注</h2><dl>` +
		`<dt>発注書番号</dt><dd>250401-203</dd></dl></section>`
	if err := SyncIndex("000072", body); err != nil {
		t.Fatal(err)
	}

	ids, err := PagesByTag(database.DB, "発注書番号", "250401-203")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("業務ブロックの列をタグの逆引きが拾っています: %v", ids)
	}
}

// TestNewTableTriggersRebuild は、**表が増えたら作り直す**ことを固定します。
//
// `ApplySchema` は `CREATE TABLE IF NOT EXISTS` を流すだけなので、新しい表は
// **空のまま**できます。既に索引済みのページは本文を読み直さないと入りません
// ——2026-09-13 に `page_tags` を足したとき、これでタグ88行が古い表に取り残されました
// （画面は無言で空になる、いちばん気づきにくい壊れ方）。
func TestNewTableTriggersRebuild(t *testing.T) {
	setupSaveTest(t)
	if err := SyncIndex("000073",
		`<h1>記録</h1><dl data-type="tags"><dt>差出人</dt><dd>小澤</dd></dl>`); err != nil {
		t.Fatal(err)
	}

	// 「表を足した直後」を作る——page_tags だけ落とす。
	if _, err := database.DB.Exec(`DROP TABLE page_tags`); err != nil {
		t.Fatal(err)
	}
	drifted := DriftedSchemaTables(database.DB)
	if len(drifted) == 0 {
		t.Fatal("表が増えたのに作り直しの合図が出ません（新しい表が空のまま残ります）")
	}
	for _, name := range drifted {
		if strings.HasPrefix(name, "idx_") {
			t.Errorf("索引（CREATE INDEX）を表と取り違えています: %q", name)
		}
	}
}

// TestIndexStatementsAreNotTables は、`CREATE INDEX` を表と数えないことを固定します。
//
// 数えてしまうと `sqlite_master` の `type='table'` で見つからず、**起動のたびに
// 再構築が走ります**（2026-09-13 にログで気づきました）。
func TestIndexStatementsAreNotTables(t *testing.T) {
	if got := createdTableName(
		"CREATE INDEX IF NOT EXISTS idx_page_tags_field ON page_tags(name);"); got != "" {
		t.Errorf("CREATE INDEX を表として拾っています: %q", got)
	}
	if got := createdTableName(
		"CREATE TABLE IF NOT EXISTS page_tags (page_id INTEGER);"); got != "page_tags" {
		t.Errorf("CREATE TABLE の表名が取れません: %q", got)
	}
}
