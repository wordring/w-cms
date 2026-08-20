package cms

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン機構（レジストリ＋インターフェース方式）
//
// 1つの計算ユースケース（例:「顧客の発注書」）を、1つの自己完結したプラグインとして
// 表現するための仕組みです。プラグインは init() で自身を Register() に登録し、
// コア（parser/sync/schema/routing）は登録済みプラグインを走査するだけで動作します。
//
// プラグインは**本文の語彙を持ちません**（語彙モデル §8.4 の移行完了・2026-08-20）。
// 本文で扱えるHTMLは htmldoc の構造HTML＋`data-*` マーカーだけで、形式の宣言は
// ①語彙レジストリ（vocab.go）が持ちます。プラグインの仕事は「マーカー付き標準HTMLを
// 読んで自分のテーブルへ同期する」ことに絞られました（かつては必須メソッド Tags() で
// カスタム要素 <m-*> の語彙も所有していた）。
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

	// Sync は1ページ分のHTMLノード木を走査し、自分のテーブルを当該ページ分だけ
	// 洗い替え（DELETE → INSERT）します。トランザクション tx の中で呼ばれます。
	Sync(tx *sql.Tx, pageID int, root *html.Node) error
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

// TagValue はページ横断メタ（可変タグ）から名前 tagName の値を返します。
// 同名タグが複数ある場合は最初の1つ。見つからなければ空文字列。
// 読む形式は <dl data-type="tags"><dt>{tagName}</dt><dd>値</dd></dl>
// （鍵は dt の表示文字。dd の data-field があればそちらが優先）。
func TagValue(root *html.Node, tagName string) string {
	var found string
	WalkElements(root, func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Data == "dl" && Attr(n, "data-type") == "tags" {
			found = dlTagValue(n, tagName)
		}
	})
	return found
}

// dlTagValue は <dl data-type="tags"> の中から名前 tagName の最初の値を返します。
// 鍵は dt の表示文字（自由語）で、dd の data-field があればそちらが優先します
// （②汎用索引と同じ規則）。
func dlTagValue(dl *html.Node, tagName string) string {
	currentKey := ""
	found := ""
	walkSkippingNested(dl, map[string]bool{"dl": true, "table": true}, func(n *html.Node) {
		if found != "" {
			return
		}
		switch n.Data {
		case "dt":
			currentKey = strings.TrimSpace(nodeText(n))
		case "dd":
			key := Attr(n, "data-field")
			if key == "" {
				key = currentKey
			}
			if key == tagName {
				found = strings.TrimSpace(nodeText(n))
			}
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
