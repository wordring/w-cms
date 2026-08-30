package cms

// ─────────────────────────────────────────────────────────────────────────
// 配送係（Walker）——本文HTMLの回覧機構
//
// 設計の正本は [docs/【考察】パーサとプラグイン.md]。本文HTMLを処理する3つの経路
// （保存時の索引・表示時の計算ビュー・新規作成時のテンプレート新規化）を、1つの
// 走査へ寄せるための土台です。パーサは `x/net/html` のままで、**木を作ってから
// 文書順に歩いてイベントを押し込む**（SAX over DOM）形を取ります。
//
// 解いている問題は3つ:
//
//  1. **走査が3系統バラバラだった**——各プラグインが木を丸ごと受け取って自前で歩き、
//     計算ビューも新規化も別々に歩いていた。同じ木をプラグインの数だけ歩くうえ、
//     除外規則を**それぞれが覚えている必要**があった。
//  2. **除外規則が散在していた**——`.vocab-chrome` の中を読まない・テンプレート領域を
//     索引しない、が「走査するコードが覚えている規約」だった。配送係が**そもそも
//     イベントを発火しない**ようにすると、除外は規約から**観測できない事実**になる。
//  3. **一斉回覧ではない**——引き金（いまは `data-type`）の無い要素では誰も呼ばれず、
//     引き金のある要素でも呼ばれるのは表引きで当たった担当だけ。`p` や `h2` のような
//     大半のタグでプラグインが呼ばれることはありません。
//
// **引き金の解決は1箇所（triggerOf）に隔離**してあります。機能見出し
// （[【考察】語彙モデル.md](../../docs/【考察】語彙モデル.md) §11・未決定）が採用された
// ときに差し替わるのはそこだけで、本機構は決定を待たずに動きます。
//
// 段は3つあり、**それぞれ別の受け口・別のコンテキスト**を持ちます（最小権限）:
//
//   観察係（保存時）  … ObserveHandler / ObserveContext。書き込みTxを持つ。**Replace は無い**
//   鏡型（表示時）    … MirrorHandler  / MirrorContext。読み取り専用DB。Replace を持つ
//   種まき（新規作成）… SeedHandler    / SeedContext。読み取り専用DB。Replace を持つ
//
// 観察係に `Replace` が無いのは行儀の問題ではなく**型の問題**です。同じ理由で鏡型には
// 書き込みTxを渡しません——「派生→正本の逆流」（2026-08-21 に一度踏んだ）が
// コンパイル時に不可能になります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TriggerAll は「マーカーのある要素すべて」を受け取る引き金です。
// ②汎用索引がこの形で登録されます（未知の `data-type` も索引する現行仕様の表現）。
const TriggerAll = "*"

// ReadOnlyDB は読み取りだけを宣言するDBの口です。
//
// **ラッパーではなくインターフェース**にしているのがポイントで、鏡型・種まきへ
// これを渡すと `Exec` が存在しない＝書けないことが型として保証されます。
type ReadOnlyDB interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// ── 段別コンテキスト ─────────────────────────────────────────────────────

// walkState は歩行そのものの状態です（3つの段が埋め込んで共有する）。
type walkState struct {
	ancestors []*html.Node
	replaced  map[*html.Node]bool
	counters  map[string]int
}

// Counter は同じ鍵で呼ぶたびに 0, 1, 2 … を返します（1周のあいだの通し番号）。
// 同じ形式のブロックが複数あるとき、行や採番を区別するのに使います。
//
// 状態を**コンテキスト側**に置くのが要点です——ハンドラは登録時に1つだけ作られる
// singleton なので、そちらに持たせるとページ間で番号が漏れます。
func (w *walkState) Counter(key string) int {
	if w.counters == nil {
		w.counters = map[string]int{}
	}
	n := w.counters[key]
	w.counters[key] = n + 1
	return n
}

// Ancestors は現在の要素の祖先を、根に近い順で返します。
// ファイル容器の `data-src` を親から拾う、といった用途に使います。
func (w *walkState) Ancestors() []*html.Node { return w.ancestors }

// Closest は祖先のうち、fn が真を返す最も近いものを返します（無ければ nil）。
func (w *walkState) Closest(fn func(*html.Node) bool) *html.Node {
	for i := len(w.ancestors) - 1; i >= 0; i-- {
		if fn(w.ancestors[i]) {
			return w.ancestors[i]
		}
	}
	return nil
}

