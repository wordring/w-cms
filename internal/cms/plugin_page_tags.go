package cms

import (
	"database/sql"
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
// 特定のブロックに属さない「ページ全体のメタ情報」を担う。name は自由語で、
// **同じ name を同一ページに複数置いてよい**（担当者が2人、関連部品番号が複数など。
// dt の繰り返し／複数 dd で表す）。
//
// 移行第2段（語彙モデル §8.4-2）で同期元を <dl data-type="tags"> へ切り替え、
// 旧 <m-tag> の読み取り（短期の保険）は実データの一括変換完了後に除去した。
// 旧要素の表示・変換は移行完了（第4段）まで残る（語彙宣言 Tags() と変換ツール）。
//
// かつてコアの parser.go / sync.go / database.CoreTables が直接扱っていたが、
// 「カスタムタグはすべてプラグインが所有する」方針に合わせてここへ移設した。
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

func (pageTagsPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-tag", Attributes: []string{"name", "value"}},
	}
}

func (pageTagsPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM page_tags WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	insert := func(name, value string) error {
		// 「親ページID」はサイドカーへ移行済みのため、dt に書かれても取り込まない。
		if name == "" || name == legacyParentTagName {
			return nil
		}
		_, err := tx.Exec(
			`INSERT INTO page_tags (page_id, name, value) VALUES (?, ?, ?)`,
			pageID, name, value)
		return err
	}

	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		if n.Data == "dl" && Attr(n, "data-type") == "tags" {
			firstErr = syncTagsDL(n, insert)
		}
	})
	return firstErr
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
			key := Attr(n, "data-field") // ③計算形式の機械キーがあれば優先（②と同じ規則）
			if key == "" {
				key = currentKey
			}
			firstErr = insert(key, strings.TrimSpace(nodeText(n)))
		}
	})
	return firstErr
}
