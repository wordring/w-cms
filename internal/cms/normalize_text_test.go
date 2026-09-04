package cms

import (
	"testing"

	"w-cms/internal/database"
)

// TestCodeTagIsFoundThroughVariants は、`code` 型のタグが**揺れた形でも引ける**ことを
// 索引ごしに確かめます（案2の目的そのもの）。
//
// 保存された値は生のまま（`M305-822-07B`）で、引く側が小文字・全角・アンダースコアで
// 打っても当たること——これが「ワンノートはハイフンで区切られた図面名称を検索できない」への答えです。
func TestCodeTagIsFoundThroughVariants(t *testing.T) {
	setupSaveTest(t)

	const id = "000061"
	body := `<dl data-type="tags"><dt>図面番号</dt><dd>M305-822-07B</dd></dl>`
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// 生の完全一致は、打った通りのときだけ当たる。
	if ids, err := PagesByTag(database.DB, "図面番号", "M305-822-07B"); err != nil || len(ids) != 1 {
		t.Fatalf("生の一致で引けません: %v %v", ids, err)
	}
	if ids, _ := PagesByTag(database.DB, "図面番号", "p200_911_03a"); len(ids) != 0 {
		t.Errorf("生の一致が揺れた形で当たってしまいます: %v", ids)
	}

	// 畳んだ一致は、実データで見た揺れをすべて吸収する。
	for _, variant := range []string{
		"M305-822-07B",
		"m305-822-07b",   // 英字の大小
		"M305_822_07B",   // アンダースコア
		"Ｍ３０５-８２２-０７Ｂ",   // 全角
		"M305ー822ー07B",   // 長音で打った
		"  M305-822-07B", // 前後の空白
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
	if ids, _ := PagesByTagLoose(database.DB, "図面番号", "M30591103A"); len(ids) != 0 {
		t.Errorf("区切りを外した形まで当たってしまいます: %v", ids)
	}
}

// TestTextTagKeepsLongVowel は、**一般のテキストで長音が壊れない**ことを固定します。
// ここが崩れると `ひかり加工` が `レ-ザ-マックス` になり、会社名で引けなくなります。
// （`code` と `text` を分けた理由そのものなので、テストで留めておきます）
func TestTextTagKeepsLongVowel(t *testing.T) {
	setupSaveTest(t)

	const id = "000062"
	body := `<dl data-type="tags"><dt>差出人</dt><dd>ひかり加工</dd></dl>`
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	var norm string
	err := database.DB.QueryRow(
		`SELECT norm_value FROM vocab_index WHERE page_id = 62 AND field = '差出人'`).Scan(&norm)
	if err != nil {
		t.Fatalf("索引の読み出しエラー: %v", err)
	}
	if norm != "ひかり加工" {
		t.Errorf("長音が壊れています: %q", norm)
	}
}