// ObserveContext は観察係（保存時）の権能です。**Replace はありません**。
type ObserveContext struct {
	walkState
	Tx     *sql.Tx // 書き込みは自分の索引テーブルへの洗い替えだけに使う
	PageID int
	// Meta はサイドカー（属性の正本）を読みます。読めなければエラーを返します
	// ——派生から正本を組み立て直さない、という決定（2026-08-21）どおり、
	// **黙って既定値で補完しない**。
	Meta func() (page.PageMeta, error)
	// Tag はページ横断メタ（`<dl data-type="tags">`）から名前で値を引きます。
	//
	// **ページ全体を見る必要がある少数の用途のため**にコアが用意する口です
	// （部材の `part_id` が代表例——部材表の行ではなくページのタグに書かれており、
	// タグが表より後ろにあっても引けなければならないので、文書順の回覧では拾えない）。
	// 木そのものは渡しません——渡すと「各自が自前で歩く」形へ逆戻りするからです。
	Tag func(name string) string

	counters map[string]int
}

// Counter は同じ鍵で呼ぶたびに 0, 1, 2 … を返します（ページ内の通し番号）。
// 同じ形式のブロックがページに複数あるとき、行を区別するのに使います
// （②汎用索引の `block_no`）。状態は**コンテキスト側**に置きます——観察係は
// 登録時に1つだけ作られる singleton なので、そちらに持たせるとページ間で漏れます。
func (c *ObserveContext) Counter(key string) int {
	if c.counters == nil {
		c.counters = map[string]int{}
	}
	n := c.counters[key]
	c.counters[key] = n + 1
	return n
}

// MirrorContext は鏡型（表示時）の権能です。書き込みDBは持ちません。
type MirrorContext struct {
	walkState
	DB     ReadOnlyDB
	Viewer *auth.User // 見せ分け（読めないページ由来の行を落とす）はここで効かせる
	PageID int
}

// SeedContext は種まき（新規作成時）の権能です。
type SeedContext struct {
	walkState
	DB        ReadOnlyDB
	NewPageID string
	Now       time.Time // 日付の既定値用。時計もコンテキスト経由（テストしやすさ）
}

// Replace は el を nodes で置き換えます（鏡型）。
//
// **素手でノードを繋ぎ替えさせない**のは配送の保証と直結します——「差し替え済みの
// 部分木へイベントを流さない」（再回覧なし・停止性）は、どこが差し替わったかを
// 配送係が知っている場合にだけ守れる約束だからです。
// 文字列APIは用意しません（`html.Render` がテキストと属性値を自動エスケープするので、
// ノードを組んで渡す形にしておけばエスケープの掛け忘れが起こりえない）。
func (c *MirrorContext) Replace(el *html.Node, nodes ...*html.Node) {
	c.walkState.replaceNodes(el, nodes...)
}

// Replace は el を nodes で置き換えます（種まき）。
func (c *SeedContext) Replace(el *html.Node, nodes ...*html.Node) {
	c.walkState.replaceNodes(el, nodes...)
}

// ReplaceChildren は el の中身だけを nodes で置き換えます（枠は残す）。
// 計算ビューのように「マーカーは本文に残し、中身だけサーバーが所有する」形で使います。
func (c *MirrorContext) ReplaceChildren(el *html.Node, nodes ...*html.Node) {
	for el.FirstChild != nil {
		el.RemoveChild(el.FirstChild)
	}
	for _, n := range nodes {
		el.AppendChild(n)
	}
	c.walkState.markReplaced(el)
}

// SetText は要素の中身をテキスト1つで置き換えます（種まきのセル埋め）。
func (c *SeedContext) SetText(el *html.Node, s string) {
	for el.FirstChild != nil {
		el.RemoveChild(el.FirstChild)
	}
	if s != "" {
		el.AppendChild(&html.Node{Type: html.TextNode, Data: s})
	}
	c.walkState.markReplaced(el)
}

func (w *walkState) replaceNodes(el *html.Node, nodes ...*html.Node) {
	parent := el.Parent
	if parent == nil {
		return
	}
	for _, n := range nodes {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		parent.InsertBefore(n, el)
		w.markReplaced(n)
	}
	parent.RemoveChild(el)
	w.markReplaced(el)
}

func (w *walkState) markReplaced(n *html.Node) {
	if w.replaced == nil {
		w.replaced = map[*html.Node]bool{}
	}
	w.replaced[n] = true
}

// ── 受け口 ───────────────────────────────────────────────────────────────
//
// 戻り値 descend が false なら、配送係は el の子孫へ降りません（担当済みの意思表示）。
// **既定は descend=true**——担当したハンドラだけが false を返す規約にしてあります。
// 読み漏らし（true の返し忘れ）より二重読み（false の返し忘れ）のほうが、テストでも
// 実データでも気づきやすいからです（設計 §10.1 の推奨どおり）。

// ObserveHandler は観察係（保存時）の受け口です。
type ObserveHandler interface {
	OnElement(ctx *ObserveContext, el *html.Node) (descend bool, err error)
}

