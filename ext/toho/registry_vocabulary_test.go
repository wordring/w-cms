package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
)

// TestRegistryAgreesWithVocabulary は、**登録（表の種類の列の宣言）と設定の語彙が、
// どの列でも同じ型・同じ選択肢を言っている**ことを固定します（2026-09-26・DBの日本語化 4-2）。
//
// ⚠ **いまは2つの正本が並んでいます**——エディタと表の写し（`data/tables.db`）は語彙
// （`cms.ColumnWord`）を読み、縦持ちの索引（`vocab_index`）はまだ登録を読みます。4-5 で
// 登録を消すまで、**型を変えるときは両方**を直す決まりでした（4-1）。
//
// ⚠ **守られていませんでした**——4-2 に入る前に数えると **5か所** ずれていました:
// 受注明細の `納期`（登録は `date` のまま・09-23 に語彙だけ `text` へ）と `単位`
// （選択肢が2つのまま）、見積もりの `単位`（`text`）、改訂明細の `図面番号` と検査記録の
// `品番`（`text`・語彙は `code`）。**エラーは出ません**——エディタは `最短納期` を薄赤にし、
// 2つの DB は同じ列を別の型で畳んでいました。
//
// 読むのは本物の `config/settings.json`（`TestMain`・`settings_repo_test.go`）。
// ⚠ `Suggest`（候補の出どころ）は語彙に無いので比べません——4-5 までに置き場が要ります。
func TestRegistryAgreesWithVocabulary(t *testing.T) {
	checked := 0
	for _, d := range cms.VocabDefs() {
		for _, c := range d.Columns {
			w := cms.ColumnWord(d.DisplayName, c.Label)
			checked++
			if w.Type != c.Type {
				t.Errorf("%s の %s: 登録は %s、語彙は %s（変えるときは両方）",
					d.DisplayName, c.Label, c.Type, w.Type)
				continue
			}
			if strings.Join(w.Values, "・") != strings.Join(c.Enum, "・") {
				t.Errorf("%s の %s: 選択肢が違います——登録 %v／語彙 %v（変えるときは両方）",
					d.DisplayName, c.Label, c.Enum, w.Values)
			}
		}
	}
	// ⚠ **空振りしないこと**——登録が読めていなければ、比べる列が0で緑になります。
	if checked < 20 {
		t.Fatalf("比べた列が %d しかありません（登録が読めていない？）", checked)
	}
}
