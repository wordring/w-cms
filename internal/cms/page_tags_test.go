package cms

import (
	"testing"

	"w-cms/internal/database"
)

// TestDuplicateTagNamesAreAllowed は、同じ名前のタグを同一ページに複数置いても
// 保存（SyncIndex）が成功し、すべて page_tags に入ることを検証します。
//
// タグの名前は自由語であり、担当者が2人・関連部品番号が複数といった多値属性は
// 正当な使い方です（dt の繰り返し／複数 dd。語彙モデル §5.3）。以前は page_tags の
// 主キーが (page_id, name) だったため、同名タグを2つ置くと UNIQUE 制約違反で保存
// そのものが 500 エラーになっていました（本文が保存されず、原因も分かりにくい
// 壊れ方）。その回帰防止テストです。
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

	var total int
	if err := database.DB.QueryRow(
		"SELECT COUNT(*) FROM page_tags WHERE page_id = ?", 20).Scan(&total); err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	if total != 3 {
		t.Errorf("タグ件数が期待と異なります: got %d want 3", total)
	}

	// 同名タグが両方とも保持されていること（片方が捨てられていない）
	rows, err := database.DB.Query(
		"SELECT value FROM page_tags WHERE page_id = ? AND name = ? ORDER BY value", 20, "担当者")
	if err != nil {
		t.Fatalf("担当者タグのクエリでエラー: %v", err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("Scanエラー: %v", err)
		}
		values = append(values, v)
	}
	if len(values) != 2 {
		t.Fatalf("同名タグが2件揃っていません: %v", values)
	}
	if values[0] != "田中" || values[1] != "紀平" {
		t.Errorf("同名タグの値が期待と異なります: %v", values)
	}
}

// TestSyncSkipsLegacyParentTag は、dt に「親ページID」と書かれても page_tags へ
// 取り込まれないことを検証します。ページ属性はサイドカー <id>.meta.json が正本のためです。
//
// この規則は旧 <m-tag name="親ページID">（サイドカー移行前の遺物）に由来し、
// 以前はサニタイザ側にありましたが、タグの意味を知るのは所有プラグインだけ、
// という線引きに合わせて plugin_page_tags.go へ移しました。
func TestSyncSkipsLegacyParentTag(t *testing.T) {
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

	var legacy int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ?`, 22, "親ページID").Scan(&legacy); err != nil {
		t.Fatalf("クエリでエラー: %v", err)
	}
	if legacy != 0 {
		t.Errorf("親ページIDタグが取り込まれています: %d件", legacy)
	}

	var normal int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ?`, 22, "発注元").Scan(&normal); err != nil {
		t.Fatalf("クエリでエラー: %v", err)
	}
	if normal != 1 {
		t.Errorf("通常のタグまで落ちています: %d件", normal)
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

	var total int
	if err := database.DB.QueryRow(
		"SELECT COUNT(*) FROM page_tags WHERE page_id = ?", 21).Scan(&total); err != nil {
		t.Fatalf("page_tagsのクエリでエラー: %v", err)
	}
	if total != 2 {
		t.Errorf("再同期でタグが重複蓄積しています: got %d want 2", total)
	}
}
