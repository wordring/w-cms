package cms

import (
	"strings"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン: ページ横断メタ（可変タグ）
//
//   <dl data-type="tags"><dt>発注元</dt><dd>株式会社トーア</dd></dl>
//
//   → page_tags（page_id, name, value）
//
// 特定のブロックに属さない「ページ全体のメタ情報」を担う。name は自由語。
// **タグは名前：値の1対**（2026-08-26 ユーザー決定「名前：値のタグは、値を複数持てません。
// 値を複数持てるのは、また別の形式だと思います」）。同じ name が要るとき（担当者が2人など）は
// **対の繰り返し**で表す——各タグは1値のままなので決定と矛盾しない。
// 1つの dt に複数 dd を並べる形はタグの形式から外れた。エディタは対単位でしか作らないが、
// 手書きで来ても拒否はせず、読む側は名前の繰り返しとして寛容に読む（エコーバックの流儀）。
//
// 移行第2段（語彙モデル §8.4-2）で同期元を旧 <m-tag> から <dl data-type="tags"> へ
// 切り替え、移行完了（2026-08-20）で旧要素の読み取り・変換ツールごと撤去した。
//
// かつてコアの parser.go / sync.go / database.CoreTables が直接扱っていたが、
// 「語彙の解釈はプラグインが持つ」方針に合わせてここへ移設した。
// コアが知るのはタイトル抽出だけになっている。
// ─────────────────────────────────────────────────────────────────────────

// legacyParentTagName は旧方式で親ページIDの指定に使われていたタグ名です。
// ページ属性はサイドカー <id>.meta.json が正本へ移行済みのため、dt にこの名前が
// 書かれてもユーザータグとしては取り込みません。
const legacyParentTagName = "親ページID"

func init() {
	Register(pageTagsPlugin{})
}

type pageTagsPlugin struct{}

func (pageTagsPlugin) Name() string { return "page_tags" }

func (pageTagsPlugin) Schema() []string {
	return []string{
		// name に主キーを張らないのは、同名タグを複数置けるようにするため。
		// 検索用には非一意インデックスだけを持つ。
		`CREATE TABLE IF NOT EXISTS page_tags (
			page_id INTEGER,
			name TEXT,
			value TEXT,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_page_tags_page_name ON page_tags(page_id, name);`,
	}
}

func (pageTagsPlugin) Tables() []string {
	return []string{"page_tags"}
}

// Triggers は可変タグの定義リストだけを担当することを宣言します。
func (pageTagsPlugin) Triggers() []string { return []string{"tags"} }

// OnPageStart は当該ページ分を洗い流します。
func (pageTagsPlugin) OnPageStart(ctx *ObserveContext) error {
	_, err := ctx.Tx.Exec(`DELETE FROM page_tags WHERE page_id = ?`, ctx.PageID)
	return err
}

func (pageTagsPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data != "dl" {
		return true, nil
	}
	insert := func(name, value string) error {
		// 「親ページID」はサイドカーへ移行済みのため、dt に書かれても取り込まない。
		if name == "" || name == legacyParentTagName {
			return nil
		}
		_, err := ctx.Tx.Exec(
			`INSERT INTO page_tags (page_id, name, value) VALUES (?, ?, ?)`,
			ctx.PageID, name, value)
		return err
	}
	// タグの中に入れ子の形式は来ない（dt/dd だけ）ので、子孫へは降りない。
	return false, syncTagsDL(el, insert)
}

// syncTagsDL は <dl data-type="tags"> の dt/dd を insert へ流し込みます。
// 鍵は dt の表示文字（trim後）。多値は dt の繰り返し／複数 dd で表す（語彙モデル §5.3）。
func syncTagsDL(dl *html.Node, insert func(name, value string) error) error {
	currentKey := ""
	var firstErr error
	walkSkippingNested(dl, map[string]bool{"dl": true, "table": true}, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		switch n.Data {
		case "dt":
			currentKey = strings.TrimSpace(nodeText(n))
		case "dd":
			// 鍵は直前の dt の表示文字（自由語）。
			firstErr = insert(currentKey, strings.TrimSpace(nodeText(n)))
		}
	})
	return firstErr
}
