package cms

import (
	"net/url"
	"strings"

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
var dangerousElements = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"form": true, "input": true, "button": true, "textarea": true, "select": true,
	"option": true, "link": true, "meta": true, "base": true, "svg": true, "math": true,
	"template": true, "noscript": true, "frame": true, "frameset": true, "applet": true,
}

// voidElements は子を持てない要素です（html.Render が子付きだとエラーになるため）。
var voidElements = map[string]bool{
	"br": true, "hr": true, "img": true, "wbr": true, "col": true,
}

// urlAttributes はURLとして検証する属性です（javascript: 等を弾く）。
var urlAttributes = map[string]bool{"href": true, "src": true}

// allowedElements は「要素名 → 許可する属性の集合」です。ここに無い要素はアンラップし
// （要素は捨てるが子・テキストは残す）、レガシーHTMLの本文が消えないようにします。
var allowedElements = map[string]map[string]bool{
	// ── 構造・テキスト ──
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},
	"p": {}, "br": {}, "hr": {}, "div": {}, "span": {},
	"ul": {}, "li": {}, "blockquote": {}, "pre": {}, "code": {},
	"strong": {}, "em": {}, "b": {}, "i": {}, "u": {}, "s": {},
	"ol":    {"start": true},
	"a":     {"href": true, "title": true},
	"img":   {"src": true, "alt": true, "title": true, "width": true, "height": true},
	"table": {}, "thead": {}, "tbody": {}, "tr": {},
	"th":    {"colspan": true, "rowspan": true},
	"td":    {"colspan": true, "rowspan": true},

	// ── カスタム要素（docs/【一覧】カスタムタグ.md §1 の6種）──
	"m-tag": {"name": true, "value": true},
	// m-file は tag の値で意味が変わり、tag ごとに別のプラグインが別の属性を読む。
	// ここは全 tag 分の和集合。**プラグインが読む属性（internal/cms/plugin_*.go）を
	// 落とさないこと**（落とすと保存のたびに値が消え、集計が壊れる）。
	// 例: item-name は <m-file tag="材料屋・加工業者の見積もり"> で
	// plugin_estimates.go が supplier_estimates.item_name として読む。
	"m-file": {
		"src": true, "name": true, "tag": true,
		"order-no": true, "client-name": true, "supplier-name": true,
		"item-id": true, "item-name": true, "price": true, "cost": true, "quantity": true,
		"ordered-at": true, "estimated-at": true,
	},
	"m-item": {
		"item-id": true, "item-name": true, "price": true,
		"cost": true, "quantity": true, "status": true,
	},
	"m-material": {
		"item-name": true, "cost": true, "supplier-name": true, "quantity": true,
	},
	"m-required-materials": {"page-id": true},
	"m-child-list":         {},
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
		// 旧形式の親ページIDタグは取り込まない（属性はサイドカーが正本）。
		if name == "m-tag" && attrValue(n, "name") == "親ページID" {
			return nil
		}

		kids := cleanNodes(childNodes(n))

		allowedAttrs, ok := allowedElements[name]
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
		if !allowed[key] {
			continue
		}
		if urlAttributes[key] && !safeURL(a.Val) {
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

// safeURL は href/src に許可するURLかを判定します。
// 相対URLと http/https/mailto のみ許可し、javascript: や data: 等は拒否します。
// ブラウザはURL中のタブ・改行を無視するため、判定前に取り除きます
// （"java\tscript:..." のような回避を防ぐ）。
func safeURL(v string) bool {
	s := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		return true // 相対URL
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	}
	return false
}
