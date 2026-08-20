package cms

// ─────────────────────────────────────────────────────────────────────────
// ページテンプレート：一覧の読み口（docs/【考察】ページテンプレート.md §7）
//
// テンプレート選択メニューの中身を返します。構造はテンプレートルート配下の
// **ツリーそのまま**で、枝（子を持つページ）は分類の見出し、葉はコピーできる
// テンプレートになります——分類が階層でタダで手に入る、というのが
// フォルダ方式を選んだ理由のひとつです（同書 §3.2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TemplateNode はテンプレートツリーの1ノードです。
// Children が空なら葉＝テンプレート（コピーできる）、そうでなければ分類です。
type TemplateNode struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Children []TemplateNode `json:"children,omitempty"`
}

// maxTemplateDepth はツリー走査の深さ上限です（万一の循環で無限再帰しないため）。
const maxTemplateDepth = 20

// TemplatesAPIHandler は GET /api/templates で、閲覧者が使えるテンプレートの
// ツリーを返します。テンプレートルートが無ければ空配列です（従来どおり
// 「空のページ」だけが作られる）。
func TemplatesAPIHandler(w http.ResponseWriter, r *http.Request) {
	out := make([]TemplateNode, 0)
	if rootID, ok := templateRootID(); ok {
		out = templateChildren(auth.CurrentUser(r), rootID, 0)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// templateRootID はテンプレートルートのIDを返します。
// 「トップページの子で、タイトルが TemplateRootTitle のページ」という約束（同書 §3.3）。
// 候補が複数あるときはIDの小さい方を採ります。
//
// ここはリクエスト時にしか呼ばれないのでDBを見て構いません
// （索引除外の判定＝IsTemplateArea は再構築中にも呼ばれるためサイドカーを使う。同書 §6.1）。
func templateRootID() (string, bool) {
	topID, err := strconv.Atoi(TopPageID)
	if err != nil {
		return "", false
	}
	var id int
	if err := database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id ASC LIMIT 1`,
		topID, TemplateRootTitle).Scan(&id); err != nil {
		return "", false
	}
	return page.NormalizeID(strconv.Itoa(id))
}

// templateChildren は親の配下を再帰的にツリー化します。
// 見えないページ（read 権限が無い）は visibleChildren の時点で落ちます。
func templateChildren(user *auth.User, parentID string, depth int) []TemplateNode {
	if depth >= maxTemplateDepth {
		return nil
	}
	n, err := strconv.Atoi(parentID)
	if err != nil {
		return nil
	}
	kids, err := visibleChildren(user, n)
	if err != nil {
		return nil
	}
	out := make([]TemplateNode, 0, len(kids))
	for _, k := range kids {
		out = append(out, TemplateNode{
			ID:       k.ID,
			Title:    k.Title,
			Children: templateChildren(user, k.ID, depth+1),
		})
	}
	return out
}
