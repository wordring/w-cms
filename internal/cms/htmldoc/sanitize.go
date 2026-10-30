// Package htmldoc は、本文HTMLを許可リスト方式でサニタイズする純粋なHTML部品です。
//
// このパッケージは**アプリのドメイン（プラグイン・レジストリ）を一切知りません**。
// 本文で扱えるHTMLの語彙（structuralElements＋`data-*` マーカー）はここが唯一の正本で、
// 依存は `cms → htmldoc` の一方向、逆向きはありません。
//
// かつては各プラグインが宣言したカスタム要素の語彙を New() の引数として
// 注入していました（cms 内で PluginTags() を呼ぶ循環を解くための反転。変更履歴
// 2026-08-06 の選択肢(b)）。語彙モデルへの移行完了（2026-08-20）でカスタム要素が
// ゼロになり、注入そのものが不要になっています。
package htmldoc

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ─────────────────────────────────────────────────────────────────────────
// 本文HTMLのサニタイズ（許可リスト方式）— docs/旧文書/本文サニタイズ設計.md
//
// ページ本文はユーザー入力由来のHTMLであり、サーバーがシェル（assets/index.html）へ
// 埋め込んで返すため、危険な要素・属性が残っているとそのまま実行されてしまいます。
// そこで「許可したものだけを通す」方式で、以下の2箇所に同じ関数を通します。
//
//   - 保存時（SaveAPIHandler。2026-10-12 からの画面の保存口 SyncAPIHandler〔/api/sync〕も）: 正本ファイルを
//     清書し、結果をエディタへ返して編集者が画面上の変化で除去内容を把握できるようにする。
//   - 描画時（RootHandler の合成）: 保存経路を通っていない既存データ・バックアップ復元・
//     手動配置に対する最後の防壁。
//
// 冪等性（Sanitize(Sanitize(x)) == Sanitize(x)）を満たすことが、保存時エコーバックが
// 収束するための前提条件です（cms パッケージの sanitize_test.go で検証）。
//
// 許可リストを増減したら docs/旧文書/本文サニタイズ設計.md §5 を同時に更新すること。
// ─────────────────────────────────────────────────────────────────────────

// dangerousElements は部分木ごと削除する要素です（中身のテキストも残しません）。
// 理由は docs/旧文書/本文サニタイズ設計.md §5.1 に要素ごとに記載。
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
// 許可しても壊れた表示になるだけで利益がない（docs/旧文書/本文サニタイズ設計.md §5.5）。
var embedURLAttributes = map[string]bool{"src": true, "srcset": true, "poster": true,
	// data-src はファイル容器（section[data-type="file"]）の配線。エンハンサが
	// プレビューのURLに使うため、埋め込みと同じく相対URLに限る（多層防御）。
	"data-src": true}

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

// ShellIDPrefix は**殻（assets/index.html）が独占する id の接頭辞**です。
//
// 本文は殻と同じDOMへ合成されるため、id の名前空間を共有します。getElementById は
// 文書順で最初の要素を返すので、本文に殻と同じ id を書かれると権限UIの入力欄などを
// 乗っ取れます（本文の挿入点より後ろに pp-mode・pp-owner-input 等が並ぶ）。
// これが 2026-08-02 に id を全面拒否した理由でした。
//
// 2026-08-20 に分担を**反転**させました——**殻の側が接頭辞を独占し、本文の id は自由**。
// 守るべき規律が「無数の書き手」から「1つの殻」へ移り、本文ではページ内リンクが普通に書けます。
// 本文に接頭辞つきの id が現れたら、名前空間を侵さないよう**接頭辞を剥がして**通します
// （拒否ではなく告知の流儀）。殻の側の後戻りは internal/cms/shell_id_test.go が検出します。
const ShellIDPrefix = "w-"

// stripShellIDPrefix は本文の id から殻の接頭辞を剥がします。
//
// **無くなるまで繰り返す**のは冪等性のためです。1回だけだと "w-w-x" が
// 1回目のサニタイズで "w-x"、2回目で "x" となり、
// Sanitize(Sanitize(v)) == Sanitize(v) が崩れて保存時エコーバックが収束しません。
func stripShellIDPrefix(v string) string {
	for {
		switch {
		case strings.HasPrefix(v, ShellIDPrefix):
			v = v[len(ShellIDPrefix):]
		case strings.HasPrefix(v, ScriptGlobalPrefix):
			v = v[len(ScriptGlobalPrefix):]
		default:
			return v
		}
	}
}

