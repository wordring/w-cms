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

// Plugin は1つのユースケース（マーカー付き標準HTML→テーブルの抽出・同期）を
// 自己完結で表す拡張単位です。**テーブルの所有だけ**を宣言します。
//
// 本文の読み取りは Observer（下）が担います。かつては `Sync(tx, pageID, root)` で
// **木を丸ごと受け取って各自が走査**していましたが、同じ木をプラグインの数だけ歩くうえ、
// 除外規則（`.vocab-chrome` の中を読まない等）を各自が覚えている必要がありました。
// 2026-08-26 に回覧機構（walk.go）へ移し、走査はコアの配送係1つになりました。
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
}

// Observer は保存時に本文を読んで自分のテーブルを洗い替えるプラグインです（観察係）。
//
// 走査はしません——コアの配送係が、宣言した引き金に当たった要素だけを OnElement へ
// 届けます。洗い替えの DELETE は OnPageStart で行います。**この分担のおかげで、
// テンプレート領域の除外が「要素イベントを1つも発火しない」だけで成立します**
// （OnPageStart は呼ばれるので古い行は消え、新しい行は入らない）。
type Observer interface {
	Plugin

	// Triggers は担当する引き金（`data-type` の値）を返します。
	// TriggerAll（"*"）を返すと、マーカーのある要素すべてを受け取ります。
	Triggers() []string

	// OnPageStart は要素を配る前に呼ばれます。ここで自テーブルの当該ページ分を
	// DELETE してください（洗い替えの前半）。
	OnPageStart(ctx *ObserveContext) error

	// OnElement は担当の要素ごとに呼ばれます。el の部分木は自由に読んで構いません。
	// descend=false を返すと、配送係は el の子孫へ降りません（担当済みの意思表示）。
	OnElement(ctx *ObserveContext, el *html.Node) (descend bool, err error)
}

// PageFinisher は「要素を配り終えたあと」の処理が要る観察係が追加で実装します
// （集計の確定など）。RouteProvider と同じく任意インターフェースです。
type PageFinisher interface {
	OnPageEnd(ctx *ObserveContext) error
}

// RouteProvider は集計APIなどのHTTPエンドポイントを提供したいプラグインが
// 追加で実装する任意インターフェースです（Tier 2: コードプラグイン）。
// 実装しなくてもプラグインは成立するため、Plugin の必須メソッドにはしていません。
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
// Observer を実装していれば、宣言した引き金で回覧機構へも自動的に登録します
// （登録の口を2つ覚える必要がないように）。
func Register(p Plugin) {
	registry = append(registry, p)
	if o, ok := p.(Observer); ok {
		observers = append(observers, o)
		for _, t := range o.Triggers() {
			walkers.observe(t, observerAdapter{o})
		}
	}
}

// observers は Observer を実装したプラグイン（ページ単位のフックを呼ぶ相手）。
var observers []Observer

// Observers は登録済みの観察係を返します（SyncIndex がページ単位のフックを回す）。
func Observers() []Observer { return observers }

// observerAdapter は Observer を配送係の受け口（ObserveHandler）へ橋渡しします。
type observerAdapter struct{ o Observer }

func (a observerAdapter) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	descend, err := a.o.OnElement(ctx, el)
	if err != nil {
		// どのプラグインで失敗したかが分からないと原因に辿り着けない
		// （旧 SyncIndex の "プラグイン %q の同期に失敗" と同じ情報量を保つ）。
		return false, fmt.Errorf("プラグイン %q の同期に失敗: %w", a.o.Name(), err)
	}
	return descend, nil
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

// DriftedSchemaTables は、既存DBの定義が現在の宣言とずれているプラグインテーブルを返します。
//
// ApplySchema は CREATE TABLE IF NOT EXISTS を流すだけなので、**既に在るテーブルの
// 定義変更は一切反映されません**（列の追加・UNIQUE の変更など）。放っておくと、
// 起動は成功して保存だけが "no such column" で 500 になる、という気づきにくい壊れ方を
// します（設計総点検で「プラグインテーブルにマイグレーション機構が無い」と指摘された点）。
//
// cms.db は data/master から再生成できる派生索引なので、ずれを見つけたら作り直すのが
// 正しい対処です。判定は sqlite_master に保存された CREATE 文と宣言の文字列比較で、
// 空白の差は吸収します（宣言そのものが元のテーブルを作った文なので、版が同じなら一致する）。
func DriftedSchemaTables(db *sql.DB) []string {
	var drifted []string
	for _, p := range registry {
		for _, q := range p.Schema() {
			name := createdTableName(q)
			if name == "" {
				continue
			}
			var stored string
			err := db.QueryRow(
				`SELECT sql FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&stored)
			if err != nil {
				continue // 未作成なら ApplySchema がこれから作る＝ずれではない
			}
			if normalizeSQL(stored) != normalizeSQL(q) {
				drifted = append(drifted, name)
			}
		}
	}
	return drifted
}

// createdTableName は CREATE TABLE 文からテーブル名を取り出します。
func createdTableName(q string) string {
	f := strings.Fields(strings.ReplaceAll(q, "(", " ("))
	for i, w := range f {
		if strings.EqualFold(w, "EXISTS") && i+1 < len(f) {
			return strings.Trim(f[i+1], "(`\"")
		}
	}
	return ""
}

// normalizeSQL は空白の差と末尾のセミコロン・IF NOT EXISTS を落として比較可能にします。
func normalizeSQL(q string) string {
	q = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(q), ";"))
	q = strings.ReplaceAll(q, "IF NOT EXISTS ", "")
	return strings.Join(strings.Fields(q), " ")
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
// 読む形式はページのタグ（セクション外の素の dl。属性 data-type="tags" も互換で可）の
// <dt>{tagName}</dt><dd>値</dd>
// （鍵は dt の表示文字）。
func TagValue(root *html.Node, tagName string) string {
	var found string
	WalkElements(root, func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Data == "dl" && vocabTypeOf(n) == "tags" {
			found = dlTagValue(n, tagName)
		}
	})
	return found
}

// dlTagValue は <dl data-type="tags"> の中から名前 tagName の最初の値を返します。
// 鍵は dt の表示文字（自由語）です（②汎用索引と同じ規則）。
func dlTagValue(dl *html.Node, tagName string) string {
	found := ""
	eachDLPair(dl, false, func(key string, dd *html.Node) bool {
		if key == tagName {
			found = strings.TrimSpace(nodeText(dd))
			return false
		}
		return true
	})
	return found
}
