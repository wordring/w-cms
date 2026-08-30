package cms

import (
	"testing"

	"w-cms/internal/database"
)

// 可変タグ（<dl data-type="tags">）の索引のテスト。
//
// 行き先は②汎用索引 vocab_index だけです——専用の page_tags テーブルは中身が
// 完全に重複し読む者がいなかったため、2026-08-30（D-1）で吸収しました。
// このファイルのテストが固定するのは吸収後も変わらない3つの約束:
// 多値（同名タグの繰り返し）が保存できること・洗い替えで蓄積しないこと・
// 「親ページID」も普通のタグとして載ること（旧ガードの撤去）。

// queryTags はページの可変タグを "名前=値" の列で返します（名前・値順）。
func queryTags(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT field, value FROM vocab_index
		 WHERE page_id = ? AND data_type = 'tags' ORDER BY field, value`, pageID)
	if err != nil {
		t.Fatalf("vocab_indexのクエリでエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f, v string
		rows.Scan(&f, &v)
		out = append(out, f+"="+v)
	}
	return out
}

// TestDuplicateTagNamesAreAllowed は、同じ名前のタグを同一ページに複数置いても
// 保存（SyncIndex）が成功し、すべて索引に入ることを検証します。
//
// タグの名前は自由語であり、担当者が2人・関連部品番号が複数といった多値属性は
// 正当な使い方です（dt の繰り返し／複数 dd。語彙モデル §5.3）。かつて専用テーブルの
// 主キーが (page_id, name) だったため、同名タグを2つ置くと UNIQUE 制約違反で保存
// そのものが 500 エラーになっていました。その回帰防止テストです。
func TestDuplicateTagNamesAreAllowed(t *testing.T) {
	setupSaveTest(t)

	const id = "000020"
	body := `<h1>多値タグのページ</h1>` +
		`<dl data-type="tags">` +
		`<dt>担当者</dt><dd>紀平</dd><dd>田中</dd>` + // 多値＝複数 dd
		`<dt>希望納期</dt><dd>2026-07-10</dd>` +
		`</dl>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("同名タグがあると同期に失敗します: %v", err)
	}

	got := queryTags(t, 20)
	want := []string{"希望納期=2026-07-10", "担当者=田中", "担当者=紀平"}
	if len(got) != len(want) {
		t.Fatalf("タグ件数が期待と異なります: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("タグの内容が期待と異なります: got %v want %v", got, want)
			break
		}
	}
}

// TestLegacyParentTagIsJustATag は、dt に「親ページID」と書いても**普通のタグとして
// 索引に載るだけ**であることを検証します。
//
// かつて page_tags には取り込まない旧ガード（legacyParentTagName）がありましたが、
// 親はサイドカー <id>.meta.json が正本で、この語を親として解釈するコードはもう
// 存在しません。値は不活性なので、コアが特定の日本語を特別扱いする理由も無くなり、
// page_tags の吸収（2026-08-30・D-1）と同時にガードごと撤去しました。
func TestLegacyParentTagIsJustATag(t *testing.T) {
	setupSaveTest(t)

	const id = "000022"
	body := `<h1>タグの除外</h1>` +
		`<dl data-type="tags">` +
		`<dt>親ページID</dt><dd>000000</dd>` +
		`<dt>発注元</dt><dd>X</dd>` +
		`</dl>`

	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	got := queryTags(t, 22)
	want := []string{"発注元=X", "親ページID=000000"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("タグの内容が期待と異なります: got %v want %v", got, want)
	}
}

// TestResyncReplacesTags は、洗い替え（DELETE→INSERT）が同名タグでも正しく働き、
// 再同期のたびに件数が増えていかないことを検証します。
func TestResyncReplacesTags(t *testing.T) {
	setupSaveTest(t)

	const id = "000021"
	body := `<h1>再同期</h1>` +
		`<dl data-type="tags">` +
		`<dt>担当者</dt><dd>紀平</dd>` + // 多値＝dt の繰り返し
		`<dt>担当者</dt><dd>田中</dd>` +
		`</dl>`

	for i := 0; i < 3; i++ {
		if err := SyncIndex(id, body); err != nil {
			t.Fatalf("%d回目の同期でエラー: %v", i+1, err)
		}
	}

	if got := queryTags(t, 21); len(got) != 2 {
		t.Errorf("再同期でタグが重複蓄積しています: got %v want 2件", got)
	}
}
