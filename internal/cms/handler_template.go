package cms

// ─────────────────────────────────────────────────────────────────────────
// ページテンプレート：一覧の読み口（docs/考察/【考察】ページテンプレート.md §7）
//
// テンプレート選択メニューの中身を返します。構造はテンプレートルート配下の
// **ツリーそのまま**で、枝（子を持つページ）は分類の見出し、葉はコピーできる
// テンプレートになります——分類が階層でタダで手に入る、というのが
// フォルダ方式を選んだ理由のひとつです（同書 §3.2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"os"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
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
	return FindChildByTitle(TopPageID, TemplateRootTitle) // 同じ SQL の写しだった（2026-10-29 に寄せた）
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
		children := templateChildren(user, k.ID, depth+1)
		if isLeafPage(k.ID) {
			// 作るときに決める語の値が、人の選べない値（ボタンが作る書類など）のテンプレートは出さない（2026-10-27・fixed_tag.go）。
			if body, err := os.ReadFile(page.BodyPath(k.ID)); err == nil && !templateCreatable(Sanitize(string(body))) {
				continue
			}
		} else if len(children) == 0 {
			// 中のテンプレートが全部出せない分類は、分類ごと出さない（子の無い分類は、メニューで葉に見えてしまう）。
			continue
		}
		out = append(out, TemplateNode{
			ID:       k.ID,
			Title:    k.Title,
			Children: children,
		})
	}
	return out
}
