package cms

import (
	"encoding/json"
	"errors"
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
		{"JSONとして壊れている", `{"vocabulary": {`},
		// `datetime` は 2026-09-06 に**実在の型になった**ので、例を差し替えた
		// （SQL にある名前を借りると、いつか実装されて例でなくなる）。
		{"未知の列型", `{"vocabulary": {"加工日": {"type": "timestamptz"}}}`},
		{"空の見出し語", `{"vocabulary": {"  ": {"type": "date"}}}`},
		{"打ち間違えたキー", `{"vocabularly": {"加工日": {"type": "date"}}}`},
		{"置き換えの連鎖", `{"char_folding": {"Φ": "φ", "φ": "f"}}`},
		// 段（machine_stages）の検査は 2026-09-15 に下請け（ext/subcon）の節へ移った。
		// **コアの型から外したので、トップに書くと「知らないキー」で止まる**のが正しい。
		{"段をトップに書いた（09-15 に extensions.subcon へ移した）", `{"machine_stages": ["現行"]}`},
		{"選択肢が空", `{"vocabulary": {"在籍": {"type": "enum"}}}`},
		{"選択肢に空の値", `{"vocabulary": {"在籍": {"type": "enum", "values": ["在籍", "  "]}}}`},
		{"選択肢の重複", `{"vocabulary": {"在籍": {"type": "enum", "values": ["在籍", "在籍"]}}}`},
		// **型と選択肢が1件になったので、新しく起こりうる食い違い**——選択肢を
		// 書いたのに型が enum でないとき。黙って無視すると、書いた人は色分けが
		// 出ない理由に気づけません。
		{"enum でないのに選択肢がある", `{"vocabulary": {"在籍": {"type": "text", "values": ["在籍"]}}}`},
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
	if _, ok := settingsSnapshot().Extensions["subcon"]; !ok {
		t.Error("extensions.subcon の節がありません（段はそこへ移した）")
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
	writeTestSettings(t, `{"vocabulary": {"加工日": {"type": "date"}}}`)
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

// TestTagEnumsAreSuggestionsNotRules は、選択肢の表が**縛りではない**ことを固定します。
//
// 2026-09-13 ユーザー:「語彙に無いものは背景色で区別すればよいのでは？」。
// 表に無い値も書けます——画面が色で知らせるだけで、**読み込みも保存も拒否しません**。
// 既にある規律と同じです（語彙モデル §5.1: 検証して通知する。拒否はしない）。
//
// **拒否が見せかけだから**でもあります。本文は人が書くもので、編集モードで何でも
// 打てる以上、入口で弾いても本文には入ります。見えるほうが直せます。
func TestTagEnumsAreSuggestionsNotRules(t *testing.T) {
	useTempSettings(t)
	writeTestSettings(t, `{"vocabulary": {`+
		`"在籍": {"type": "enum", "values": ["在籍", "休職", "出向", "退社"]}}}`)
	if err := LoadSettings(); err != nil {
		t.Fatalf("正しい設定が読めません: %v", err)
	}
	dict := VocabularyDict()
	if len(dict["在籍"].Values) != 4 {
		t.Fatalf("選択肢が読めていません: %v", dict)
	}

	// **写しであること**——呼び出し側が書き換えても設定は壊れない。
	dict["在籍"].Values[0] = "書き換え"
	if again := VocabularyDict(); again["在籍"].Values[0] != "在籍" {
		t.Errorf("返した配列が設定の実体でした（写しを返すべき）: %v", again["在籍"].Values)
	}

	// **型と選択肢は1件で登録します**（2026-09-14 に統合）。前は選択肢だけが
	// 書けたので `在籍` は既定の text 扱いでした——1語の情報が2つの表に割れていた
	// ためで、いまは型を書かないと読み込みが止まります。
	if got := InferColumnType("在籍"); got != ColEnum {
		t.Errorf("選択肢を持つ語の型が enum になっていません: %v", got)
	}

	// それでも**表に無い値は拒否しません**——索引には生の値がそのまま入り、
	// 画面が薄黄で知らせるだけです（語彙モデル §5.1）。
	if norm, ok := NormalizeValue(ColEnum, "育休"); !ok || norm != "育休" {
		t.Errorf("選択肢の外の値を弾いています: norm=%q ok=%v", norm, ok)
	}
}

// TestValidColumnTypeNamesCoversAll は、**知らせが型の一覧を取りこぼさない**ことを
// 固定します。
//
// 2026-09-13 に `datetime`・`ref`・`email` が抜けたまま残っていました——エラー文へ
// 手で書き写していたためです。型を足した日に片方だけ古くなる形は、このプロジェクトが
// 何度も踏んでいます（設定とコードの二重管理）。
func TestValidColumnTypeNamesCoversAll(t *testing.T) {
	got := validColumnTypeNames()
	if len(got) != len(validColumnTypes) {
		t.Errorf("知らせに出る型が %d 個、使える型は %d 個——並び順の表から漏れています: %v",
			len(got), len(validColumnTypes), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for typ := range validColumnTypes {
		if !seen[string(typ)] {
			t.Errorf("型 %q が知らせに出ません", typ)
		}
	}
}

// settingsSnapshot はいま効いている設定を返します（試験用）。
func settingsSnapshot() *Settings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return &Settings{}
	}
	return settings
}

// withSection は試験のあいだだけ設定の節を登録します。
func withSection(t *testing.T, name string, parse SettingsSectionParser) {
	t.Helper()
	if _, dup := settingsSections[name]; dup {
		t.Fatalf("節 %s は既に登録されています", name)
	}
	settingsSections[name] = parse
	t.Cleanup(func() { delete(settingsSections, name) })
}

// TestSettingsSectionsApplyOnlyWhenAllValid は、**拡張の節は全部の検査が通ってから効く**
// ことを固定します（2026-09-15・拡張の組み替え §4.2）。
//
// 節の1つが壊れているのに、先に読んだ節だけ効いてしまうと、設定が半端な状態で動きます。
// 起動なら止まるので害は無いが、**DB再構築の読み直しでは、半端なまま走り続けます**。
func TestSettingsSectionsApplyOnlyWhenAllValid(t *testing.T) {
	useTempSettings(t)
	var got string
	withSection(t, "aaa", func(raw json.RawMessage) (func(), error) {
		v := string(raw)
		return func() { got = v }, nil
	})
	withSection(t, "zzz", func(raw json.RawMessage) (func(), error) {
		if string(raw) == `"壊れ"` {
			return nil, errors.New("壊れています")
		}
		return nil, nil
	})

	writeTestSettings(t, `{"extensions": {"aaa": "一回目", "zzz": "良い"}}`)
	if err := LoadSettings(); err != nil {
		t.Fatalf("正しい設定が読めません: %v", err)
	}
	if got != `"一回目"` {
		t.Fatalf("節が効いていません: %q", got)
	}

	// **aaa は正しく、zzz が壊れている**——aaa を先に読んでも、効かせてはいけない。
	writeTestSettings(t, `{"extensions": {"aaa": "二回目", "zzz": "壊れ"}}`)
	err := LoadSettings()
	if err == nil {
		t.Fatal("壊れた節が受け入れられました")
	}
	if !strings.Contains(err.Error(), "extensions.zzz") {
		t.Errorf("どの節が壊れているのか分からない知らせです: %v", err)
	}
	if got != `"一回目"` {
		t.Errorf("壊れた設定の一部だけが効いています: %q（一回目のままのはず）", got)
	}
}

// TestSettingsUnknownSectionIsSkipped は、**載っていない拡張の節では止めない**ことを
// 固定します。同じ `config/settings.json` を `-tags minimal` でも使うためです。
func TestSettingsUnknownSectionIsSkipped(t *testing.T) {
	useTempSettings(t)
	writeTestSettings(t, `{"extensions": {"このビルドに無い拡張": {"何でも": 1}}}`)
	if err := LoadSettings(); err != nil {
		t.Fatalf("載っていない拡張の節で止まりました: %v", err)
	}
}

// TestSettingsAbsentSectionGetsNil は、**節が無ければ nil を渡す**ことを固定します。
// 拡張は nil を「空の設定」として効かせます——読み直しで節を消したとき、古い値が残らないように。
func TestSettingsAbsentSectionGetsNil(t *testing.T) {
	useTempSettings(t)
	called, gotNil := false, false
	withSection(t, "absent", func(raw json.RawMessage) (func(), error) {
		called, gotNil = true, raw == nil
		return nil, nil
	})
	writeTestSettings(t, `{}`)
	if err := LoadSettings(); err != nil {
		t.Fatalf("設定が読めません: %v", err)
	}
	if !called || !gotNil {
		t.Errorf("節が無いときに nil で呼ばれていません: called=%v nil=%v", called, gotNil)
	}
}
