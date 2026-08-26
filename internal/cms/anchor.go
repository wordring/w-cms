// Package cms は w-cms の中身——ページの保存・描画・索引・計算——を担います。
//
// # 正本はファイル、DBは派生
//
// 正本は data/master 配下の本文HTML（内容）と属性サイドカー（[w-cms/internal/cms/page]）で、
// cms.db は**そこから丸ごと再生成できる索引**です（[RebuildDatabase]）。
// この不変条件が、リバート・DB再構築・バックアップの単純さを支えています——
// ファイルから作れない値をDBへ持たせてはいけません。
//
// # 本文は「マーカー付き標準HTML」
//
// 本文は構造HTML（table / dl / section …）に `data-type` の印を付けただけのもので、
// カスタム要素はありません。印の意味は**語彙レジストリ**（vocab.go の3層モデル①）が
// 宣言データとして持ち、形式を増やすのは原則この宣言1件で済みます。
// 項目の鍵は常に**見出しの表示文字**（th / dt）で、機械キーは本文に現れません。
//
// # 3つの層
//
//	① 語彙レジストリ … 形式の宣言（vocab.go）。編集支援・型推論・正規化の語彙
//	② 汎用索引       … 全マーカーを縦持ちで写す（vocab_index.go）。形式が増えても不変
//	③ 計算プラグイン … 業務ごとのテーブルと集計（plugin_*.go）。計算が要るときだけ書く
//
// # 本文を読むのは配送係だけ
//
// 本文の走査はコアの**配送係**（walk.go）が1回だけ行い、引き金（`data-type`）に当たった
// 要素を担当へ届けます。段は3つ——保存時の[Observer]（観察係）・表示時の[MirrorHandler]
// （鏡型）・新規作成時の[SeedHandler]（種まき）——で、それぞれ別のコンテキストを持ちます。
// 観察係に Replace が無く鏡型に書き込みDBが無いのは**型による最小権限**で、
// 「派生→正本の逆流」がコンパイル時に不可能になります。
//
// # サニタイズしてから足す
//
// 本文は保存時と描画時の**二層**でサニタイズします（実体は
// [w-cms/internal/cms/htmldoc]）。サニタイズの**後**にHTMLを足す関数が4つあり
// （[RenderComputedViews]・[RenderAnchors]・[RenderPageShell]・[RenderPublicShell]）、
// それぞれが自前でエスケープ責任を負います——この規律が破れると保存型XSSになります。
package cms

// ─────────────────────────────────────────────────────────────────────────
// ページ内アンカーのサーバー合成
//
// 本文の `id` は書き手が自由に書けますが（殻が接頭辞 w- を独占する分担。
// docs/本文サニタイズ設計.md 5.3）、**何も書かなくてもリンクできる**ように、
// ページを返すときだけ見出しとブロックへ id を合成します。
//
//   <h2 data-id="a7k2m9">発注書A</h2>  →  <h2 data-id="a7k2m9" id="発注書A">発注書A</h2>
//   <p  data-id="a7k2m9">本文</p>      →  <p  data-id="a7k2m9" id="a7k2m9">本文</p>
//
// **保存形式は変えません。** 合成するのは RootHandler（ページ描画）だけで、
// エディタが編集モードで読み直す GET /api/load は通しません——通すと合成した id が
// エディタのDOMへ入り、シリアライザが本文として保存してしまいます
// （`id` は語彙に含まれる属性なので、.vocab-chrome のように飛ばされません）。
//
// これにより、書き手が何も書いていない既存ページも**遡って**アンカーを持ちます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// anchorDropChars はアンカー名に残さない文字です。URLの区切り・HTMLの引用符など、
// リンクとして書いたときに壊れるものだけを落とします（日本語はそのまま残す）。
var anchorDropChars = `"'<>&#/?%{}|^[]` + string([]rune{92, 96}) // 92=バックスラッシュ 96=バッククォート

// RenderAnchors は本文HTMLの見出し（h1〜h6）とブロック（data-id 持ち）へ
// `id` を合成して返します。**書き手が付けた `id` は上書きしません。**
//
// 見出しは表示文字から、それ以外はブロックIDからアンカー名を作ります。
// 見出しを優先するのは「見える文字が鍵」（語彙モデル §5.1）と同じ考えで、
// `#発注書A` のように人が読み書きできるリンクになるからです。
//
// 冪等です——2度通しても、既に `id` がある要素は素通りするので結果が変わりません。
func RenderAnchors(bodyHTML string) string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}

	// 1周目: 既にある id を集める。合成はこれを避ける（書き手の名前が勝つ）。
	used := map[string]bool{}
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if v := Attr(el, "id"); v != "" {
				used[v] = true
			}
		})
	}

	// 2周目: id の無い要素へ合成する。
	changed := false
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if Attr(el, "id") != "" || inVocabChrome(el) {
				return
			}
			base := anchorBase(el)
			if base == "" {
				return
			}
			id := uniqueAnchor(base, used)
			el.Attr = append(el.Attr, html.Attribute{Key: "id", Val: id})
			used[id] = true
			changed = true
		})
	}
	if !changed {
		return bodyHTML
	}

	var sb strings.Builder
	for _, n := range nodes {
		html.Render(&sb, n)
	}
	return sb.String()
}

// inVocabChrome は要素がサーバー描画の編集クローム（.vocab-chrome）の中にあるかを返します。
//
// クロームは本文ではない（シリアライザが保存しない）ので、アンカーを付けても無駄なうえ、
// **本文の見出しが使いたいアンカー名を先に消費してしまいます**。
// 本文の class はサニタイザが必ず落とすので、`vocab-chrome` はサーバー／エンハンサが
// 挿したクロームにしか現れません——この判定は誤爆しません。
func inVocabChrome(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		for _, c := range strings.Fields(Attr(p, "class")) {
			if c == "vocab-chrome" {
				return true
			}
		}
	}
	return false
}

// anchorBase は要素のアンカー名の元を返します（作れなければ空）。
func anchorBase(el *html.Node) string {
	switch el.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		if s := anchorSlug(nodeText(el)); s != "" {
			return s
		}
	}
	// 見出しでない、または見出しの文字からは作れなかった場合はブロックIDを使う。
	return anchorSlug(Attr(el, htmldoc.BlockIDAttr))
}

// anchorSlug は表示文字をアンカー名へ整えます。
// 空白は `-` へ寄せ、リンクとして書くと壊れる文字を落とし、
// **殻が独占する接頭辞は剥がします**（本文から殻の名前空間を侵させない）。
func anchorSlug(s string) string {
	s = strings.Join(strings.Fields(s), "-") // 前後をtrimしつつ、連続する空白も1つの - へ
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(anchorDropChars, r) {
			return -1
		}
		return r
	}, s)
	return stripShellPrefix(s)
}

// stripShellPrefix は殻の接頭辞を無くなるまで剥がします（サニタイザと同じ規則）。
func stripShellPrefix(v string) string {
	for strings.HasPrefix(v, htmldoc.ShellIDPrefix) {
		v = v[len(htmldoc.ShellIDPrefix):]
	}
	return v
}

// uniqueAnchor は未使用のアンカー名を返します（重複は -2, -3 … と連番）。
func uniqueAnchor(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := base + "-" + strconv.Itoa(i)
		if !used[cand] {
			return cand
		}
	}
}
