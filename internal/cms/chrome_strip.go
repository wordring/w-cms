package cms

// 表示専用クローム（`.vocab-chrome`）を、本文へ入る手前で落とす。
//
// ⚠ **これは実際の事故から生まれた守りです**（2026-09-21）。使い捨てのスクリプトが
// `GET /api/load` の出力を `POST /api/save` へそのまま書き戻し、**移植した6枚の本文に
// 鏡が焼き込まれました**——`📄 …を開いています` がファイル表示の節の中に、検算の
// `<tfoot>` が材料表に、**2回ずつ**（2回 load→save したので）。
// ユーザーの報告:「不必要な文字列が2行書き込まれています」。
//
// **なぜ起きたか**: `/api/load` は**計算ビューの中身を埋めて返します**
// （[handler_view.go] ——編集モードの載せ替えでも表示が消えないように）。
// そこには「シリアライザが保存時に落とすので正本には混ざらない」と書いてありますが、
// ⚠ **そのシリアライザはブラウザの `app.js`** です。**外から叩くと誰も落としません。**
//
// ⚠ **エラーは一切出ませんでした。** 保存は 200 で、画面は鏡が2重に出るだけ。
// しかも保存されると `class` はサニタイズで落ちるので、**本文と見分けが付かなく
// なります**（直すときは構造から探すしかありませんでした）。
//
// ── なぜ「サニタイズの前」なのか ───────────────────────────────────────────
//
// ⚠ **順序がこの守りの全部です。** 印は `class="vocab-chrome"` で、**サニタイズは
// `class` を必ず落とします**——サニタイズの後に探しても、そこにはもう印がありません。
// だから `Sanitize`／`SanitizeReport` の**冒頭**で落とします。
//
// ⚠ **入口ごとに置きません。** 保存の口はいま4つ（`/api/save`・`/api/save-block`・
// 本文の書き換え・ページ作成）で、**5つ目を足した日に忘れます**——それはまさに
// 今回踏んだ形です。**本文が正本になる道は必ずサニタイズを通る**ので、そこに置きます。
//
// ⚠ **表示の経路でも通りますが、無害です**——描画は `Sanitize` の**あと**に
// `RenderComputedViews` が鏡を足すので、落とす対象がまだ存在しません。

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// StripChrome は本文HTMLから表示専用クローム（`.vocab-chrome`）を根こそぎ落とし、
// 落とした数を返します。
//
// ⚠ **`DropChrome` とは別ものです。** あちらは鏡を描き直す前に「要素の直下」を
// 掃除するもので、こちらは**本文全体から根こそぎ**落とします（入れ子の中にある
// 鏡も対象——ファイル表示の節の中がそうでした）。
//
// ⚠ **印が1つも無ければ、1バイトも触りません。** パースして描き直す往復は
// 本文をわずかに変えうるので、**何も落とすものが無いときは何もしない**のが
// いちばん安全です（サニタイズ本体がどのみち往復しますが、この関数のせいで
// 変わったのかが分からなくなるのを避けます）。
func StripChrome(s string) (out string, dropped int) {
	// 安価な事前判定。⚠ **`class` の値の一部としてしか現れない言葉**なので、
	// 文字列に無ければ木にも無いと言い切れます。
	if !strings.Contains(s, chromeClass) {
		return s, 0
	}
	nodes, err := htmldoc.ParseFragment(s)
	if err != nil {
		// ⚠ **読めないなら触りません。** サニタイズ本体が受け止めます
		//    ——ここで落とせなかったことより、本文を壊すほうが悪い。
		return s, 0
	}
	var top []*html.Node
	for _, n := range nodes {
		if n.Type == html.ElementNode && isChrome(n) {
			dropped++
			continue // 最上位の鏡は、木に入れずに捨てる
		}
		dropped += stripChromeIn(n)
		top = append(top, n)
	}
	if dropped == 0 {
		// ⚠ **`vocab-chrome` という語が別の場所にあっただけ**のとき
		//    （本文のテキストなど）。往復させずに元の文字列を返します。
		return s, 0
	}
	return htmldoc.Render(top), dropped
}

// stripChromeIn は要素の子孫から鏡を取り除き、落とした数を返します。
func stripChromeIn(n *html.Node) (dropped int) {
	var doomed []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && isChrome(c) {
			doomed = append(doomed, c)
			continue
		}
		dropped += stripChromeIn(c)
	}
	for _, d := range doomed {
		n.RemoveChild(d)
		dropped++
	}
	return dropped
}
