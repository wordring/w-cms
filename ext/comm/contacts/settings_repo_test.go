package contacts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"w-cms/internal/cms"
)

// ── テストの設定読み込み ─────────────────────────────────────────────
//
// 既定値はコードにありません（`config/settings.json` が唯一の正本）。読まないと
// **型推論が空**になり、`メールアドレス` タグの畳んだ値（`email` 型＝アドレスだけ）が
// 作られません——アドレスでの逆引きが全部空振りします。
//
// 場所は `runtime.Caller` で解決します——テストは `os.Chdir` で一時ディレクトリへ
// 移るため、相対パスでは見つかりません（`ext/subcon` と同じ流儀）。
func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "settings.json")
	if err := cms.LoadSettingsFrom(path); err != nil {
		panic("テストの設定を読み込めません: " + err.Error())
	}
	os.Exit(m.Run())
}
