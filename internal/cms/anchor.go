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
			if Attr(el, "id") != "" {
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
