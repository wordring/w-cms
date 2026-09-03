package cms

import (
	"testing"
)

// 子ページの並び順のテスト（2026-09-03）。
//
// ユーザー:「子ページの一覧が良い順番に並んでいないという問題があります」。
// それまでは作成順（ID順）で、これは「いつ作ったか」であって「どう並んで
// ほしいか」ではありませんでした——メールを2回に分けて取り込んだだけで
// `2026年 → 2024年 → 2025年` になりました。

// TestSortChildrenFallsBackToTitle は、**キーが無くても題で正しく並ぶ**ことを
// 固定します。年・月フォルダはこれだけで直ります（既存ページを触らずに済む）。
func TestSortChildrenFallsBackToTitle(t *testing.T) {
	pages := []PageSummary{
		{ID: "010165", Title: "2026年"},
		{ID: "010219", Title: "2024年"},
		{ID: "010320", Title: "2025年"},
	}
	sortChildren(pages)
	for i, want := range []string{"2024年", "2025年", "2026年"} {
		if pages[i].Title != want {
			t.Errorf("%d番目が %q ではなく %q です", i, want, pages[i].Title)
		}
	}
}

// TestSortChildrenUsesKey は、並び順キーが題より優先されることを固定します。
// メールは件名ではなく**届いた順**に並ぶ必要があります。
func TestSortChildrenUsesKey(t *testing.T) {
	pages := []PageSummary{
		{ID: "000002", Title: "あ件名", SortKey: "2026-03-14T18:00:00+09:00"},
		{ID: "000001", Title: "ん件名", SortKey: "2026-03-14T09:00:00+09:00"},
	}
	sortChildren(pages)
	if pages[0].Title != "ん件名" {
		t.Errorf("届いた順になっていません: %+v", pages)
	}
}

// TestSortChildrenKeyedComesFirst は、キーのあるものと無いものが混ざったとき
// **キーのあるほうが先**であることを固定します——人が明示的に位置を決めたものを、
// 既定の並びより後ろへ追いやらないため。
func TestSortChildrenKeyedComesFirst(t *testing.T) {
	pages := []PageSummary{
		{ID: "000001", Title: "あ"},
		{ID: "000002", Title: "ん", SortKey: "0100"},
	}
	sortChildren(pages)
	if pages[0].Title != "ん" {
		t.Errorf("キーのあるページが先に来ていません: %+v", pages)
	}
}

// TestSortChildrenIsStable は、キーも題も同じときIDで決まることを固定します
// （並びが呼び出しごとに変わらない）。
func TestSortChildrenIsStable(t *testing.T) {
	pages := []PageSummary{
		{ID: "000009", Title: "同じ"},
		{ID: "000003", Title: "同じ"},
	}
	sortChildren(pages)
	if pages[0].ID != "000003" {
		t.Errorf("IDで決まっていません: %+v", pages)
	}
}
