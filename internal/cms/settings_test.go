package cms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// restoreSettings は読み込み済み設定を元へ戻す後始末を仕込みます。
// 設定はパッケージ変数なので、戻さないと後続のテストへ漏れます。
func restoreSettings(t *testing.T) {
	t.Helper()
	settingsMu.RLock()
	saved := settings
	settingsMu.RUnlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settings = saved
		settingsMu.Unlock()
	})
}

// useTempSettings は一時ディレクトリへ移り、設定を元へ戻す後始末を仕込みます。
func useTempSettings(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwdエラー: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	restoreSettings(t)
}

// TestSettingsCreatedWithDefaults は、設定ファイルが無いときに既定値で作られることを検証します。
// 運用者が「中身を見て編集する」ための入口なので、黙って既定値で動くのでは足りません。
func TestSettingsCreatedWithDefaults(t *testing.T) {
	useTempSettings(t)

	if err := LoadSettings(); err != nil {
		t.Fatalf("LoadSettingsエラー: %v", err)
	}
	raw, err := os.ReadFile(SettingsPath)
	if err != nil {
		t.Fatalf("設定ファイルが作られていません: %v", err)
	}
	var got Settings
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("書き出した設定を読み戻せません: %v", err)
	}
	for word, want := range defaultTypeInference {
		if got.TypeInference[word] != want {
			t.Errorf("既定の語 %q が書き出されていません（got %q want %q）", word, got.TypeInference[word], want)
		}
	}
	if !strings.Contains(string(raw), "\n") {
		t.Error("設定ファイルが1行で書かれています（手で編集する前提なので整形して書くこと）")
	}
}

// TestSettingsDictionaryTakesEffect は、ファイルへ足した語が推論に効くことを検証します。
// これが「運用中に増やしてDB再構築します」（2026-08-30 決定）の実体です。
func TestSettingsDictionaryTakesEffect(t *testing.T) {
	useTempSettings(t)
	writeTestSettings(t, `{"type_inference": {"加工日": "date", "員数": "number"}}`)

	if err := LoadSettings(); err != nil {
		t.Fatalf("LoadSettingsエラー: %v", err)
	}
	if got := InferColumnType("加工日"); got != ColDate {
		t.Errorf("ファイルへ足した語が効いていません: 加工日 → %q", got)
	}
	if got := InferColumnType("員数"); got != ColNumber {
		t.Errorf("ファイルへ足した語が効いていません: 員数 → %q", got)
	}
	// ファイルは既定値を**置き換える**（足すのではない）。要らない語を消せることが要件。
	if got := InferColumnType("検査日"); got != ColText {
		t.Errorf("ファイルに無い既定の語がまだ効いています: 検査日 → %q（置き換え意味論のはず）", got)
	}
	// エディタへ配る辞書も同じものを見ること（形式知識の3原則の1）。
	if got := TypeInferenceDict()["加工日"]; got != ColDate {
		t.Errorf("TypeInferenceDict が別の辞書を見ています: 加工日 → %q", got)
	}
}

// TestSettingsRejectsBrokenFile は、壊れた設定で**黙って既定値へ落ちない**ことを検証します。
// 落ちると運用者が足した語が消え、集計の内容が黙って変わります（§8.4 と同じ流儀）。
func TestSettingsRejectsBrokenFile(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"JSONとして壊れている", `{"type_inference": {`},
		{"未知の列型", `{"type_inference": {"加工日": "datetime"}}`},
		{"空の見出し語", `{"type_inference": {"  ": "date"}}`},
		{"打ち間違えたキー", `{"type_inferrence": {"加工日": "date"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempSettings(t)
			writeTestSettings(t, tc.body)

			if err := LoadSettings(); err == nil {
				t.Fatal("壊れた設定が受け入れられました（起動を止めるべき）")
			}
			// 正本を既定値で上書きしていないこと。
			raw, err := os.ReadFile(SettingsPath)
			if err != nil {
				t.Fatalf("設定ファイルが消えています: %v", err)
			}
			if string(raw) != tc.body {
				t.Errorf("壊れた設定を書き換えています（自動で治さないのが決定）:\n%s", raw)
			}
		})
	}
}

// TestSettingsWriteLeavesNoTempFile は、書き込みが一時ファイルを残さないことを検証します。
// 一時ファイル＋rename にしているのは、書きかけの切り詰めが正本に残らないようにするため。
func TestSettingsWriteLeavesNoTempFile(t *testing.T) {
	useTempSettings(t)

	if err := LoadSettings(); err != nil {
		t.Fatalf("LoadSettingsエラー: %v", err)
	}
	entries, err := os.ReadDir("data")
	if err != nil {
		t.Fatalf("ReadDirエラー: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("一時ファイルが残っています: %s", e.Name())
		}
	}
}

// TestSettingsFallsBackToDefaults は、未読込のとき既定値が効くことを固定します。
// LoadSettings を呼ばない経路（テスト・ライブラリ利用）で推論が死なないための保険。
func TestSettingsFallsBackToDefaults(t *testing.T) {
	useTempSettings(t)
	settingsMu.Lock()
	settings = nil
	settingsMu.Unlock()

	if got := InferColumnType("発注日"); got != ColDate {
		t.Errorf("未読込のとき既定値が効いていません: 発注日 → %q", got)
	}
}

func writeTestSettings(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatalf("MkdirAllエラー: %v", err)
	}
	if err := os.WriteFile(filepath.Join("data", "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("設定ファイルを書けません: %v", err)
	}
}

// TestRebuildReloadsSettings は、**DB再構築が設定を読み直す**ことを固定します。
// ユーザーの決定は「運用中に増やしてDB再構築します」（2026-08-30）なので、
// 読み直しが無いと**再起動するまで足した語が効きません**——しかも画面は何も変わらず、
// 索引の中身だけが黙って古いままになります。
func TestRebuildReloadsSettings(t *testing.T) {
	setupUploadTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})
	restoreSettings(t)

	// 「加工日」は既定の辞書に無い語（＝いまは text 扱いで正規化されない）。
	if got := InferColumnType("加工日"); got != ColText {
		t.Fatalf("前提が崩れています: 加工日 は既定では text のはず（got %q）", got)
	}

	dir := page.GetPageDir("000001")
	body := `<h1>部品</h1><table data-type="inspection-record">` +
		`<tr><th>加工日</th></tr><tr><td>2026/6/15</td></tr></table>`
	if err := os.WriteFile(filepath.Join(dir, "000001.html"), []byte(body), 0644); err != nil {
		t.Fatalf("本文の作成エラー: %v", err)
	}

	// 運用者が辞書へ語を足して、DB再構築を回す。
	writeTestSettings(t, `{"type_inference": {"加工日": "date"}}`)
	if err := RebuildDatabase(); err != nil {
		t.Fatalf("RebuildDatabaseエラー: %v", err)
	}

	var norm string
	err := database.DB.QueryRow(
		`SELECT COALESCE(norm_value, '') FROM vocab_index WHERE field = ?`, "加工日").Scan(&norm)
	if err != nil {
		t.Fatalf("索引を読めません: %v", err)
	}
	if norm != "2026-06-15" {
		t.Errorf("再構築が設定を読み直していません: norm_value=%q (期待 \"2026-06-15\")", norm)
	}
}
