package cms

import (
	"database/sql"
	"testing"

	"w-cms/internal/cms/page"
)

// 発注書番号（order_no）はページ内の識別子であって、サイト全体の主キーではありません。
// UNIQUE がページ横断だったころ、Sync の
//   ON CONFLICT(order_no) DO UPDATE SET ... page_id = excluded.page_id
// によって、**同じ番号を使った後勝ちのページが先のページの受注を奪って**いました。
// 明細テーブルは page_id を持たず order_no だけで結び付いていたため、洗い替えの
// DELETE も取りこぼし、両ページの明細が同じ番号の下に混ざります。例外もログも出ません。
//
// 番号が空のときはさらに広く、SQLite の UNIQUE は '' を重複扱いするので
// **空番号の発注書はサイト全体で1件しか持てません**（番号を書き忘れただけで起きる）。

// clientOrderHTML は受注ページの本文（ヘッダ dl ＋ 明細1行）を組み立てます。
func clientOrderHTML(orderNo, client, itemID string) string {
	return `<section data-type="client-order"><dl>` +
		`<dt>発注書番号</dt><dd>` + orderNo + `</dd>` +
		`<dt>発注元</dt><dd>` + client + `</dd>` +
		`<dt>発注日</dt><dd>2026-08-20</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>` + itemID + `</td><td>部品</td><td>100</td><td>1</td><td></td></tr>` +
		`</tbody></table></section>`
}

// countOrdersOf は指定ページの受注ヘッダ・明細の件数を索引から返します。
// ヘッダは <section data-type="client-order"> のブロック数、明細は
// <table data-type="client-order-items"> のデータ行数です。
func countOrdersOf(t *testing.T, pageID int) (headers, items int) {
	t.Helper()
	return countVocabBlocks(t, pageID, "client-order"),
		countVocabDataRows(t, pageID, "client-order-items")
}

// seedOrderPages は受注ページ2枚分のサイドカーとDB行を用意します。
func seedOrderPages(t *testing.T, ids ...string) {
	t.Helper()
	setupUploadTest(t, ids[0], page.PageMeta{Owner: "alice", Mode: "330"})
	for _, id := range ids[1:] {
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330"}); err != nil {
			t.Fatalf("WriteSidecar(%s)エラー: %v", id, err)
		}
	}
}

// TestOrderNoIsScopedToPage は、同じ発注書番号を別のページで使っても
// 互いの受注を奪わないことを固定します。
func TestOrderNoIsScopedToPage(t *testing.T) {
	seedOrderPages(t, "000031", "000032")

	// ページ31とページ32が**同じ番号** PO-1 を使う（手入力・ブロックのコピーで普通に起きる）
	if err := SyncIndex("000031", clientOrderHTML("PO-1", "得意先A", "PART-A")); err != nil {
		t.Fatalf("SyncIndex(31)エラー: %v", err)
	}
	if err := SyncIndex("000032", clientOrderHTML("PO-1", "得意先B", "PART-B")); err != nil {
		t.Fatalf("SyncIndex(32)エラー: %v", err)
	}

	if h, i := countOrdersOf(t, 31); h != 1 || i != 1 {
		t.Errorf("ページ31の受注が奪われました: ヘッダ=%d 明細=%d (期待 1/1)", h, i)
	}
	if h, i := countOrdersOf(t, 32); h != 1 || i != 1 {
		t.Errorf("ページ32の受注が入っていません: ヘッダ=%d 明細=%d (期待 1/1)", h, i)
	}

	// ページ31を再保存しても、ページ32の明細が消えないこと（洗い替えの取りこぼし）
	if err := SyncIndex("000031", clientOrderHTML("PO-1", "得意先A", "PART-A")); err != nil {
		t.Fatalf("SyncIndex(31・再)エラー: %v", err)
	}
	if h, i := countOrdersOf(t, 32); h != 1 || i != 1 {
		t.Errorf("ページ31の再保存でページ32の受注が消えました: ヘッダ=%d 明細=%d", h, i)
	}

}

// TestEmptyOrderNoDoesNotCollide は、番号が空でもページ同士が衝突しないことを固定します。
// SQLite の UNIQUE は ” を重複扱いするので、横断UNIQUE のままだと
// 「番号を書き忘れた発注書」がサイト全体で1件しか持てませんでした。
func TestEmptyOrderNoDoesNotCollide(t *testing.T) {
	seedOrderPages(t, "000041", "000042")

	if err := SyncIndex("000041", clientOrderHTML("", "得意先A", "PART-A")); err != nil {
		t.Fatalf("SyncIndex(41)エラー: %v", err)
	}
	if err := SyncIndex("000042", clientOrderHTML("", "得意先B", "PART-B")); err != nil {
		t.Fatalf("SyncIndex(42)エラー: %v", err)
	}

	if h, _ := countOrdersOf(t, 41); h != 1 {
		t.Errorf("番号が空のページ41の受注が消えました: ヘッダ=%d", h)
	}
	if h, _ := countOrdersOf(t, 42); h != 1 {
		t.Errorf("番号が空のページ42の受注が入っていません: ヘッダ=%d", h)
	}
}

// TestDriftedSchemaTablesDetectsOldSchema は、旧定義のまま残ったテーブルを
// 検出できることを固定します。
//
// ApplySchema は CREATE TABLE IF NOT EXISTS を流すだけなので、既に在るテーブルの
// 定義変更は反映されません。検出できないと、起動は成功して保存だけが
// "no such column" で 500 になる、という気づきにくい壊れ方をします。
func TestDriftedSchemaTablesDetectsOldSchema(t *testing.T) {
	db := freshDB(t)

	// 旧定義（norm_num を持たないころの汎用索引）で作る。
	// 硬いドメイン表はもう無いので（D-1）、いま宣言されているテーブルで試す。
	if _, err := db.Exec(`CREATE TABLE vocab_index (
		page_id INTEGER,
		data_type TEXT,
		block_no INTEGER,
		block_id TEXT,
		row_no INTEGER,
		field TEXT,
		value TEXT,
		norm_value TEXT,
		FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
	);`); err != nil {
		t.Fatalf("旧テーブル作成エラー: %v", err)
	}

	drifted := DriftedSchemaTables(db)
	found := false
	for _, name := range drifted {
		if name == "vocab_index" {
			found = true
		}
	}
	if !found {
		t.Errorf("旧定義の vocab_index を検出できません: %v", drifted)
	}

	// 現在の宣言で作り直したら検出しないこと（毎起動で再構築が走らない）
	if _, err := db.Exec(`DROP TABLE vocab_index`); err != nil {
		t.Fatalf("DROPエラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("ApplySchemaエラー: %v", err)
	}
	if drifted := DriftedSchemaTables(db); len(drifted) != 0 {
		t.Errorf("現在の宣言で作ったのにずれと判定されました: %v", drifted)
	}
}

// freshDB は空のインメモリDBを返します。
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
