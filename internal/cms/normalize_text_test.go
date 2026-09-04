package cms

import (
	"testing"

	"w-cms/internal/database"
)

// TestCodeTagIsFoundThroughVariants は、`code` 型のタグが**揺れた形でも引ける**ことを
// 索引ごしに確かめます（案2の目的そのもの）。
//
// 保存された値は生のまま（`P200-911-03A`）で、引く側が小文字・全角・アンダースコアで
// 打っても当たること——これが「ワンノートはハイフンで区切られた図面名称を検索できない」への答えです。
func TestCodeTagIsFoundThroughVariants(t *testing.T) {
	setupSaveTest(t)

	const id = "000061"
	body := `<dl data-type="tags"><dt>図面番号</dt><dd>P200-911-03A</dd></dl>`
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 生の完全一致は、打った通りのときだけ当たる。
	if ids, err := PagesByTag(database.DB, "図面番号", "P200-911-03A"); err != nil || len(ids) != 1 {
		t.Fatalf("生の一致で引けません: %v %v", ids, err)
	}
	if ids, _ := PagesByTag(database.DB, "図面番号", "p200_911_03a"); len(ids) != 0 {
		t.Errorf("生の一致が揺れた形で当たってしまいます: %v", ids)
	}

	// 畳んだ一致は、実データで見た揺れをすべて吸収する。
	for _, variant := range []string{
		"P200-911-03A",
		"p200-911-03a",   // 英字の大小
		"P200_911_03A",   // アンダースコア
		"Ｐ２００-９１１-０３Ａ",   // 全角
		"P200ー911ー03A",   // 長音で打った
		"  P200-911-03A", // 前後の空白
	} {
		ids, err := PagesByTagLoose(database.DB, "図面番号", variant)
		if err != nil {
			t.Fatalf("PagesByTagLooseエラー(%q): %v", variant, err)
		}
		if len(ids) != 1 || ids[0] != 61 {
			t.Errorf("%q で引けません: %v", variant, ids)
		}
	}

	// **区切りの有無は畳まない**——別の部品を1つにするほうが害が大きい。
	if ids, _ := PagesByTagLoose(database.DB, "図面番号", "P20091103A"); len(ids) != 0 {
		t.Errorf("区切りを外した形まで当たってしまいます: %v", ids)
	}
}

// TestTextTagKeepsLongVowel は、**一般のテキストで長音が壊れない**ことを固定します。
// ここが崩れると `レーザーマックス` が `レ-ザ-マックス` になり、会社名で引けなくなります。
// （`code` と `text` を分けた理由そのものなので、テストで留めておきます）
func TestTextTagKeepsLongVowel(t *testing.T) {
	setupSaveTest(t)

	const id = "000062"
	body := `<dl data-type="tags"><dt>差出人</dt><dd>レーザーマックス</dd></dl>`
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	var norm string
	err := database.DB.QueryRow(
		`SELECT norm_value FROM vocab_index WHERE page_id = 62 AND field = '差出人'`).Scan(&norm)
	if err != nil {
		t.Fatalf("索引の読み出しエラー: %v", err)
	}
	if norm != "レーザーマックス" {
		t.Errorf("長音が壊れています: %q", norm)
	}
}

// TestNormalizeDateWidened は、D-3 の一覧（【一覧】日付形式と数詞 §4）が挙げた
// 「読めない形」のうち、和暦とドット区切りを埋めたことを固定します。
func TestNormalizeDateWidened(t *testing.T) {
	cases := map[string]string{
		"2026.06.15": "2026-06-15",
		"2026.6.15":  "2026-06-15",
		"令和8年6月15日":  "2026-06-15",
		"R8.6.15":    "2026-06-15",
		"令和元年5月1日":   "2019-05-01",
		"平成31年4月30日": "2019-04-30",
	}
	for raw, want := range cases {
		got, ok := NormalizeValue(ColDate, raw)
		if !ok || got != want {
			t.Errorf("NormalizeValue(date, %q) = (%q, %v), want %q", raw, got, ok, want)
		}
	}
	// 読めない形は ok=false のまま（生が残る）。
	for _, raw := range []string{"9月末", "6/15", "2024-09", "来週"} {
		if _, ok := NormalizeValue(ColDate, raw); ok {
			t.Errorf("%q が読めてしまいます", raw)
		}
	}
}

// TestNormalizeNumberUnits は助数詞・単位の扱いを固定します。
//
// **助数詞は列挙しません**——number 型と宣言された升目なら、数のうしろは単位と
// 読んでよいためです。一方で **桁を変える接尾辞（万円・千円）だけは列挙が要ります**
// ——`1.2万円` を 1.2 と読むと1万分の1の見積になり、黙って通ると害が大きい。
func TestNormalizeNumberUnits(t *testing.T) {
	cases := map[string]string{
		"5個":     "5",
		"12.5mm": "12.5",
		"892rpm": "892",
		"3500円":  "3500",
		"1.2万円":  "12000",
		"3千円":    "3000",
		"¥8,000": "8000",
	}
	for raw, want := range cases {
		got, ok := NormalizeValue(ColNumber, raw)
		if !ok || got != want {
			t.Errorf("NormalizeValue(number, %q) = (%q, %v), want %q", raw, got, ok, want)
		}
	}
	// 頭が数でないものは読まない（`一式`・`約8000`）。
	for _, raw := range []string{"一式", "約8000", "Φ410"} {
		if _, ok := NormalizeValue(ColNumber, raw); ok {
			t.Errorf("%q が読めてしまいます", raw)
		}
	}
}

// TestCanonicalForIngest は「機械が本文を書き起こすときは正規形で書く」（D-3）を
// 固定します。**text / code は書かれたまま**——機械が畳んで書き換えると、
// 原本と見比べたときに食い違います。
func TestCanonicalForIngest(t *testing.T) {
	cases := []struct{ field, raw, want string }{
		{"発注日", "令和8年6月15日", "2026-06-15"},
		{"発注日", "2026/6/15", "2026-06-15"},
		{"単価", "¥8,000", "8000"},
		{"数量", "5個", "5"},
		{"発注日", "9月末", "9月末"},                    // 読めなければ生のまま
		{"図面番号", "p200_911_03a", "p200_911_03a"}, // code は畳まない
		{"発注元", "レーザーマックス", "レーザーマックス"},          // text も畳まない
	}
	for _, c := range cases {
		if got := CanonicalForIngest(c.field, c.raw); got != c.want {
			t.Errorf("CanonicalForIngest(%q, %q) = %q, want %q", c.field, c.raw, got, c.want)
		}
	}
}
