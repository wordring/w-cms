package cms

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン機構（レジストリ＋インターフェース方式）
//
// 1つのユースケース（例:「顧客の発注書」）を、1つの自己完結したプラグインとして
// 表現するための仕組みです。プラグインは init() で自身を Register() に登録し、
// コア（parser/sync/schema/routing）は登録済みプラグインを走査するだけで動作します。
//
// 新しいユースケースの追加手順は docs/【ガイド】プラグイン開発.md を参照してください。
// ─────────────────────────────────────────────────────────────────────────

// Plugin は1つのユースケース（タグ→テーブルの抽出・同期）を自己完結で表す拡張単位です。
type Plugin interface {
	// Name は識別子（ログ・デバッグ用）。テーブル名やタグ名と重複しても構いません。
	Name() string

	// Schema はこのプラグインが必要とする CREATE TABLE 文を返します。
	// 必ず "CREATE TABLE IF NOT EXISTS ..." の形にしてください（多重作成に耐えるため）。
	Schema() []string

	// Tables はこのプラグインが所有するテーブル名を返します（自己文書化・整合性検証用）。
	// 全再構築（RebuildDatabase）は sqlite_master から全テーブルをDROPするため、
	// このリストの記載漏れが再構築バグを引き起こすことはありません。ただし Schema() で
	// 定義したテーブルはここにも列挙してください（テストで両者の整合性を検証します）。
	Tables() []string

	// Tags はこのプラグインが所有するカスタム要素と、その属性契約を返します。
	//
	// **カスタムタグはすべてプラグインが所有します**（コアは構造HTMLの安全性しか知りません）。
	// そのため、このメソッドは任意インターフェースではなく Plugin の必須メソッドです。
	// 宣言せずに属性を読むプラグインを書けないようにするための、コンパイラによる強制です。
	// 扱うカスタム要素が無いプラグインは nil を返してください。
	Tags() []TagSpec

	// Sync は1ページ分のHTMLノード木を走査し、自分のテーブルを当該ページ分だけ
	// 洗い替え（DELETE → INSERT）します。トランザクション tx の中で呼ばれます。
	Sync(tx *sql.Tx, pageID int, root *html.Node) error
}

// TagSpec は1つのカスタム要素の「属性契約」です。
//
// ここに挙げるのは「プラグインが読む属性」ではなく **その要素に許される属性の全体** です。
// 誰も同期しないが保存・表示には要る属性（<m-file> の src/name、
// <m-required-materials> の page-id など）も1つの宣言で扱えるようにするためです。
//
// この単一の宣言が3つの用途を賄います:
//   - サニタイズの許可リスト（internal/cms/sanitize.go）
//   - /api/tag-schema（エディタのシリアライザが参照する）
//   - ドキュメント（docs/【一覧】カスタムタグ.md）の材料
//
// ここに載せ忘れた属性は保存のたびに黙って除去されるため、
// プラグインが読む属性は必ず含めてください。
type TagSpec struct {
	Element    string   // 要素名（例: "m-client-order"）
	Attributes []string // この要素に許される属性
}

// RouteProvider は集計APIなどのHTTPエンドポイントを提供したいプラグインが
// 追加で実装する任意インターフェースです（Tier 2: コードプラグイン）。
// Tags と違い、実装しなくてもプラグインは成立するため任意のままにしています。
type RouteProvider interface {
	Routes() []Route
}

// Route は1つのHTTPエンドポイント登録を表します。
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// registry は登録済みの全プラグイン。各プラグインファイルの init() から Register される。
var registry []Plugin

// Register はプラグインを登録します。各プラグインの init() から呼び出してください。
func Register(p Plugin) {
	registry = append(registry, p)
}

// Plugins は登録済みの全プラグインを返します。
func Plugins() []Plugin {
	return registry
}

// ApplySchema は登録済みの全プラグインのテーブルを作成します。
// 本番では database.InitDB() でコアテーブルを作成した後、main から呼び出します。
func ApplySchema(db *sql.DB) error {
	for _, p := range registry {
		for _, q := range p.Schema() {
			if _, err := db.Exec(q); err != nil {
				return fmt.Errorf("プラグイン %q のスキーマ作成に失敗: %w", p.Name(), err)
			}
		}
	}
	return nil
}

// PluginTags は登録済み全プラグインのカスタム要素宣言を集約して返します。
// 同じ要素を複数のプラグインが扱う場合（例: <m-item> は顧客の発注書と弊社の発注書の
// 双方で使う）は**属性の和集合**になります。どのプラグインが読む属性も落ちません。
// 戻り値は要素名でソートしてあり、呼び出しごとに順序が変わりません。
func PluginTags() []TagSpec {
	merged := map[string]map[string]bool{}
	for _, p := range registry {
		for _, spec := range p.Tags() {
			if spec.Element == "" {
				continue
			}
			if merged[spec.Element] == nil {
				merged[spec.Element] = map[string]bool{}
			}
			for _, a := range spec.Attributes {
				merged[spec.Element][a] = true
			}
		}
	}

	elements := make([]string, 0, len(merged))
	for el := range merged {
		elements = append(elements, el)
	}
	sort.Strings(elements)

	out := make([]TagSpec, 0, len(elements))
	for _, el := range elements {
		attrs := make([]string, 0, len(merged[el]))
		for a := range merged[el] {
			attrs = append(attrs, a)
		}
		sort.Strings(attrs)
		out = append(out, TagSpec{Element: el, Attributes: attrs})
	}
	return out
}

// PluginRoutes は RouteProvider を実装する全プラグインのルートを集約して返します。
// main がこれを走査して mux に登録します。
func PluginRoutes() []Route {
	var routes []Route
	for _, p := range registry {
		if rp, ok := p.(RouteProvider); ok {
			routes = append(routes, rp.Routes()...)
		}
	}
	return routes
}

// ─────────────────────────────────────────────────────────────────────────
// プラグインの実装を簡潔にするための DOM / 値変換ヘルパー
// ─────────────────────────────────────────────────────────────────────────

// Attr は要素ノード n の属性 key の値を返します。存在しなければ空文字列。
func Attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// AtoiSafe は文字列を整数に変換します。変換できない場合は 0 を返します。
func AtoiSafe(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// Quantity は数量属性を返します。未指定・変換不能の場合はデフォルトの 1 を返します。
func Quantity(n *html.Node) int {
	if v := Attr(n, "quantity"); v != "" {
		return AtoiSafe(v)
	}
	return 1
}

// WalkElements はノード木 root を先行順（document order）で走査し、
// すべての要素ノードに対して fn を呼び出します。
func WalkElements(root *html.Node, fn func(*html.Node)) {
	if root.Type == html.ElementNode {
		fn(root)
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		WalkElements(c, fn)
	}
}

// TagValue は <m-tag name="{tagName}" value="..."> の value を返します。
// 同名タグが複数ある場合は最初の1つ。見つからなければ空文字列。
func TagValue(root *html.Node, tagName string) string {
	var found string
	WalkElements(root, func(n *html.Node) {
		if found == "" && n.Data == "m-tag" && Attr(n, "name") == tagName {
			found = Attr(n, "value")
		}
	})
	return found
}

// NullableString は空文字列を SQL の NULL に変換します（日付列などで使用）。
func NullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