// ScriptGlobalPrefix は、画面の JS が window に置く名前の頭です（window.wcmsMailCompose など）。
// ブラウザは id を持つ要素を同じ名前で window から引けるので、本文に id="wcmsMailCompose" があると、その名前の JS が
// 要素に化けて、送る欄などが黙って動かなくなりました（2026-10-26）。本文の id からは、この頭も剥がします。
const ScriptGlobalPrefix = "wcms"

// globalAttributes はどの許可要素にも共通で通す属性です。
// 要素ごとの許可リスト（構造HTML＋`data-*` マーカー）に加えて評価されます。
// id は値を検査（接頭辞を剥がす）してから通します。
var globalAttributes = map[string]bool{BlockIDAttr: true, "id": true}

// structuralElements は「要素名 → 許可する属性の集合」のうち、**構造HTML**の分です。
// サニタイザはHTMLの安全性だけを知り、ドメインの語彙（どの `data-type` がどんな形式か）は
// 持ちません。形式の宣言は①語彙レジストリ（cms/vocab.go）の担当で、ここは
// 「マーカー属性を不活性な値として通す」ところまでを受け持ちます。
//
// 方針は「**タグは寛容・属性は厳格**」。要素は文書として意味を持つ標準HTMLを危険でない限り
// 許可し、属性は必要なものだけ許可する（docs/旧文書/本文サニタイズ設計.md §5.0）。
var structuralElements = map[string]map[string]bool{
	// 見出し（目次 buildToc が h1〜h6 を拾うので全段階を扱う）
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},

	// 段落・区切り（div/span は属性を失ってもブロック/インラインの境界として意味がある）
	"p": {}, "br": {}, "wbr": {}, "hr": {}, "pre": {}, "div": {}, "span": {},

	// 引用（他ページ参照は「名前：値」の参照タグの役割。こちらはテキストの引用）
	"blockquote": {"cite": true},
	"q":          {"cite": true},
	"cite":       {},

	// リスト（dl の data-type は語彙モデルのマーカー。下記「表」の注記を参照）
	"ul": {}, "li": {},
	"dl": {"data-type": true},
	"dt": {},
	"dd": {},
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

	// 区分（貼り付けた文書の構造を保つ）。section の data-type は業務文書ブロックの
	// 外形（語彙モデル §8.2 論点A・案1）、data-src はファイル容器の配線（PDFパス）、
	// data-ref はファイル表示・編集の配線（`ページID-添付ID`。2026-09-15）。
	//
	// **data-ref は URL ではありません**ので safeEmbedURL を通しません——通す意味が
	// ないからです。値は w-cms 自身の参照（6桁-英数字）で、そこからURLを組むのは
	// サーバー（internal/cms/file_view.go）です。**書く人が宛先を選べない**という
	// 性質は、ここで値を検査するからではなく、URLを機械が組むことで守られます
	// （docs/旧文書/セキュリティ設計.md §4）。だから他の data-* と同じく不活性な文字列として通します。
	// data-mirror は鏡（計算ビュー）の印——値は鏡の表示名（`<section data-mirror="必要部材表">`・
	// 2026-09-27）。これも不活性な文字列で、名前から描く中身を選ぶのはサーバーです。
	"section": {"data-type": true, "data-src": true, "data-ref": true, "data-mirror": true}, "article": {}, "header": {}, "footer": {},
	"aside": {}, "nav": {}, "address": {},
	"figure": {}, "figcaption": {},

	// 開閉（スクリプト不要で折りたためる）
	"details": {"open": true},
	"summary": {},

	// 変更履歴
	"ins": {"cite": true, "datetime": true},
	"del": {"cite": true, "datetime": true},

	// 表。data-type は「マーカー付き標準HTML」（docs/考察/【考察】語彙モデル.md）の
	// 役割マーカーで、**属性名だけを要素限定で許可**し、値は不活性な文字列として検査しない。
	// 許可範囲は data-type→table・dl・section・th、data-src・data-ref・data-mirror→section に限る
	// （決定ログ＝同書 §9、論点A採用＝§8.2 の section 追加、data-field 撤去＝2026-08-20）。
	// **項目の鍵は見出しの表示文字が運ぶ**ので、機械キーを本文へ書き出す属性は持たない。
	// data-ref はこの原則の外です——指すのは**項目**ではなく**ファイル1つ**で、
	// 改名で壊れる「見出しと集計の対応」が存在しません（2026-09-15 の決定ログ）。
	// レジストリ（cms パッケージの語彙レジストリ）は編集支援と索引の語彙であって
	// 安全性の門ではない——未知の data-type も通す（保存時に告知するのは cms 側の責務）。
	"table": {"data-type": true}, "caption": {}, "colgroup": {},
	"thead": {}, "tbody": {}, "tfoot": {}, "tr": {},
	"col": {"span": true},
	"th":  {"colspan": true, "rowspan": true, "scope": true, "abbr": true, "headers": true, "data-type": true},
	"td":  {"colspan": true, "rowspan": true, "headers": true},

	// リンク（target/rel は許可しない。同一タブなら reverse tabnabbing の心配がない）
	// download はダウンロード時の保存名（添付のURLは生成IDなので、元の名前は
	// リンクが運ぶ——2026-08-31「一致させるとダウンロードで困るでしょうか？」への答え）。
	// 値はブラウザが保存名の既定に使うだけの不活性なテキスト。
	"a": {"href": true, "title": true, "download": true},

	// 画像・音声・動画（URLはいずれも相対のみ。autoplay は許可しない）
	"img":     {"src": true, "alt": true, "title": true, "width": true, "height": true},
	"picture": {},
	"video":   {"src": true, "poster": true, "controls": true, "width": true, "height": true, "loop": true, "muted": true, "preload": true},
	"audio":   {"src": true, "controls": true, "loop": true, "muted": true, "preload": true},
	"source":  {"src": true, "srcset": true, "type": true, "media": true},
	"track":   {"src": true, "kind": true, "srclang": true, "label": true},
}

