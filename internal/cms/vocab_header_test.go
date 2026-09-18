package cms

import (
	"testing"

	"w-cms/internal/database"
)

// TestPlainDLIsNotIndexed は、**素の定義リストがDBに入らない**ことを固定します
// （2026-09-18 ユーザー決定:「素の定義リストはDBから外しましょう。問題が出てから
// 再検討しましょう」）。
//
// それまで機能見出しの節の中の素の `dl` は**業務ブロックのヘッダ**として
// `vocab_index` に入っていました。やめた理由は3つ（`vocab_index.go` の `OnElement`）:
//
//   - **見た目がタグと同じで振る舞いが違う**（素の `<dl>` と `<dl data-type="tags">`）
//   - **横断検索の口（`PagesByTag`）が読むのは `page_tags`** で、いちばん検索したい値が
//     検索の口を持たない表に入っていた
//   - ヘッダは「下の明細表の見出し」の役目だが、**1文書＝1ページ**ならページのタグで足りる
//
// いまDBに入るのは**タグと表だけ**です。この番人が落ちたら、素の文書が黙って
// 索引に入り始めたということです。
func TestPlainDLIsNotIndexed(t *testing.T) {
	seedOrderPages(t, "000071")
	body := `<section data-type="client-order"><dl>` +
		`<dt>発注書番号</dt><dd>PO-7</dd><dt>発注元</dt><dd>得意先X</dd></dl></section>`
	if err := SyncIndex("000071", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	for _, q := range []struct{ table, sql string }{
		{"vocab_index", `SELECT COUNT(*) FROM vocab_index WHERE page_id = 71`},
		{"page_tags", `SELECT COUNT(*) FROM page_tags WHERE page_id = 71`},
	} {
		var n int
		if err := database.DB.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("素の定義リストが %s に入りました: %d行", q.table, n)
		}
	}
}

// TestTagsAreIndexedWithType は、**タグなら索引に入り、型の正規化も効く**ことを
// 固定します（素の dl との対比）。`発注日` は設定で `date` 型なので、畳んだ値が
// ISO になります——**範囲で引く口を足すときここが効きます**。
func TestTagsAreIndexedWithType(t *testing.T) {
	seedOrderPages(t, "000072")
	if err := SyncIndex("000072", clientOrderHTML("PO-7", "得意先X", "PART-X")); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	get := func(name string) (value, norm string) {
		t.Helper()
		if err := database.DB.QueryRow(
			`SELECT value, COALESCE(norm_value,'') FROM page_tags WHERE page_id = 72 AND name = ?`,
			name).Scan(&value, &norm); err != nil {
			t.Errorf("タグの索引に %q がありません: %v", name, err)
		}
		return value, norm
	}
	if v, _ := get("発注書番号"); v != "PO-7" {
		t.Errorf("発注書番号が索引と違います: %q", v)
	}
	if v, _ := get("発注元"); v != "得意先X" {
		t.Errorf("発注元が索引と違います: %q", v)
	}
	if v, norm := get("発注日"); v != "2026-08-20" || norm != "2026-08-20" {
		t.Errorf("発注日が日付として正規化されていません: 生=%q 畳んだ値=%q", v, norm)
	}
	// 明細は表のまま（行が並ぶものは表が正しい）。
	if got := countVocabDataRows(t, 72, "client-order-items"); got != 1 {
		t.Errorf("明細の行数が違います: %d", got)
	}
}
