package cms

// ─────────────────────────────────────────────────────────────────────────
// ページテンプレート：テンプレート配下かどうかの判定（docs/【考察】ページテンプレート.md §3・§6）
//
// テンプレートは**普通のWikiページ**で、置き場所だけで見分けます:
//
//	000000 トップ
//	└── テンプレート        ← ルート（分類・テンプレートではない）
//	    ├── 業務            ← 枝＝分類
//	    │   └── 受注ページ   ← 葉＝テンプレート
//	    └── 記録            ← 枝＝分類
//
// ルートは「トップページの子で、タイトルが『テンプレート』のページ」という約束で
// 見分けます（同書 §3.3。スキーマもUIも増やさず、名前を持つのが1ページだけなので
// コピー先のタイトルへ漏れない）。将来サイドカー属性へ昇格するなら、
// 差し替えるのは isTemplateRoot の中身だけで済みます。
//
// **葉かどうかはここでは問いません。** ②索引・③計算からの除外は subtree 一律で行い
// （分類ページに業務データは入らない）、葉の判定はテンプレート選択メニューを出すとき
// ——すなわちDBが揃っているリクエスト時——にしか要りません（同書 §6.2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TemplateRootTitle はテンプレートルートを見分けるタイトルです（§3.3 の約束）。
const TemplateRootTitle = "テンプレート"

// maxAncestorWalk は親チェーン辿りの安全上限です（万一の循環で無限ループしないため。
// page.EffectivePublic と同じ流儀）。
const maxAncestorWalk = 10000

// IsTemplateArea は、ページが**テンプレート領域**（ルート自身とその配下すべて）に
// 属するかを返します。②索引・③計算からの除外はこの単位で行います（同書 §6）——
// ルートも分類ページも業務データを持たないので、領域まるごと外すのが単純で、
// 「説明のつもりで書いた例の発注書が集計に出る」事故も防げます。
//
// 「そのページがテンプレートとして選べるか」はこれとは別で、
// IsUnderTemplateRoot（ルート自身を含まない）に葉の条件を加えたものです。
func IsTemplateArea(id string) bool {
	norm, ok := page.NormalizeID(id)
	if !ok {
		return false
	}
	return isTemplateRoot(norm) || IsUnderTemplateRoot(norm)
}

// IsUnderTemplateRoot は、ページがテンプレートルートの**配下**にあるかを返します。
// ルート自身は false です（ルートは分類であってテンプレートではない）。
//
// **親チェーンはサイドカーから辿ります。DBを見てはいけません。**
// RebuildDatabase は空のDBへページを1つずつ同期するため、先祖がまだ pages に
// 入っていない順序で子が同期されると「テンプレートではない」と誤判定し、
// **静かに索引が汚れます**（同書 §6.1）。親の正本はサイドカーで、ディスクには
// 常に揃っているので順序に依存しません。
func IsUnderTemplateRoot(id string) bool {
	cur, ok := page.NormalizeID(id)
	if !ok {
		return false
	}
	seen := map[string]bool{cur: true}
	for i := 0; i < maxAncestorWalk; i++ {
		meta, ok := page.ReadSidecar(cur)
		if !ok {
			return false // サイドカーが無い＝鎖を辿れない。テンプレート扱いしない
		}
		parent, ok := page.NormalizeID(meta.ParentID)
		if !ok {
			return false // 親なし（トップレベル）まで来た
		}
		if seen[parent] {
			return false // 万一の循環
		}
		seen[parent] = true
		if isTemplateRoot(parent) {
			return true
		}
		cur = parent
	}
	return false
}

// isTemplateRoot は、そのページがテンプレートルートかを返します。
// 判定は「トップページの直下」かつ「タイトルが TemplateRootTitle」です。
//
// タイトルはサイドカーに無い（PageMeta は owner/group/mode/parent/日時のみ）ため、
// 本文HTMLをディスクから読んで ParseCore で取ります。**先に親を見て弾く**ので、
// 1回の親チェーン辿りでファイルを読むのは高々1ページ分です
// （トップの直下という条件を満たす先祖は鎖の中にただ1つしか無い）。
func isTemplateRoot(id string) bool {
	if id == TopPageID {
		return false // トップページ自身はルートではない
	}
	meta, ok := page.ReadSidecar(id)
	if !ok {
		return false
	}
	parent, ok := page.NormalizeID(meta.ParentID)
	if !ok || parent != TopPageID {
		return false
	}
	return pageTitleFromDisk(id) == TemplateRootTitle
}

// IsTemplatePage は、そのページが**テンプレートとして選べる**かを返します。
// 条件は「テンプレートルートの配下」かつ「葉（子を持たない）」です（同書 §3.2）。
// 子を持つページは分類のためだけに存在し、コピーの対象になりません。
//
// 葉の判定だけはDBを見ます——**呼ばれるのはリクエスト時（メニュー表示・コピー実行）だけ**で、
// そのときDBは揃っているため、§6.1 の再構築順序の罠は及びません。
func IsTemplatePage(id string) bool {
	norm, ok := page.NormalizeID(id)
	if !ok || !IsUnderTemplateRoot(norm) {
		return false
	}
	return isLeafPage(norm)
}

// isLeafPage は子を持たないページかを返します。
func isLeafPage(id string) bool {
	n, err := strconv.Atoi(id)
	if err != nil {
		return false
	}
	var children int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM pages WHERE parent_id = ?`, n).Scan(&children); err != nil {
		return false
	}
	return children == 0
}

// loadTemplateBody は template= で指定されたページの本文を検証して読みます。
// 指定が無ければ ("", true) を返します（テンプレートを使わない通常の新規作成）。
// 失敗時は応答を書いて ok=false を返します。
//
// 本文は**サニタイザを通してから**返します。テンプレートページは保存経路を通って
// いるとは限らない（手動配置・バックアップ復元）ため、新しいページへ写す前に濾します。
func loadTemplateBody(w http.ResponseWriter, r *http.Request, tmplID string) (string, bool) {
	if strings.TrimSpace(tmplID) == "" {
		return "", true
	}
	norm, okID := page.NormalizeID(tmplID)
	if !okID {
		http.Error(w, "テンプレートのページIDが不正です", http.StatusBadRequest)
		return "", false
	}
	// テンプレートの中身を読むので read 権限が要る（RequirePageRead が応答を書く）。
	if !page.RequirePageRead(w, r, norm) {
		return "", false
	}
	if !IsUnderTemplateRoot(norm) {
		http.Error(w, "指定されたページはテンプレートではありません（「"+
			TemplateRootTitle+"」フォルダの配下にありません）", http.StatusBadRequest)
		return "", false
	}
	if !isLeafPage(norm) {
		http.Error(w, "指定されたページは分類フォルダです（子ページを持つページは"+
			"テンプレートとして使えません）", http.StatusBadRequest)
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(page.GetPageDir(norm), norm+".html"))
	if err != nil {
		http.Error(w, "テンプレートの本文を読めませんでした", http.StatusInternalServerError)
		return "", false
	}
	return Sanitize(string(data)), true
}

// pageTitleFromDisk はページ本文をディスクから読んでタイトル（最初の h1）を返します。
// DBの pages.title を使わないのは IsUnderTemplateRoot と同じ理由です（再構築の順序非依存）。
func pageTitleFromDisk(id string) string {
	data, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		return ""
	}
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(ParseCore(root).Title)
}