// MirrorHandler は鏡型（表示時）の受け口です。
type MirrorHandler interface {
	OnElement(ctx *MirrorContext, el *html.Node) (descend bool, err error)
}

// SeedHandler は種まき（新規作成時）の受け口です。
type SeedHandler interface {
	OnElement(ctx *SeedContext, el *html.Node) (descend bool, err error)
}

// MirrorHandlerFunc は関数を MirrorHandler にします。
type MirrorHandlerFunc func(ctx *MirrorContext, el *html.Node) (bool, error)

func (f MirrorHandlerFunc) OnElement(ctx *MirrorContext, el *html.Node) (bool, error) {
	return f(ctx, el)
}

// SeedHandlerFunc は関数を SeedHandler にします。
type SeedHandlerFunc func(ctx *SeedContext, el *html.Node) (bool, error)

func (f SeedHandlerFunc) OnElement(ctx *SeedContext, el *html.Node) (bool, error) {
	return f(ctx, el)
}

// ── 登録表 ───────────────────────────────────────────────────────────────

type walkRegistry struct {
	observers map[string][]ObserveHandler // 引き金 → 担当（何人でも）
	mirrors   map[string]MirrorHandler    // 引き金 → 担当（1人だけ）
	seeders   map[string]SeedHandler      // 引き金 → 担当（1人だけ）
}

func newWalkRegistry() *walkRegistry {
	return &walkRegistry{
		observers: map[string][]ObserveHandler{},
		mirrors:   map[string]MirrorHandler{},
		seeders:   map[string]SeedHandler{},
	}
}

// walkers はコアが1つだけ持つ登録表です（`init()` から登録する現行の流儀に合わせる）。
var walkers = newWalkRegistry()

// 観察係（保存時）の登録口は Register（plugin.go）だけです——観察係は Schema/Tables を
// 持つプラグインとして登録され、observerAdapter が walkers.observe へ配線します。
// 引き金ごとの人数制限は無く、同じ要素を複数の索引が読んで構いません
// （読むだけで、書く先は各自の所有テーブル）。

// RegisterMirror は鏡型（表示時）を登録します。**引き金ごとに1人**——
// 「順番」に意味を持たせないための構造的な保証で、重複は起動時に落とします。
func RegisterMirror(trigger string, h MirrorHandler) { walkers.mirror(trigger, h) }

// RegisterSeeder は種まき（新規作成時）を登録します。引き金ごとに1人。
func RegisterSeeder(trigger string, h SeedHandler) { walkers.seed(trigger, h) }

func (r *walkRegistry) observe(trigger string, h ObserveHandler) {
	r.observers[trigger] = append(r.observers[trigger], h)
}

func (r *walkRegistry) mirror(trigger string, h MirrorHandler) {
	if _, dup := r.mirrors[trigger]; dup {
		panic(fmt.Sprintf("鏡型の引き金が重複しています: %q（引き金ごとに1人）", trigger))
	}
	r.mirrors[trigger] = h
}

func (r *walkRegistry) seed(trigger string, h SeedHandler) {
	if _, dup := r.seeders[trigger]; dup {
		panic(fmt.Sprintf("種まきの引き金が重複しています: %q（引き金ごとに1人）", trigger))
	}
	r.seeders[trigger] = h
}

// ── 引き金の解決（差し替え点） ───────────────────────────────────────────

// triggerOf は要素から引き金の鍵を求めます。**将来の差し替え点はここ1つ**です
// ——機能見出し（語彙モデル §11.1）が採用されたら、この関数だけが変わります。
func triggerOf(el *html.Node) string {
	if el.Type != html.ElementNode {
		return ""
	}
	return Attr(el, "data-type")
}

// isChrome は「サーバー／エンハンサが挿した表示専用の飾り」かを返します。
// この中は本文ではないので、配送係は歩きません。
func isChrome(el *html.Node) bool {
	return el.Type == html.ElementNode &&
		strings.Contains(Attr(el, "class"), chromeClass)
}

// chromeClass は表示専用クロームの印です（シリアライザ・サニタイザと同じ言葉）。
const chromeClass = "vocab-chrome"

// ── 走査 ─────────────────────────────────────────────────────────────────

// dispatch は1要素分の配送を行い、子孫へ降りるかを返します。
type dispatch func(el *html.Node) (descend bool, err error)

