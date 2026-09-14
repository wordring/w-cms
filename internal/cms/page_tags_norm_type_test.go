package cms

import (
	"fmt"
	"testing"

	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// `page_tags.norm_value` の**格納クラス**を固定する試験（2026-09-15）
//
// この列は宣言型を持ちません（vocab_index.go の Schema）。得たものと代償が
// 両方とも「静か」なので、どちらも試験で押さえます——**得たもの**は数が数として
// 並ぶこと、**代償**は型を取り違えると0件になることです。
// ─────────────────────────────────────────────────────────────────────────

// tagsBody は可変タグの dl を1つ持つ本文を作ります。
func tagsBody(pairs ...string) string {
	b := `<h1>試験</h1><dl data-type="tags">`
	for i := 0; i+1 < len(pairs); i += 2 {
		b += `<dt>` + pairs[i] + `</dt><dd>` + pairs[i+1] + `</dd>`
	}
	return b + `</dl>`
}

// TestTagNormValueKeepsStorageClass は、number のタグが**数として**入り、
// それ以外が文字列のまま入ることを固定します。
//
// 宣言型 `TEXT` のままだと数まで文字列に化けます。そうなっても画面は何も
// 言わない（比較が辞書順になるだけ）ので、`typeof` で直に見ます。
func TestTagNormValueKeepsStorageClass(t *testing.T) {
	setupSaveTest(t)

	// `金額`・`数量` は number、`客先` は text（config/settings.json の推論辞書）。
	if err := SyncIndex("000080", tagsBody(
		"金額", "8000",
		"数量", "12.5",
		"客先", "南北スポーツ機械",
	)); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, want string }{
		{"金額", "integer"}, // 整数は整数の格納クラス（文字へ直しても "8000.0" にならない）
		{"数量", "real"},
		{"客先", "text"},
	} {
		var got string
		if err := database.DB.QueryRow(
			`SELECT typeof(norm_value) FROM page_tags WHERE page_id = 80 AND name = ?`,
			c.name).Scan(&got); err != nil {
			t.Fatalf("%s の読み出しに失敗: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s の格納クラスが %q です（期待 %q）——宣言型が戻っていませんか",
				c.name, got, c.want)
		}
	}
}

// TestTagNormValueSortsNumerically は、数が**辞書順ではなく数の順**に並ぶことを
// 固定します。これがこの変更で得たものそのものです。
//
// 宣言 `TEXT` では `"8000" < "900"` になります（実測）。
func TestTagNormValueSortsNumerically(t *testing.T) {
	setupSaveTest(t)

	for i, v := range []string{"8000", "900", "12.5"} {
		if err := SyncIndex(pageIDOf(81+i), tagsBody("金額", v)); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := database.DB.Query(
		`SELECT norm_value FROM page_tags WHERE name = '金額' ORDER BY norm_value`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []float64
	for rows.Next() {
		var f float64
		if err := rows.Scan(&f); err != nil {
			t.Fatalf("数として読めません: %v", err)
		}
		got = append(got, f)
	}
	want := []float64{12.5, 900, 8000}
	if len(got) != len(want) {
		t.Fatalf("件数が合いません: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("辞書順に並んでいます: %v（期待 %v）", got, want)
		}
	}
}

// TestPagesByTagLooseFindsNumberTag は、**書いて引く輪が閉じている**ことを
// 固定します。
//
// 読み手が裸の文字列を渡すと、ここが静かに0件になります——番人はこの1件です。
func TestPagesByTagLooseFindsNumberTag(t *testing.T) {
	setupSaveTest(t)

	if err := SyncIndex("000085", tagsBody("金額", "8,000")); err != nil {
		t.Fatal(err)
	}

	// 生の値は `8,000`、畳んだ値は数の 8000。桁区切りを外した形でも引けること。
	ids, err := PagesByTagLoose(database.DB, "金額", "8000")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 85 {
		t.Errorf("number のタグが引けません: %v"+
			"（読み手が tagLookupBind を通っていないと、ここが静かに空になります）", ids)
	}
}

// TestTagNormValueMismatchIsSilentlyEmpty は、**代償のほう**を固定します。
//
// 型を取り違えて束ねると、SQLite はエラーを返さず0件を返します。これは
// 「片付いた顔をして効かない」形の最たるもので、このプロジェクトが何度も
// 踏んできました。**気づけるように試験で見えるところへ置いておきます**
// ——将来ここが1件になったら、それは列に宣言型が戻った合図です。
func TestTagNormValueMismatchIsSilentlyEmpty(t *testing.T) {
	setupSaveTest(t)

	if err := SyncIndex("000086", tagsBody("金額", "8000")); err != nil {
		t.Fatal(err)
	}

	// 裸のSQLへ**文字列**を渡す（＝読み手が型を知らないときに起きること）。
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE name = '金額' AND norm_value = ?`,
		"8000").Scan(&n); err != nil {
		t.Fatalf("問い合わせ自体は成功するはずです: %v", err)
	}
	if n != 0 {
		t.Errorf("文字列で引けてしまいました（%d件）——列に宣言型が戻っていませんか。"+
			"戻っていると数の比較が辞書順になります", n)
	}

	// 同じ問いを**数**で渡せば当たる。エラーではなく「型違い」だったことの裏づけ。
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE name = '金額' AND norm_value = ?`,
		int64(8000)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("数で渡しても引けません: %d件", n)
	}
}

// pageIDOf は試験用に6桁のページIDを作ります。
func pageIDOf(n int) string { return fmt.Sprintf("%06d", n) }
