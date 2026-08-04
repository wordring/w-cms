package cms

import (
	"net/url"
	"sort"
	"strings"
	"sync"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ─────────────────────────────────────────────────────────────────────────
// 本文HTMLのサニタイズ（許可リスト方式）— docs/本文サニタイズ設計.md
//
// ページ本文はユーザー入力由来のHTMLであり、サーバーがシェル（assets/index.html）へ
// 埋め込んで返すため、危険な要素・属性が残っているとそのまま実行されてしまいます。
// そこで「許可したものだけを通す」方式で、以下の2箇所に同じ関数を通します。
//
//   - 保存時（SaveAPIHandler）: 正本ファイルを清書し、結果をエディタへ返して
//     編集者が画面上の変化で除去内容を把握できるようにする。
//   - 描画時（RootHandler の合成）: 保存経路を通っていない既存データ・バックアップ復元・
//     手動配置に対する最後の防壁。
//
// 冪等性（Sanitize(Sanitize(x)) == Sanitize(x)）を満たすことが、保存時エコーバックが
// 収束するための前提条件です（sanitize_test.go で検証）。
//
// 許可リストは docs/【一覧】カスタムタグ.md（現行実装の鏡）と同期させること。
// カスタム要素・属性を増減したら本ファイルと当該ドキュメントを同時に更新します。
// ─────────────────────────────────────────────────────────────────────────

// dangerousElements は部分木ごと削除する要素です（中身のテキストも残しません）。
// 理由は docs/本文サニタイズ設計.md §5.1 に要素ごとに記載。
var dangerousElements = map[string]bool{
	"script": true, "noscript": true, // スクリプト実行そのもの
	"style":  true,                                  // 任意CSSの注入
	"iframe": true, "frame": true, "frameset": true, // 外部文書の埋め込み
	"object": true, "embed": true, "applet": true, // プラグインの埋め込み
	"base": true,               // 相対URLの解決先を乗っ取れる
	"link": true, "meta": true, // 外部リソース読み込み・リダイレクト
	"svg": true, "math": true, // 内部にスクリプトを書ける
	"form": true, "input": true, "button": true, // 入力欄の偽装
	"textarea": true, "select": true, "option": true, // 〃
	"template": true, // 走査から外れたDOMを保持できる
	"dialog":   true, // モーダルで画面を覆える
}

// voidElements は子を持てない要素です（html.Render が子付きだとエラーになるため）。
var voidElements = map[string]bool{
	"br": true, "hr": true, "img": true, "wbr": true, "col": true,
	"source": true, "track": true,
}

// linkURLAttributes は**外部URLを許してよい**属性です（利用者が自分でクリックするもの）。
var linkURLAttributes = map[string]bool{"href": true, "cite": true}

// embedURLAttributes は**相対URLに限る**属性です。
// 外部URLは閲覧するだけで自動取得され、閲覧者のIPと閲覧時刻を第三者へ渡す
// （トラッキングビーコン）ため許可しない。CSP `default-src 'self'` でも遮断されるので、
// 許可しても壊れた表示になるだけで利益がない（docs/本文サニタイズ設計.md §5.5）。
var embedURLAttributes = map[string]bool{"src": true, "srcset": true, "poster": true}

// BlockIDAttr はブロック単位保存で使う識別子の属性名です。
//
// **任意の属性**であり、無くても構いません（無ければ全文保存になります）。
// そのため手で書いたHTMLやスクリプトが生成したHTMLもそのまま扱えます。
// 文書構造の都合であってドメインの語彙ではないので、プラグインではなくコアが持ちます。
//
// `id` ではなく data-* にしているのは、本文がシェル（assets/index.html）と同じDOMへ
// 合成されるため。`id` を許すと本文から `html-preview` などシェル側の要素を乗っ取れてしまい
// （getElementById は文書順で最初の要素を返す）、保存や権限UIが壊れる経路が開く。
const BlockIDAttr = "data-id"

// globalAttributes はどの許可要素にも共通で通す属性です。
// 要素ごとの許可リスト（構造HTML＋プラグイン宣言）に加えて評価されます。
var globalAttributes = map[string]bool{BlockIDAttr: true}

// structuralElements は「要素名 → 許可する属性の集合」のうち、**構造HTML**の分です。
// サニタイザはHTMLの安全性だけを知り、ドメインの語彙（カスタム要素 <m-*>）は持ちません。
// カスタム要素はすべてプラグインが Tags() で宣言し、allowedElements() が合成します。
//
// 方針は「**タグは寛容・属性は厳格**」。要素は文書として意味を持つ標準HTMLを危険でない限り
// 許可し、属性は必要なものだけ許可する（docs/本文サニタイズ設計.md §5.0）。
var structuralElements = map[string]map[string]bool{
	// 見出し（目次 buildToc が h1〜h6 を拾うので全段階を扱う）
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},

	// 段落・区切り（div/span は属性を失ってもブロック/インラインの境界として意味がある）
	"p": {}, "br": {}, "wbr": {}, "hr": {}, "pre": {}, "div": {}, "span": {},

	// 引用（他ページ参照は <m-quote> の役割。こちらはテキストの引用）
	"blockquote": {"cite": true},
	"q":          {"cite": true},
	"cite":       {},

	// リスト
	"ul": {}, "li": {}, "dl": {}, "dt": {}, "dd": {},
	"ol": {"start": true, "reversed": true, "type": true},

	// 文字装飾（b/i は strong/em へ正規化せず、貼り付けた表現をそのまま保つ）
	"strong": {}, "em": {}, "b": {}, "i": {}, "u": {}, "s": {},
	"code": {}, "mark": {}, "small": {}, "sub": {}, "sup": {},

	// 用語・注釈
	"abbr": {}, "dfn": {}, "kbd": {}, "samp": {}, "var": {},

	// 日時・値
	"time": {"datetime": true},
	"data": {"value": true},

	// ルビ（日本語の振り仮名。この用途のために入れている）
	"ruby": {}, "rt": {}, "rp": {},

	// 書字方向
	"bdi": {},
	"bdo": {"dir": true},

	// 区分（貼り付けた文書の構造を保つ）
	"section": {}, "article": {}, "header": {}, "footer": {},
	"aside": {}, "nav": {}, "address": {},
	"figure": {}, "figcaption": {},

	// 開閉（スクリプト不要で折りたためる）
	"details": {"open": true},
	"summary": {},

	// 変更履歴
	"ins": {"cite": true, "datetime": true},
	"del": {"cite": true, "datetime": true},

	// 表（生の table は貼り付けた資料としての表。業務明細は将来の <m-table> が担う）
	"table": {}, "caption": {}, "colgroup": {},
	"thead": {}, "tbody": {}, "tfoot": {}, "tr": {},
	"col": {"span": true},
	"th":  {"colspan": true, "rowspan": true, "scope": true, "abbr": true, "headers": true},
	"td":  {"colspan": true, "rowspan": true, "headers": true},

	// リンク（target/rel は許可しない。同一タブなら reverse tabnabbing の心配がない）
	"a": {"href": true, "title": true},

	// 画像・音声・動画（URLはいずれも相対のみ。autoplay は許可しない）
	"img":     {"src": true, "alt": true, "title": true, "width": true, "height": true},
	"picture": {},
	"video":   {"src": true, "poster": true, "controls": true, "width": true, "height": true, "loop": true, "muted": true, "preload": true},
	"audio":   {"src": true, "controls": true, "loop": true, "muted": true, "preload": true},
	"source":  {"src": true, "srcset": true, "type": true, "media": true},
	"track":   {"src": true, "kind": true, "srclang": true, "label": true},
}

