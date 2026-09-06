package sheetmetal

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"w-cms/internal/cms"
)

// ── テストの設定読み込み（2026-09-07）───────────────────────────────────
//
// 既定値をコードから無くしたので（`config/settings.json` が唯一の正本）、
// 設定を読まないと**段も型推論も空**になります。整理のテストは段を選ぶので、
// 読まないと全件「段（）を選んでください」で落ちます。
//
// 場所は `runtime.Caller` で解決します——テストは `os.Chdir` で一時ディレクトリへ
// 移るため、相対パスでは見つかりません。
func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "settings.json")
	if err := cms.LoadSettingsFrom(path); err != nil {
		panic("テストの設定を読み込めません: " + err.Error())
	}
	os.Exit(m.Run())
}
