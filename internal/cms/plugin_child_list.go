package cms

import (
	"database/sql"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン: 子ページ一覧（表示専用）
//
//   <m-child-list>
//
// 固有のテーブルを持たず、フロントが描画時に /api/children を呼ぶだけの表示要素。
// 同期するデータが無いので Schema/Tables/Sync は空だが、
// 「カスタムタグはすべてプラグインが所有する」方針に従い、語彙の所有者としてここに置く。
// これによりサニタイザ（コア）は m-* を一切知らずに済む。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(childListPlugin{})
}

type childListPlugin struct{}

func (childListPlugin) Name() string { return "child_list" }

func (childListPlugin) Schema() []string { return nil }

func (childListPlugin) Tables() []string { return nil }

func (childListPlugin) Tags() []TagSpec {
	return []TagSpec{
		{Element: "m-child-list"},
	}
}

func (childListPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error { return nil }
