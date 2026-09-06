package cms

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ── テストの設定読み込み（2026-09-07）───────────────────────────────────
//
// 既定値をコードから無くしたので、**設定を読まないテストは何も推論できません**。
// かといって、テストごとに設定ファイルを書き写すのは本末転倒です——リポジトリの
// `config/settings.json` が唯一の正本なのだから、テストもそれを読むべきです。
//
// 場所は `runtime.Caller` で解決します。多くのテストが `os.Chdir` で一時ディレクトリへ
// 移るため、**相対パスでは見つかりません**。ソースの位置から遡るのが確実です。
//
// TestMain で1度だけ読みます。個々のテストが `useTempSettings` で差し替えたときは、
// そのテストの後始末（restoreSettings）がここで読んだものへ戻します。

// repoSettingsPath はリポジトリの設定ファイルの絶対パスを返します。
func repoSettingsPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "settings.json")
}

// loadRepoSettings はリポジトリの設定を読み込みます（作業ディレクトリに依りません）。
func loadRepoSettings() error { return LoadSettingsFrom(repoSettingsPath()) }

func TestMain(m *testing.M) {
	if err := loadRepoSettings(); err != nil {
		// 設定が読めないなら、ほとんどのテストは意味を成しません。
		panic("テストの設定を読み込めません: " + err.Error())
	}
	os.Exit(m.Run())
}
