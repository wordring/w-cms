package cms

import (
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
	savedFolder := charFolder
	settingsMu.RUnlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settings = saved
		charFolder = savedFolder
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

// TestSettingsMissingFileStops は、**設定ファイルが無ければ起動を止める**ことを
// 固定します（2026-09-07 ユーザー:「コードに埋め込まれる規定を無くし、Settings.jsonを
// 必須にし、Githubに入れてはどうでしょう？」）。
//
// もとは既定値で作り直していました。やめたのは、コードとファイルが同じことを
// 2か所で言うのを断つため——コードに語を足しても、**既にファイルのある環境には
// 届きません**でした（2026-09-06 に ref 型でこれを踏みかけた）。
func TestSettingsMissingFileStops(t *testing.T) {
	useTempSettings(t)
	// **起動直後の姿を作ります**——まだ何も読んでいない状態。読み込み済みのときは
	// 「いま持っているものを守る」ほうへ分岐するので、そこと区別して確かめます。
	settingsMu.Lock()
	settings = nil
	settingsMu.Unlock()

	err := LoadSettings()
	if err == nil {
		t.Fatal("設定ファイルが無いのに起動できました（止めるべき）")
	}
	if _, statErr := os.Stat(SettingsPath); statErr == nil {
		t.Error("設定ファイルを作り直しています（既定値をコードに持たないのが決定）")
	}
	if !strings.Contains(err.Error(), SettingsPath) {
		t.Errorf("どのファイルが無いのか分からない知らせです: %v", err)
	}
}

// TestSettingsRejectsBrokenFile は、壊れた設定で**起動を止める**ことを固定します。
// 既定値で黙って上書きすると、運用者が足した語が消えて集計の内容が黙って変わります。
func TestSettingsRejectsBrokenFile(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"JSONとして壊れている", `{"type_inference": {`},
		// `datetime` は 2026-09-06 に**実在の型になった**ので、例を差し替えた
		// （SQL にある名前を借りると、いつか実装されて例でなくなる）。
		{"未知の列型", `{"type_inference": {"加工日": "timestamptz"}}`},
		{"空の見出し語", `{"type_inference": {"  ": "date"}}`},
		{"打ち間違えたキー", `{"type_inferrence": {"加工日": "date"}}`},
		{"置き換えの連鎖", `{"char_folding": {"Φ": "φ", "φ": "f"}}`},
		{"段の重複", `{"machine_stages": ["現行", "現行"]}`},
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

// TestSettingsUnloadedInfersNothing は、**設定を読む前は推論しない**ことを固定します。
//
// もとは「未読込なら既定値」でしたが、既定値をコードから無くしたので、読む前は
// 何も知らない状態が正しい姿です（本番は起動時に必ず読み、無ければ止まります）。
func TestSettingsUnloadedInfersNothing(t *testing.T) {
	useTempSettings(t)
	settingsMu.Lock()
	settings = nil
	charFolder = nil
	settingsMu.Unlock()

	if got := InferColumnType("発注日"); got != ColText {
		t.Errorf("設定を読む前に推論が効いています: 発注日 → %q", got)
	}
	if got := NormalizeText("Φ320"); got != "Φ320" {
		t.Errorf("設定を読む前に文字を畳んでいます: %q", got)
	}
}

// TestSettingsLoadsRepoFile は、**リポジトリの config/settings.json が読める**ことを
// 固定します。Git管理の必須ファイルなので、壊すとどの環境も起動しなくなります。
func TestSettingsLoadsRepoFile(t *testing.T) {
	restoreSettings(t)
	if err := loadRepoSettings(); err != nil {
		t.Fatalf("リポジトリの設定を読めません: %v", err)
	}
	// コアが書く語が宣言されていること（ここが欠けると参照リンクが消えます）。
	for word, want := range map[string]ColumnType{
		"受信元": ColRef, "受信日時": ColDateTime, "図面番号": ColCode, "発注日": ColDate,
	} {
		if got := InferColumnType(word); got != want {
			t.Errorf("config/settings.json に %q の宣言がありません: %q（期待 %q）", word, got, want)
		}
	}
	if len(MachineStages()) == 0 {
		t.Error("machine_stages が空です")
	}
	if NormalizeText("Φ320") != "φ320" {
		t.Error("char_folding が効いていません")
	}
}

func writeTestSettings(t *testing.T, body string) {
	t.Helper()
	if dir := filepath.Dir(SettingsPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAllエラー: %v", err)
		}
	}
	if err := os.WriteFile(SettingsPath, []byte(body), 0o644); err != nil {
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

	// 「加工日」はリポジトリの辞書に無い語（＝いまは text 扱いで正規化されない）。
	if got := InferColumnType("加工日"); got != ColText {
		t.Fatalf("前提が崩れています: 加工日 は text のはず（got %q）", got)
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
