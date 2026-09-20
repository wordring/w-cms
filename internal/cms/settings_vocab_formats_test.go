package cms

import (
	"strings"
	"testing"
)

// **運用者が `settings.json` から表の形式を足せます**（2026-09-20 ユーザー決定）。
//
// ⚠ **「登録された語彙だけDBに入る」と対の決定**です（同日）。それまでは「登録されて
// いなくても索引に載る」が運用者の抜け道でしたが、そこを塞いだので**正面の道を
// 開けました**——塞いだだけだと、**運用者が自分の表をDBに入れる道が無くなります**
// （「語彙とプラグインは運用者が追加できることが要件」・2026-08-26）。

// withSettingsFormats は設定由来の形式を差し替え、試験の後で戻します。
func withSettingsFormats(t *testing.T, defs []VocabDef) {
	t.Helper()
	SetSettingsVocabFormats(defs)
	t.Cleanup(func() { SetSettingsVocabFormats(nil) })
}

// TestSettingsVocabFormatIndexes は、**設定で足した形式の表が索引に載る**ことを
// 固定します（これができないと、足す意味がありません）。
func TestSettingsVocabFormatIndexes(t *testing.T) {
	setupSaveTest(t)
	withSettingsFormats(t, []VocabDef{{
		Type:        "shop-log",
		DisplayName: "作業日誌",
		Element:     "table",
		Columns: []VocabColumn{
			{Label: "日", Type: ColDate},
			{Label: "作業", Type: ColText},
		},
	}})

	body := `<h1>日誌</h1><table data-type="shop-log"><tbody>` +
		`<tr><th>日</th><th>作業</th></tr>` +
		`<tr><td>2026-09-20</td><td>段取り</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000091", body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	rows := queryVocabRows(t, 91)
	if len(rows) == 0 {
		t.Fatal("設定で足した形式が索引に載っていません")
	}
	for _, r := range rows {
		if r.dataType != "shop-log" {
			t.Errorf("形式名が違います: %+v", r)
		}
	}
}

// TestSettingsVocabFormatWorksWithCaption は、設定で足した形式も**caption で名乗れる**
// ことを固定します。
//
// ⚠ **見える文字で宣言する**のがこの先の書き方なので（§2.4）、運用者が足した形式だけ
// 属性でしか名乗れない、では中途半端です。
func TestSettingsVocabFormatWorksWithCaption(t *testing.T) {
	setupSaveTest(t)
	withSettingsFormats(t, []VocabDef{{
		Type: "shop-log", DisplayName: "作業日誌", Element: "table",
		Columns: []VocabColumn{{Label: "日", Type: ColDate}, {Label: "作業", Type: ColText}},
	}})

	body := `<h1>日誌</h1><table><caption>作業日誌</caption><tbody>` +
		`<tr><th>日</th><th>作業</th></tr>` +
		`<tr><td>2026-09-20</td><td>段取り</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000092", body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
	if rows := queryVocabRows(t, 92); len(rows) == 0 {
		t.Error("caption で名乗った運用者の形式が索引に載っていません")
	}
}

// TestSettingsVocabFormatsReplaceOnReload は、**読み直しても落ちない・増えない**ことを
// 固定します。
//
// ⚠ **設定は読み直されます**（DB再構築が `LoadSettings` を通る）。毎回 `RegisterVocab`
// を呼ぶと**二重登録でその場で落ちます**——サーバーが起動しなくなる形の壊れ方です。
func TestSettingsVocabFormatsReplaceOnReload(t *testing.T) {
	before := len(VocabDefs())
	defs := []VocabDef{{
		Type: "shop-log", DisplayName: "作業日誌", Element: "table",
		Columns: []VocabColumn{{Label: "日", Type: ColDate}},
	}}
	withSettingsFormats(t, defs)
	after := len(VocabDefs())
	if after != before+1 {
		t.Fatalf("1件増えていません: %d → %d", before, after)
	}
	// **3回読み直しても増えない**（落ちないことも兼ねる）。
	for i := 0; i < 3; i++ {
		SetSettingsVocabFormats(defs)
	}
	if got := len(VocabDefs()); got != after {
		t.Errorf("読み直しで増えています: %d → %d", after, got)
	}
}

// TestSettingsVocabFormatsValidate は、**おかしな宣言で起動を止める**ことを固定します。
//
// ⚠ 黙って読み飛ばすと、**書いたつもりの形式が効かないまま**、索引だけが静かに
// 変わります（設定の既存の規律と同じ）。
func TestSettingsVocabFormatsValidate(t *testing.T) {
	base := Settings{Vocabulary: map[string]VocabWord{}}
	for _, c := range []struct {
		name string
		defs []VocabDef
		want string
	}{
		{"形式名が無い", []VocabDef{{DisplayName: "日誌", Element: "table"}}, "形式名"},
		{"表示名が無い", []VocabDef{{Type: "shop-log", Element: "table"}}, "表示名"},
		{"形式名が重複", []VocabDef{
			{Type: "shop-log", DisplayName: "A", Element: "table"},
			{Type: "shop-log", DisplayName: "B", Element: "table"},
		}, "2つあります"},
		{"コードの宣言と同じ名前", []VocabDef{
			{Type: "tags", DisplayName: "私のタグ", Element: "dl"},
		}, "コードが既に宣言"},
		{"列の型が使えない", []VocabDef{{
			Type: "shop-log", DisplayName: "日誌", Element: "table",
			Columns: []VocabColumn{{Label: "日", Type: "いつか"}},
		}}, "使えません"},
		{"列に見出しが無い", []VocabDef{{
			Type: "shop-log", DisplayName: "日誌", Element: "table",
			Columns: []VocabColumn{{Type: ColText}},
		}}, "見出しがありません"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := base
			s.VocabFormats = c.defs
			err := s.validate("test.json")
			if err == nil {
				t.Fatalf("止めていません")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("理由が違います: %v", err)
			}
		})
	}
}

// TestSettingsVocabFormatsSurviveReloadValidation は、**読み直しの検査を通る**ことを
// 固定します。
//
// ⚠ **これは実データで踏んだバグです**（2026-09-20）。設定は読み直されるので
// （DB再構築が `LoadSettings` を通る）、そのとき**前回自分が登録した形式**が
// レジストリに居ます。検査が素朴に `VocabDefByType` を見ると、**自分自身を
// 「コードの宣言と衝突している」と判定**し、**DB再構築が HTTP 500 で落ちます**。
//
// ⚠ **単体の試験だけでは出ませんでした**——まっさらな `Settings` を検査していたので、
// 「前回のぶんが居る」状態を作っていなかったためです。実際に動かして初めて出ました。
func TestSettingsVocabFormatsSurviveReloadValidation(t *testing.T) {
	defs := []VocabDef{{
		Type: "shop-log", DisplayName: "作業日誌", Element: "table",
		Columns: []VocabColumn{{Label: "日", Type: ColDate}},
	}}
	withSettingsFormats(t, defs) // 1回目の読み込みに相当

	// 2回目の読み込み——**同じ設定を検査して通ること**。
	s := Settings{Vocabulary: map[string]VocabWord{}, VocabFormats: defs}
	if err := s.validate("test.json"); err != nil {
		t.Fatalf("⚠ 読み直しの検査で落ちています（DB再構築が 500 になります）: %v", err)
	}
	// 反映も通る（増えない）。
	SetSettingsVocabFormats(defs)
	n := 0
	for _, d := range VocabDefs() {
		if d.Type == "shop-log" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("読み直しで増減しています: %d件", n)
	}
}