// 注: タグの名前の意味（どの語をどう読むか）は、その語彙を所有する
// 語彙レジストリと汎用索引（cms パッケージ）が持ちます。サニタイザはHTMLの安全性だけを見て、
// カスタム要素の意味には立ち入りません。
// （ここはかつて「『親ページID』というタグ名を取り込まない規則」を指していましたが、その守りは
// 2026-09-14 に消えています——cms の vocab_index.go の syncVocabDL。2026-10-09 からは、通信の拡張が
// `親ページID` を通信記録の親子の参照タグとして使います。）

// Sanitizer は合成済みの許可リストを持つサニタイザです。New() で構築します。
// 構築後は読み取り専用なので、複数ゴルーチンから同時に使えます。
type Sanitizer struct {
	allowed map[string]map[string]bool
}

// New は構造HTMLの語彙を持つサニタイザを返します。
//
// かつては各プラグインが宣言したカスタム要素の語彙を引数で**注入**していましたが、
// 語彙モデルへの移行完了（2026-08-20）でカスタム要素はゼロになり、本文の語彙は
// この structuralElements ＋ `data-*` マーカーだけになりました。
func New() *Sanitizer {
	merged := make(map[string]map[string]bool, len(structuralElements))
	for el, attrs := range structuralElements {
		set := make(map[string]bool, len(attrs))
		for a := range attrs {
			set[a] = true
		}
		merged[el] = set
	}
	return &Sanitizer{allowed: merged}
}

