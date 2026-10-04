package toho

// ─────────────────────────────────────────────────────────────────────────
// 古い図面に赤枠を出す——表示のときだけ（2026-09-03）
//
// ユーザー:「既存ページの図面の項目の先頭に配置してはどうでしょう？既存の図面は
// 古いとわかるように**赤枠で囲み**、ユーザーの判断で消します。（古い図面は旧版に
// 残っています）」
//
// **状態は持ちません。** 改定図面は先頭へ差し込まれるので（filing.go の
// mergeAsRevision）、**先頭以外が古い**——並びそのものが最新を表します。
// 「どれが古いか」を本文へ書き込むと、人が並べ替えたときに嘘になります。
//
// 印は**保存されません**。`class` はサニタイズで落ちるので（`ref-missing` の
// 薄赤と同じ作り）、閲覧のたびにここで付け直します。人が古い図面ブロックを
// 消せば、その時点で赤枠も消えます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

// textOf は要素の中の文字を連結します。
func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	out := ""
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out += textOf(c)
	}
	return out
}

// addClass は class 属性へ1つ足します（既存があれば空白で連ねる）。
func addClass(el *html.Node, name string) {
	for i, a := range el.Attr {
		if a.Key == "class" {
			if a.Val == "" {
				el.Attr[i].Val = name
			} else {
				el.Attr[i].Val = a.Val + " " + name
			}
			return
		}
	}
	el.Attr = append(el.Attr, html.Attribute{Key: "class", Val: name})
}

func init() {
	// 廃版の構成部品を薄く見せる（表示のときだけ）。**行は消しません**
	// ——外注加工に出した紙に社内コードが載っているので、消すと指し先が消えます
	// （ユーザー:「構成部品は図面の改定に伴って廃版になる場合があります」）。
	//
	// ⚠ **引き金ごとに鏡は1人**です（2人目を登録すると起動時に panic します）。
	// 材料の表には2つ要る（廃版の印＋参考単価・2026-09-21）ので、**ここで束ねます**。
	for _, t := range []string{partMaterialsType, "part-outsourcing", "part-purchased", "part-supplied"} {
		t := t
		cms.RegisterMirror(t, cms.MirrorHandlerFunc(
			func(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
				for _, tbl := range mirrorTablesOf(el) {
					if _, err := markObsoleteRows(ctx, tbl); err != nil {
						return true, err
					}
					// 支給部品の表（部品の表の最後）の下に「貰った見積」（2026-10-04・rfq_quotes.go）。
					if t == "part-supplied" {
						appendQuotesList(ctx, tbl)
					}
					if t != partMaterialsType {
						continue
					}
					if _, err := renderMaterialPrices(ctx, tbl); err != nil {
						return true, err
					}
				}
				return true, nil
			}))
	}
}

// mirrorTablesOf は、鏡を掛ける表を返します——el が表ならその表、**見出しの節**
// （`<section><h2>材料</h2>`）なら、中の**素の表**（自分で形式を名乗らない表）だけ。
//
// ⚠ **自分で名乗る表は配送係が別に届けます**（2026-09-27）。加工製品テンプレートは
// `<section><h2>材料</h2><table><caption>材料</caption>` の形（見出しを残してキャプションを
// 付けた）で、節の中の表を全部拾っていたころは**節から1回・表から1回**鏡が走り、
// **最新単価の列が2つ**出ていました（利用者:「材料の表に最新単価の列が二つあるのは何故ですか？」）。
// 索引は 2026-09-21 に同じ二重を直していました（`eachPlainVocabTable`）——判定は同じ
// `cms.VocabTypeOf` に揃えます。⚠ 表ごとに掛けるので、節に素の表が2つあれば、それぞれに1列。
func mirrorTablesOf(el *html.Node) []*html.Node {
	if el.Data == "table" {
		return []*html.Node{el}
	}
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data == "section" {
				continue // 入れ子の節は、それ自身の業務ブロック
			}
			if c.Data == "table" {
				if cms.VocabTypeOf(c) == "" {
					out = append(out, c)
				}
				continue
			}
			walk(c)
		}
	}
	walk(el)
	return out
}

// markObsoleteRows は 状態＝廃版 の行へ印を付けます（見た目は CSS が担う）。
func markObsoleteRows(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	col := statusColumnIndex(el)
	if col < 0 {
		return true, nil
	}
	for _, tr := range rowsOf(el) {
		if cellText(tr, col) == obsoleteMark {
			addClass(tr, "row-obsolete")
		}
	}
	return true, nil
}

// statusColumnIndex は見出し行から「区分」列の位置を返します（無ければ -1）。
//
// **「状態」ではなく「区分」**——受注明細の「状態」（未着手／加工中／納品済）は
// **受注ごとの進捗**で、こちらは**定義そのものの現行／廃版**です。同じ語にすると
// 混同されます（2026-09-03 ユーザーが実際に取り違えた）。進捗は加工製品ページではなく
// 受注ページで見るもの、という線引きがここに現れています。
// **見出しの表示文字が鍵**——機械キーを本文へ書く属性はありません。
func statusColumnIndex(table *html.Node) int {
	rows := rowsOf(table)
	if len(rows) == 0 {
		return -1
	}
	// 最初の行が見出し行（語彙モデル §5.1）。そこに無ければ諦める。
	for i, c := range cellsOf(rows[0]) {
		if c.Data == "th" && strings.TrimSpace(textOf(c)) == "区分" {
			return i
		}
	}
	return -1
}

// rowsOf は表の行を（tbody を挟んでいても）集めます。
func rowsOf(table *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "tr" {
				out = append(out, c)
				continue
			}
			walk(c)
		}
	}
	walk(table)
	return out
}

// cellsOf は行のセル（`th`・`td`）を並び順に集めます。
//
// 表を読む鏡（廃版の印・検算）はすべてここを通ります——「セルとは何か」を
// 1か所に置くためです。
func cellsOf(tr *html.Node) []*html.Node {
	var out []*html.Node
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
			out = append(out, c)
		}
	}
	return out
}

// cellText は行の i 番目のセルの文字を返します。
func cellText(tr *html.Node, i int) string {
	cells := cellsOf(tr)
	if i < 0 || i >= len(cells) {
		return ""
	}
	return strings.TrimSpace(textOf(cells[i]))
}
