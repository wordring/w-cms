package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン: ページ横断メタ（可変タグ）
//
//   <m-tag name="発注元" value="株式会社トーア">
//
//   → page_tags（page_id, name, value）
//
// 特定のブロックに属さない「ページ全体のメタ情報」を担う。name は自由語で、
// **同じ name を同一ページに複数置いてよい**（担当者が2人、関連部品番号が複数など）。
//
// かつてコアの parser.go / sync.go / database.CoreTables が直接扱っていたが、
// 「カスタムタグはすべてプラグインが所有する」方針に合わせてここへ移設した。
// コアが知るのはタイトル抽出だけになっている。
// ─────────────────────────────────────────────────────────────────────────

// legacyParentTagName は旧方式の親ページID指定タグの名前です。
// ページ属性はサイドカー <id>.meta.json が正本へ移行済みのため、本文由来のタグとしては
// 取り込みません（m-tag の意味を知るのはこのプラグインだけ）。
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

func (pageTagsPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-tag", Attributes: []string{"name", "value"}},
	}
}

func (pageTagsPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM page_tags WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || n.Data != "m-tag" {
			return
		}
		name := Attr(n, "name")
		// 旧方式の「親ページID」タグはサイドカーへ移行済みのため、ユーザータグとしては扱わない。
		if name == "" || name == legacyParentTagName {
			return
		}
		if _, err := tx.Exec(
			`INSERT INTO page_tags (page_id, name, value) VALUES (?, ?, ?)`,
			pageID, name, Attr(n, "value")); err != nil {
			firstErr = err
		}
	})
	return firstErr
}