// AllowedVocabulary は「要素名 → 許可属性（ソート済み）」を返します。
//
// エディタはこれを /api/tag-schema 経由で受け取り、**シリアライザの語彙**として使います。
// サニタイザとエディタが同じ宣言から導かれるので、「サニタイザは通すのにエディタが
// 保存時に捨てる」という食い違いが起きません（かつて ul・table・h4〜h6 で起きていました）。
func (s *Sanitizer) AllowedVocabulary() map[string][]string {
	out := make(map[string][]string, len(s.allowed))
	for el, attrs := range s.allowed {
		list := make([]string, 0, len(attrs)+len(globalAttributes))
		for a := range attrs {
			list = append(list, a)
		}
		// 全要素共通の属性（data-id・id）も含める。エディタのシリアライザは
		// これを語彙として使うので、含めないと「サーバーは通すのにエディタが落とす」
		// 非対称が生まれ、保存のたびに id が消える。
		for a := range globalAttributes {
			if !attrs[a] {
				list = append(list, a)
			}
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
func (s *Sanitizer) Sanitize(str string) string {
	out, _ := s.SanitizeReport(str)
	return out
}

// SanitizeReport はサニタイズ結果と、それによって内容が変化したか（＝何かを除去したか）を返します。
// changed は「整形の差」ではなく「意味の差」を見るため、同じ描画器で出力した
// サニタイズ前後の文字列を比較して判定します（引用符の正規化等では true になりません）。
func (s *Sanitizer) SanitizeReport(str string) (out string, changed bool) {
	nodes, err := ParseFragment(str)
	if err != nil {
		// 読めない入力は、深すぎる所の枠を外して読み直す（2026-10-30）。読めない理由は入れ子の深さです——x/net の html は開いた要素が
		// 512 を超えると読むのをやめます。それまでは全部を捨てていたので、1か所が深いだけで、保存した本文や開いたページが丸ごと空になった。
		// 枠を外しても読めなければ、文字だけを1つの段落に残す。文字は捨てない。
		if nodes, err = ParseFragment(flattenDeepNesting(str)); err != nil {
			if nodes, err = ParseFragment("<p>" + html.EscapeString(textOfMarkup(str)) + "</p>"); err != nil {
				return "", str != ""
			}
		}
		return renderNodes(s.cleanNodes(nodes)), true
	}
	before := renderNodes(nodes)
	out = renderNodes(s.cleanNodes(nodes))
	return out, out != before
}

// maxNesting は、読めない入力の枠を外すときに残す入れ子の深さです（x/net の上限 512 より十分に低く）。
const maxNesting = 256

// voidTags は閉じタグを持たない要素です（入れ子に数えない）。
var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// flattenDeepNesting は、入れ子が maxNesting より深い所のタグだけを外した文字列を返します（中の文字は残す）。
// 閉じていないタグも1段に数えるので、実際より浅くは数えません（読めなかった入力にだけ使うので、多めに外しても困らない）。
func flattenDeepNesting(str string) string {
	var sb strings.Builder
	depth := 0
	z := html.NewTokenizer(strings.NewReader(str))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return sb.String()
		}
		raw := z.Raw()
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			if voidTags[string(name)] {
				break
			}
			depth++
			if depth > maxNesting {
				continue
			}
		case html.EndTagToken:
			depth--
			if depth >= maxNesting {
				continue
			}
			if depth < 0 {
				depth = 0
			}
		}
		sb.Write(raw)
	}
}

// textOfMarkup は、タグを外した文字だけを返します（ブロックの間は改行）。
func textOfMarkup(str string) string {
	var sb strings.Builder
	z := html.NewTokenizer(strings.NewReader(str))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(sb.String())
		case html.TextToken:
			sb.Write(z.Text())
			sb.WriteByte('\n')
		}
	}
}

// ParseFragment は本文（body の中身に相当する断片）をノード列としてパースします。
// サニタイズ以外（ブロック差し替え等）でも本文を同じ規則でパースするために公開しています。
func ParseFragment(s string) ([]*html.Node, error) {
	// DataAtom を設定しないと ParseFragment が解析モードを決められず何も返らない。
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	return html.ParseFragment(strings.NewReader(s), ctx)
}