// 注: 旧方式の <m-tag name="親ページID"> を取り込まない規則は、m-tag を所有する
// plugin_page_tags.go が持ちます。サニタイザはHTMLの安全性だけを見て、
// カスタム要素の意味には立ち入りません。

// allowedElementsCache は合成済みの許可リストです。プラグインは init() で登録され
// 実行中に増減しないため、初回に一度だけ組み立てて使い回します。
var (
	allowedElementsOnce  sync.Once
	allowedElementsCache map[string]map[string]bool
)

// allowedElements は「構造HTML ∪ 全プラグインが宣言したカスタム要素」を返します。
// プラグインが自分で読む属性を宣言するため、「読むのに許可し忘れる」不整合が起きません。
func allowedElements() map[string]map[string]bool {
	allowedElementsOnce.Do(func() {
		merged := make(map[string]map[string]bool, len(structuralElements))
		for el, attrs := range structuralElements {
			set := make(map[string]bool, len(attrs))
			for a := range attrs {
				set[a] = true
			}
			merged[el] = set
		}
		for _, spec := range PluginTags() {
			if merged[spec.Element] == nil {
				merged[spec.Element] = map[string]bool{}
			}
			for _, a := range spec.Attributes {
				merged[spec.Element][a] = true
			}
		}
		allowedElementsCache = merged
	})
	return allowedElementsCache
}

// AllowedVocabulary は「要素名 → 許可属性（ソート済み）」を返します。
//
// エディタはこれを /api/tag-schema 経由で受け取り、**シリアライザの語彙**として使います。
// サニタイザとエディタが同じ宣言から導かれるので、「サニタイザは通すのにエディタが
// 保存時に捨てる」という食い違いが起きません（かつて ul・table・h4〜h6 で起きていました）。
func AllowedVocabulary() map[string][]string {
	src := allowedElements()
	out := make(map[string][]string, len(src))
	for el, attrs := range src {
		list := make([]string, 0, len(attrs))
		for a := range attrs {
			list = append(list, a)
		}
		sort.Strings(list)
		out[el] = list
	}
	return out
}

// VoidElementNames は子を持てない要素名（ソート済み）を返します。
// エディタは終了タグを書かないために使います（`<br></br>` は不正なHTMLになる）。
func VoidElementNames() []string {
	list := make([]string, 0, len(voidElements))
	for el := range voidElements {
		list = append(list, el)
	}
	sort.Strings(list)
	return list
}

// Sanitize は本文HTMLを許可リストに従って安全な形へ整えて返します。
func Sanitize(s string) string {
	out, _ := SanitizeReport(s)
	return out
}

