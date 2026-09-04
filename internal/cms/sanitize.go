package cms

// 本文サニタイズの入口。実装は internal/cms/htmldoc（純粋なHTML部品）にあり、
// ここは薄いラッパです。
//
// かつては「各プラグインが Tags() で宣言したカスタム要素を PluginTags() で集めて
// htmldoc.New() へ注入する」語彙の合成を担っていました（`cms → htmldoc → cms` の
// 循環を避けるための依存反転。docs/変更履歴.md 2026-08-06）。語彙モデルへの移行完了
// （2026-08-20）でカスタム要素がゼロになり、注入も、プラグインの init() 登録を待つ
// 遅延初期化（sync.Once）も不要になりました——**暗黙の初期化順序への依存が消えています**。
// 依存は `cms → htmldoc` の一方向のまま。**htmldoc から cms を import しないこと。**

import (
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// BlockIDAttr はブロック単位保存で使う識別子の属性名（data-id）です。
// 定義と経緯は htmldoc.BlockIDAttr を参照。
const BlockIDAttr = htmldoc.BlockIDAttr

// sanitizer は本文サニタイザです。語彙は構造HTML（htmldoc）に固定なので、
// パッケージ変数として一度だけ組み立てて使い回します。
var sanitizer = htmldoc.New()

// Sanitize は本文HTMLを許可リストに従って安全な形へ整えて返します。
func Sanitize(s string) string {
	return sanitizer.Sanitize(s)
}

// SanitizeReport はサニタイズ結果と、内容が変化したか（＝何かを除去したか）を返します。
func SanitizeReport(s string) (out string, changed bool) {
	return sanitizer.SanitizeReport(s)
}

// AllowedVocabulary は「要素名 → 許可属性（ソート済み）」を返します。
// /api/tag-schema がエディタのシリアライザへ渡す語彙の実体です。
func AllowedVocabulary() map[string][]string {
	return sanitizer.AllowedVocabulary()
}

// ShellPrefixedIDs は本文中の `id` のうち、**殻が独占する接頭辞**
// （htmldoc.ShellIDPrefix）が付いているものを重複なくソートして返します。
//
// サニタイズは接頭辞を剥がして保存を通す（拒否しない）ので、書き手が
// 「意図した id と違う名前になった」ことに気づけるよう、**保存時の告知**に使います
// （UnknownVocabTypes・UnresolvedVocabFields と同じエコーバックの流儀）。
// 走査するのはサニタイズ**前**のHTMLです——後では接頭辞が消えていて分かりません。
func ShellPrefixedIDs(htmlStr string) []string {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			if v := Attr(n, "id"); v != "" && strings.HasPrefix(v, htmldoc.ShellIDPrefix) {
				seen[v] = true
			}
		})
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