// Render はノード列をHTML文字列へ描画します（ParseFragment の逆）。
//
// 本文を木にして書き換え、文字列へ戻す口はすべてここを通します——同じ3行が
// cms パッケージの5か所に写されていました（2026-09-21 に寄せた）。
func Render(nodes []*html.Node) string {
	return renderNodes(nodes)
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
func (s *Sanitizer) cleanNodes(nodes []*html.Node) []*html.Node {
	var out []*html.Node
	for _, n := range nodes {
		out = append(out, s.cleanNode(n)...)
	}
	return out
}

// cleanNode は1ノードをサニタイズします。削除なら空、アンラップなら子の列を返します。
func (s *Sanitizer) cleanNode(n *html.Node) []*html.Node {
	switch n.Type {
	case html.TextNode:
		return []*html.Node{{Type: html.TextNode, Data: n.Data}}

	case html.ElementNode:
		name := strings.ToLower(n.Data)

		// 危険要素は中身ごと削除する。
		if dangerousElements[name] {
			return nil
		}
		kids := s.cleanNodes(childNodes(n))

		allowedAttrs, ok := s.allowed[name]
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
		// 本文の id は自由だが、殻が独占する接頭辞だけは侵させない（ShellIDPrefix）。
		if key == "id" {
			v := stripShellIDPrefix(a.Val)
			if v == "" {
				continue // 接頭辞だけの id は指し先の名前が残らないので落とす
			}
			out = append(out, html.Attribute{Key: key, Val: v})
			continue
		}
		// URLは用途で基準が違う。リンクは外部可、埋め込みは相対のみ（§5.5）。
		if linkURLAttributes[key] && !safeLinkURL(a.Val) {
			continue
		}
		if key == "srcset" && !safeSrcset(a.Val) || key != "srcset" && embedURLAttributes[key] && !safeEmbedURL(a.Val) {
			continue
		}
		out = append(out, html.Attribute{Key: key, Val: a.Val})
	}
	return out
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
	// 逆スラッシュは、ブラウザが「/」と読む（2つ重ねると外の宛先・1つなら /api/… になる）。
	if strings.ContainsRune(s, '\\') {
		return false
	}
	if !isRelativeURL(s) {
		return false
	}
	// 符号にした点・区切り（%2e・%2f・%5c）も断る——ブラウザは `/data/%2e%2e/api/x` の %2e%2e を「..」と読み、/api/x を
	// 叩いた（2026-10-27 の見直し。下の「..」の検査は文字どおりの点しか見ていなかった）。添付の名前に符号は入らない。
	if l := strings.ToLower(s); strings.Contains(l, "%2e") || strings.Contains(l, "%2f") || strings.Contains(l, "%5c") {
		return false
	}
	// **絶対パスの宛先は添付置き場（/data/）に限る**（2026-08-21 ユーザー決定）。
	//
	// 相対URLというだけで通していたころ、本文へ `<img src="/api/logout">` を1つ保存すると
	// そのページを開いた全員が無音でログアウトさせられた（保存型CSRF）。個々の口は
	// メソッドを絞って塞いだが、**本文からアプリのアドレスを叩けること自体**が残っていた。
	// 宛先を限れば、GETで何かが起きる口を将来足しても本文からは届かない——
	// 「存在するか」の検査では止められない（/api/logout は実在する正規の口だから）。
	//
	// ページからの相対パス（`a.png`・`img/a.png`）は従来どおり通す。添付は
	// ページのフォルダに置かれ、配信は /data/ 配下の認可付きハンドラが担う
	// （page.DataFileHandler）。
	// ⚠ ただしページのアドレスは `/000012`（末尾の `/` なし）なので、相対名は**サイトのルート**から
	// 解かれます（`a.png` → `/a.png`・`api/x` → `/api/x`——upload_image.go の冒頭の注記と同じ事情）。
	// つまりこの枝は「宛先を /data/ ときれいなURLに限る」の外にありました（2026-10-20 に気づいた・2026-10-26 に名前1つだけに絞った——下）。
	// リンク（a[href]）は別扱いで制限しない——
	// 押さないと何も起きず、埋め込みのように自動取得されないため。
	if strings.HasPrefix(s, "/") {
		if strings.Contains(s, "..") {
			return false
		}
		// 添付の配信口は2つ——旧形（/data/ 配下・互換）と、きれいなURL
		// （/<ページID6桁>/<ファイル名>。2026-08-31「実際に保存される場所を
		// 推測されたくない」）。どちらも認可つきのハンドラが配る。
		return strings.HasPrefix(s, dataDirPrefix) || attachmentURLRe.MatchString(s)
	}
	// 相対の名前は、名前1つだけ（「/」も「?」も無い）を通す（2026-10-26）。サイトのルートから解かれるので、「api/x」の形は
	// /api/x になり、開くたびに読む人みんなの手でアプリの口を叩けた。名前1つなら /a.png のような根の名前にしかならない。
	// ⚠ 名前1つでも、拡張子の無い名前（`002025`）は断る（2026-10-27 の見直し）——/002025 はページの住所で、開いた人みんなの
	// 手でそのページを描かせ（描くときに書き換える鏡・PDF の絵づくり・写真の写しづくりが走る）られた。
	return !strings.ContainsAny(s, "/?#") && strings.Contains(strings.TrimPrefix(s, "."), ".")
}

// safeSrcset は srcset（「URL 幅, URL 幅, …」）の候補のURLを1つずつ確かめます。
// ⚠ 全体を1つのURLとして見ると、2つ目の候補に /api/… を書けた（2026-10-22 の見直し）。
func safeSrcset(v string) bool {
	for _, cand := range strings.Split(v, ",") {
		f := strings.Fields(cand)
		if len(f) == 0 {
			continue
		}
		if !safeEmbedURL(f[0]) {
			return false
		}
	}
	return true
}

// dataDirPrefix は埋め込みに許す絶対パスの接頭辞です（添付の旧配信口）。
const dataDirPrefix = "/data/"

// attachmentURLRe は添付のきれいなURL（/<ページID6桁>/<ファイル名>）です。
// ファイル名に / は含まれない（深い階層はアプリの口を指しうるので許さない）。
var attachmentURLRe = regexp.MustCompile(`^/[0-9]{6}/[^/]+$`)