// SanitizeReport はサニタイズ結果と、それによって内容が変化したか（＝何かを除去したか）を返します。
// changed は「整形の差」ではなく「意味の差」を見るため、同じ描画器で出力した
// サニタイズ前後の文字列を比較して判定します（引用符の正規化等では true になりません）。
func SanitizeReport(s string) (out string, changed bool) {
	nodes, err := parseFragment(s)
	if err != nil {
		// パースできない入力は安全側に倒して全部落とす（通常は到達しない）。
		return "", s != ""
	}
	before := renderNodes(nodes)
	out = renderNodes(cleanNodes(nodes))
	return out, out != before
}

// parseFragment は本文（body の中身に相当する断片）をノード列としてパースします。
func parseFragment(s string) ([]*html.Node, error) {
	// DataAtom を設定しないと ParseFragment が解析モードを決められず何も返らない。
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	return html.ParseFragment(strings.NewReader(s), ctx)
}

// renderNodes はノード列をHTML文字列へ描画します。
func renderNodes(nodes []*html.Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		if err := html.Render(&sb, n); err != nil {
			continue
		}
	}
	return sb.String()
}

// cleanNodes はノード列をサニタイズした新しいノード列を返します（元の木は変更しません）。
func cleanNodes(nodes []*html.Node) []*html.Node {
	var out []*html.Node
	for _, n := range nodes {
		out = append(out, cleanNode(n)...)
	}
	return out
}

// cleanNode は1ノードをサニタイズします。削除なら空、アンラップなら子の列を返します。
func cleanNode(n *html.Node) []*html.Node {
	switch n.Type {
	case html.TextNode:
		return []*html.Node{{Type: html.TextNode, Data: n.Data}}

	case html.ElementNode:
		name := strings.ToLower(n.Data)

		// 危険要素は中身ごと削除する。
		if dangerousElements[name] {
			return nil
		}
		kids := cleanNodes(childNodes(n))

		allowedAttrs, ok := allowedElements()[name]
		if !ok {
			// 未知の要素はアンラップ（要素は捨てるが中身は残す）。
			return kids
		}

		el := &html.Node{
			Type:     html.ElementNode,
			DataAtom: n.DataAtom,
			Data:     name,
			Attr:     filterAttrs(n.Attr, allowedAttrs),
		}
		if !voidElements[name] {
			for _, c := range kids {
				el.AppendChild(c)
			}
		}
		return []*html.Node{el}

	default:
		// コメント・DOCTYPE などは落とす。
		return nil
	}
}

// childNodes は子ノードをスライスとして取り出します。
func childNodes(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// filterAttrs は許可された属性だけを元の順序を保って返します。
// on* ハンドラと style は、どの要素でも常に除去します（CSP strict 化の方針とも一致）。
func filterAttrs(attrs []html.Attribute, allowed map[string]bool) []html.Attribute {
	var out []html.Attribute
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		if strings.HasPrefix(key, "on") || key == "style" {
			continue
		}
		if !allowed[key] && !globalAttributes[key] {
			continue
		}
		// URLは用途で基準が違う。リンクは外部可、埋め込みは相対のみ（§5.5）。
		if linkURLAttributes[key] && !safeLinkURL(a.Val) {
			continue
		}
		if embedURLAttributes[key] && !safeEmbedURL(a.Val) {
			continue
		}
		out = append(out, html.Attribute{Key: key, Val: a.Val})
	}
	return out
}

// attrValue は属性値を返します（無ければ空文字列）。
func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// normalizeURL は判定用にURLを整えます。
// ブラウザはURL中のタブ・改行を無視するため、判定前に取り除きます
// （"java&#9;script:..." のような回避を防ぐ）。
func normalizeURL(v string) string {
	s := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
	return strings.TrimSpace(s)
}

// isRelativeURL は「同一オリジンの相対URL」かを判定します。
//
// スキームが空でも **`//host/path`（プロトコル相対URL）は別ホストを指す**ため、
// 相対URLとして扱ってはいけません（実際に穴として見つかり 2026-08-04 に塞ぎました）。
func isRelativeURL(s string) bool {
	if strings.HasPrefix(s, "//") {
		return false // プロトコル相対＝別ホスト
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}

// safeLinkURL は **リンク**（a[href]・cite）に許可するURLかを判定します。
// 利用者が自分でクリックするものなので、外部サイトへのリンクは正当として許可します。
func safeLinkURL(v string) bool {
	s := normalizeURL(v)
	if s == "" {
		return true
	}
	if isRelativeURL(s) {
		return true
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false // javascript: data: vbscript: など
}

// safeEmbedURL は **埋め込み**（img/video/audio/source/track の src 等）に許可するURLかを
// 判定します。**相対URLのみ**を許します。外部URLは閲覧するだけで自動取得され、閲覧者のIPと
// 閲覧時刻を第三者へ渡す（トラッキングビーコン）ためです。
func safeEmbedURL(v string) bool {
	s := normalizeURL(v)
	if s == "" {
		return true
	}
	return isRelativeURL(s)
}