// walkFragment は文書順に歩き、引き金のある要素だけへ配送します。
// クロームの中と差し替え済みの部分木へは降りません。
//
// 断片（根の無いノード列）を**一時的な親へぶら下げてから**歩きます。
// 親が無いと `Replace` がトップレベルの要素を差し替えられない
// （`html.Node` の繋ぎ替えは親を通してしか行えない）ためです。
// 歩き終えたら外して返すので、呼び出し側から一時の親は見えません。
func walkFragment(nodes []*html.Node, st *walkState, d dispatch) ([]*html.Node, error) {
	root := &html.Node{Type: html.ElementNode, Data: "w-walk-root"}
	for _, n := range nodes {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		root.AppendChild(n)
	}

	var walkErr error
	for c := root.FirstChild; c != nil; {
		next := c.NextSibling
		if err := walkNode(c, st, d); err != nil {
			walkErr = err
			break
		}
		c = next
	}

	out := []*html.Node{}
	for c := root.FirstChild; c != nil; {
		next := c.NextSibling
		root.RemoveChild(c)
		out = append(out, c)
		c = next
	}
	return out, walkErr
}

func walkNode(n *html.Node, st *walkState, d dispatch) error {
	// 要素以外（文書ノード・テキスト・コメント）は配送の対象ではないが、
	// **子孫はある**ので降りる。ここで打ち切ると `html.Parse` が返す文書ノードを
	// 渡されたときに1つも配れない（本文が丸ごと索引されなくなる）。
	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if err := walkNode(c, st, d); err != nil {
				return err
			}
			c = next
		}
		return nil
	}
	// 差し替えた部分木へは配らない（再回覧なし＝停止する）。
	if st.replaced[n] {
		return nil
	}
	// 表示専用クロームの中は本文ではない。**そもそも歩かない**ので、
	// 各ハンドラが除外を覚えている必要が無い。
	if isChrome(n) {
		return nil
	}

	descend := true
	if t := triggerOf(n); t != "" {
		var err error
		descend, err = d(n)
		if err != nil {
			return err
		}
		// ハンドラが自分自身を差し替えたなら、そこで打ち止め。
		if st.replaced[n] {
			return nil
		}
	}
	if !descend {
		return nil
	}

	st.ancestors = append(st.ancestors, n)
	// 走査中に子が差し替わっても安全なように、次の兄弟を先に控える。
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if err := walkNode(c, st, d); err != nil {
			st.ancestors = st.ancestors[:len(st.ancestors)-1]
			return err
		}
		c = next
	}
	st.ancestors = st.ancestors[:len(st.ancestors)-1]
	return nil
}

// walkObserve は観察係の段を1周します。
// 引き金に当たった担当**全員**へ配ります（読むだけなので衝突しない）。
// 観察係はDOMを書き換えられない（Replace が無い）ので、木は戻しません。
func (r *walkRegistry) walkObserve(ctx *ObserveContext, nodes []*html.Node) error {
	_, err := walkFragment(nodes, &ctx.walkState, func(el *html.Node) (bool, error) {
		t := triggerOf(el)
		hs := append(append([]ObserveHandler{}, r.observers[TriggerAll]...), r.observers[t]...)
		if len(hs) == 0 {
			return true, nil
		}
		// **降りないのは全員が降りないと言ったときだけ**（OR）。
		//
		// 観察係どうしは独立で、書く先は各自の所有テーブルなので、同じ要素を2人が
		// 読んでも衝突しません。したがって1人が「担当した（descend=false）」と
		// 言っても、**他の担当のために木は降ります**——例えば受注セクションは
		// 受注の観察係が明細表ごと読みますが、②汎用索引（TriggerAll）は同じ明細表を
		// 自分でも索引する必要があります。ここを AND にすると索引が静かに欠けます。
		descend := false
		for _, h := range hs {
			d, err := h.OnElement(ctx, el)
			if err != nil {
				return false, err
			}
			if d {
				descend = true
			}
		}
		return descend, nil
	})
	return err
}

// walkMirror は鏡型の段を1周し、書き換わった木を返します（引き金ごとに担当は1人）。
func (r *walkRegistry) walkMirror(ctx *MirrorContext, nodes []*html.Node) ([]*html.Node, error) {
	return walkFragment(nodes, &ctx.walkState, func(el *html.Node) (bool, error) {
		h := r.mirrors[triggerOf(el)]
		if h == nil {
			if all := r.mirrors[TriggerAll]; all != nil {
				return all.OnElement(ctx, el)
			}
			return true, nil
		}
		return h.OnElement(ctx, el)
	})
}

// walkSeed は種まきの段を1周し、書き換わった木を返します（引き金ごとに担当は1人）。
func (r *walkRegistry) walkSeed(ctx *SeedContext, nodes []*html.Node) ([]*html.Node, error) {
	return walkFragment(nodes, &ctx.walkState, func(el *html.Node) (bool, error) {
		h := r.seeders[triggerOf(el)]
		if h == nil {
			if all := r.seeders[TriggerAll]; all != nil {
				return all.OnElement(ctx, el)
			}
			return true, nil
		}
		return h.OnElement(ctx, el)
	})
}
