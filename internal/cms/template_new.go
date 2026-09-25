package cms

// ─────────────────────────────────────────────────────────────────────────
// ページテンプレート：本文を写す（docs/【考察】ページテンプレート.md §4）
//
// ⚠ **2026-09-25 から純粋なコピーです**（ユーザー:「純粋なコピーにしましょう」）。
//
// それまでは写したあと**新規化**の段（配送係の「種まき」）を通し、空欄を列型の既定値で
// 埋めていました。作った目的は発注書番号の採番でしたが、それは 2026-09-21 に撤去され
// （「顧客の発注書番号はそのまま使います」）、残っていたのは**空の日付の列に今日を
// 入れる**ことだけでした——実データで効いていたのは、加工製品の改訂履歴の `受領日` だけです。
//
// やめた理由は2つです:
//
//   - **空欄は「まだ分からない」**（2026-09-21 の決定）——作った日を受領日と決めつけない。
//   - どの列が日付かを**表の種類の登録**から引いていました——コアから登録を無くす方向
//     （docs/【考察】DBの日本語化.md）と逆向きです。
//
// 写すときにすることは1つだけ——**ブロックID（`data-id`）を外す**。エディタは保存の
// たびにブロックIDを振るので、テンプレートのページも持っています。写したページが
// テンプレートと同じIDを持つと、ページどうしでブロックを運ぶとき（改訂の合流）に
// 衝突します。外しておけば、新しいページを保存したときに振り直されます。
// ⚠ 添付はそもそも写りません（本文だけを写す）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// CopyTemplateBody はテンプレートの本文を、新しいページの本文として写します。
//
// 値はそのまま写します（空欄は空欄のまま）。変えるのはブロックIDを外すことだけです。
// 呼ぶ側（loadTemplateBody）がサニタイズ済みの本文を渡します。
func CopyTemplateBody(bodyHTML string) string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}
	for _, n := range nodes {
		dropBlockIDs(n)
	}
	return htmldoc.Render(nodes)
}

// dropBlockIDs は n とその子孫から `data-id` を外します。
func dropBlockIDs(n *html.Node) {
	if n.Type == html.ElementNode {
		kept := n.Attr[:0]
		for _, a := range n.Attr {
			if a.Key != "data-id" {
				kept = append(kept, a)
			}
		}
		n.Attr = kept
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		dropBlockIDs(c)
	}
}
