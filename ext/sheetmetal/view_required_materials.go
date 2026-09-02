package sheetmetal

import (
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/internal/auth"
)

// requiredMaterialsViewHTML は部材手配・発注進捗の集計表を組み立てます
// （/api/required-materials と同じ集計。見出しは旧テンプレートを踏襲）。
//
// 子ページ一覧と同じく閲覧者を受け取る。部材の定義元ページを読めない相手には
// その行を出さないため（RequiredMaterials 側で絞る）。この経路は匿名にも描画される
// ので、渡し忘れると非公開ページ由来の部材名・仕入先が公開ページへ出る。
func requiredMaterialsViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">📊 部材手配・発注進捗状況</h3>`
	list, err := RequiredMaterials(user, pageIDInt)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		return head + `<p class="materials-empty">必要部材として登録されているアイテムはありません。</p>`
	}
	var sb strings.Builder
	sb.WriteString(head)
	sb.WriteString(`<table class="materials-table"><thead><tr>` +
		`<th>必要材料・部材名</th><th>仕入先・外注先</th>` +
		`<th class="num">必要総数</th><th class="num">発注済数</th>` +
		`<th class="num">残要手配数</th><th class="status">ステータス</th>` +
		`</tr></thead><tbody>`)
	for _, m := range list {
		supplier := m.SupplierName
		if supplier == "" {
			supplier = "-"
		}
		status := `<span class="badge ok">手配完了</span>`
		if m.Remaining > 0 {
			status = `<span class="badge ng">要手配 (` + strconv.Itoa(m.Remaining) + `)</span>`
		}
		sb.WriteString(`<tr><td>` + stdhtml.EscapeString(m.MaterialName) + `</td>` +
			`<td>` + stdhtml.EscapeString(supplier) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.TotalRequired) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.Ordered) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.Remaining) + `</td>` +
			`<td class="status">` + status + `</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
