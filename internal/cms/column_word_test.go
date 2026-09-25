package cms

import (
	"strings"
	"testing"
)

// 列の型と選択肢——既定（列の名前ごと）と例外（表と列の組）（2026-09-25・DBの日本語化 §3.10）。
// 利用者:「デフォルトの選択肢と例外の選択肢ですね」。

// TestColumnWordPrefersTableException は、引く順が **表ごとの例外 → 列の名前の既定 → 文字列**
// であることを、リポジトリの設定ファイルで固定します。
func TestColumnWordPrefersTableException(t *testing.T) {
	saved := settings
	t.Cleanup(func() { settingsMu.Lock(); settings = saved; settingsMu.Unlock() })
	if err := loadRepoSettings(); err != nil {
		t.Fatalf("設定を読めません: %v", err)
	}

	// 既定——受注明細の `状態` は列の名前の選択肢（例外が無い）。
	if w := ColumnWord("受注明細", "状態"); w.Type != ColEnum || !contains(w.Values, "完了") || contains(w.Values, "取消") {
		t.Errorf("受注明細の状態は既定の選択肢のはず: %+v", w)
	}
	// 例外——発注明細の `状態` は表ごとの選択肢。
	if w := ColumnWord("発注明細", "状態"); w.Type != ColEnum || !contains(w.Values, "取消") || contains(w.Values, "完了") {
		t.Errorf("⚠ 発注明細の状態に例外が効いていません: %+v", w)
	}
	// 名前は正規化して照らす（前後の空白・全角）。
	if w := ColumnWord("　発注明細 ", "状態"); !contains(w.Values, "取消") {
		t.Errorf("表の名前の揺れで例外が外れています: %+v", w)
	}
	// 例外の無い列は既定へ落ちる。語彙に無い列は文字列。
	if w := ColumnWord("発注明細", "数量"); w.Type != ColNumber {
		t.Errorf("例外の無い列が既定に落ちていません: %+v", w)
	}
	if w := ColumnWord("発注明細", "見たことのない列"); w.Type != ColText {
		t.Errorf("語彙に無い列は文字列のはず: %+v", w)
	}
	// ⚠ **写しを返す**——受け取った側が書き換えても設定は壊れない。
	w := ColumnWord("発注明細", "状態")
	w.Values[0] = "書き換えた"
	if ColumnWord("発注明細", "状態").Values[0] == "書き換えた" {
		t.Error("⚠ 設定の選択肢を直に返しています")
	}
}

// TestTableVocabularyIsValidated は、表ごとの例外も vocabulary と同じ規則で検査されることを
// 固定します（選択肢の無い enum・知らない型は起動で止める）。
func TestTableVocabularyIsValidated(t *testing.T) {
	for _, c := range []struct {
		name string
		tv   map[string]map[string]VocabWord
		want string
	}{
		{"選択肢の無い enum", map[string]map[string]VocabWord{"発注明細": {"状態": {Type: ColEnum}}}, "選択肢がありません"},
		{"知らない型", map[string]map[string]VocabWord{"発注明細": {"状態": {Type: "color"}}}, "未知の列型"},
		{"空の表の名前", map[string]map[string]VocabWord{" ": {"状態": {Type: ColText}}}, "空の表の名前"},
	} {
		s := Settings{Vocabulary: map[string]VocabWord{}, TableVocabulary: c.tv}
		err := s.validate("settings.json")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v（%q を含む誤りのはず）", c.name, err, c.want)
		}
	}
}

// TestTablesDBUsesTableException は、表の写しの値が**表ごとの例外の型**で正規化されることを
// 固定します（同じ列の名前でも、表によって型が違ってよい）。
func TestTablesDBUsesTableException(t *testing.T) {
	newTestFileDB(t)
	tdb := newTestTablesDB(t)
	saved := settings
	t.Cleanup(func() { settingsMu.Lock(); settings = saved; settingsMu.Unlock() })
	withSettings(t, func(s *Settings) {
		s.TableVocabulary = map[string]map[string]VocabWord{"試験表": {"番号": {Type: ColNumber}}}
	})
	body := `<h1>p</h1>` +
		`<table><caption>試験表</caption><tbody><tr><th>番号</th></tr><tr><td>０１２</td></tr></tbody></table>` +
		`<table><caption>別の表</caption><tbody><tr><th>番号</th></tr><tr><td>０１２</td></tr></tbody></table>`
	if err := SyncIndex("000080", body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	// 試験表の `番号` は例外で数 → 12（数として）。別の表の `番号` は文字 → NFKC の "012"。
	if got := queryStrings(t, tdb, `SELECT typeof(番号) || ':' || 番号 FROM 試験表`); strings.Join(got, "") != "integer:12" {
		t.Errorf("例外の型が効いていません: %v", got)
	}
	if got := queryStrings(t, tdb, `SELECT typeof(番号) || ':' || 番号 FROM 別の表`); strings.Join(got, "") != "text:012" {
		t.Errorf("⚠ 例外がほかの表にまで効いています: %v", got)
	}
}
