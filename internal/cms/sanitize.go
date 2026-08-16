package cms

// 本文サニタイズの入口。実装は internal/cms/htmldoc（純粋なHTML部品）にあり、
// ここは**語彙の合成**だけを担います——各プラグインが Tags() で宣言したカスタム要素を
// PluginTags() で集め、htmldoc.New() へ**注入**してサニタイザを組み立てる。
//
// かつて sanitize.go は PluginTags() を直接呼んでおり、そのまま別パッケージへ出すと
// `cms → htmldoc → cms` の循環参照になるため切り出せなかった（docs/変更履歴.md
// 2026-08-06「やらなかったこと」）。語彙を注入する形へ依存を反転させたことで、
// 依存は `cms → htmldoc` の一方向になっている。**htmldoc から cms を import しないこと。**

import (
	"sync"

	"w-cms/internal/cms/htmldoc"
)

// BlockIDAttr はブロック単位保存で使う識別子の属性名（data-id）です。
// 定義と経緯は htmldoc.BlockIDAttr を参照。
const BlockIDAttr = htmldoc.BlockIDAttr

// sanitizer は合成済みのサニタイザです。プラグインは init() で登録され実行中に
// 増減しないため、初回利用時に一度だけ組み立てて使い回します
// （「全プラグインの init() 登録 → 最初のサニタイズ」という順序への依存は従来どおり）。
var (
	sanitizerOnce sync.Once
	sanitizer     *htmldoc.Sanitizer
)

// activeSanitizer は「構造HTML ∪ 全プラグインが宣言したカスタム要素」の
// サニタイザを返します。
func activeSanitizer() *htmldoc.Sanitizer {
	sanitizerOnce.Do(func() {
		sanitizer = htmldoc.New(PluginTags())
	})
	return sanitizer
}

// Sanitize は本文HTMLを許可リストに従って安全な形へ整えて返します
// （htmldoc.Sanitizer.Sanitize の語彙合成済みラッパ）。
func Sanitize(s string) string {
	return activeSanitizer().Sanitize(s)
}

// SanitizeReport はサニタイズ結果と、内容が変化したか（＝何かを除去したか）を返します
// （htmldoc.Sanitizer.SanitizeReport の語彙合成済みラッパ）。
func SanitizeReport(s string) (out string, changed bool) {
	return activeSanitizer().SanitizeReport(s)
}

// AllowedVocabulary は「要素名 → 許可属性（ソート済み）」を返します。
// /api/tag-schema がエディタのシリアライザへ渡す語彙の実体です。
func AllowedVocabulary() map[string][]string {
	return activeSanitizer().AllowedVocabulary()
}

// VoidElementNames は子を持てない要素名（ソート済み）を返します。
func VoidElementNames() []string {
	return htmldoc.VoidElementNames()
}
