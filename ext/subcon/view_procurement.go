package subcon

// ─────────────────────────────────────────────────────────────────────────
// 手配状況リストの描き方（2026-09-21 に作り直し）
//
// ⚠ **形が変わりました。** それまでは**材料名で受注ページ全体を合算**していたので、
// **どの加工製品のぶんか**が消えていました。ユーザーの要件:
//
//	「各受注ページに**各加工製品ごとの項目と購入品の表を集める**必要があり、
//	 その表の列の一つとして、**発注書番号と発注書ページへのリンク**が必要」
//
// ⚠ **`発注書番号` はページ番号です**（`弊社品番` と同じ形）——別に採番すると、
// 同じものに2つの名前ができます。
//
// ⚠ **古い集計（`RequiredMaterials`）は残してあります**——`/api/required-materials`
// を叩く相手（E2E）が居るためです。表示だけを作り直しました。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// procurementViewHTML は加工製品ごとの手配状況を描きます。
//
// ⚠ **閲覧者を受け取るのは飾りではありません**（旧 `view_required_materials.go` から
// 引き継ぐ注意）。**この経路は匿名にも描画される**ので、渡し忘れると
// **非公開ページ由来の部材名・仕入先が公開ページへ出ます**。絞りは
// `ProcurementByProduct` の中（`page.CanView`）。
func procurementViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">📊 手配状況（加工製品ごと）</h3>`
	list, err := ProcurementByProduct(user, pageIDInt)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		return head + `<p class="materials-empty">受注明細がありません。</p>`
	}
	var sb strings.Builder
	sb.WriteString(head)
	for _, p := range list {
		sb.WriteString(`<div class="proc-product">`)
		sb.WriteString(procProductHead(p))
		if p.Why != "" {
			// ⚠ **引けない理由を黙りません**——空欄だと「要る物が無い」に見えます。
			cls := "proc-why"
			if strings.HasPrefix(p.Why, "⚠") {
				cls = "proc-why proc-why-ng"
			}
			sb.WriteString(`<p class="` + cls + `">` + stdhtml.EscapeString(p.Why) + `</p>`)
		}
		if len(p.Items) > 0 {
			sb.WriteString(procItemsTable(p))
		}
		sb.WriteString(`</div>`)
	}
	return sb.String()
}

// procProductHead は加工製品の見出し（題・受注数・移行の印）を描きます。
func procProductHead(p ProcurementProduct) string {
	name := p.ItemName
	if name == "" {
		name = p.Title
	}
	if name == "" {
		name = "（品名なし）"
	}
	var sb strings.Builder
	sb.WriteString(`<h4 class="proc-name">`)
	if p.PageID > 0 {
		sb.WriteString(`<a href="/` + page.FormatID(p.PageID) + `">` +
			stdhtml.EscapeString(name) + `</a>`)
	} else {
		sb.WriteString(stdhtml.EscapeString(name))
	}
	if p.Qty > 0 {
		sb.WriteString(`<span class="proc-qty"> × ` + strconv.Itoa(p.Qty) + `</span>`)
	}
	if p.Migrating {
		// ⚠ **移行の確認前だと分かるようにします**——材料表がワンノートの形のままなら、
		//    必要数そのものが当てになりません。
		sb.WriteString(`<span class="proc-migrating"> ⚠ 移行の確認前</span>`)
	}
	sb.WriteString(`</h4>`)
	return sb.String()
}

// procItemsTable は購入品の表を描きます。
func procItemsTable(p ProcurementProduct) string {
	var sb strings.Builder
	sb.WriteString(`<table class="materials-table proc-table"><thead><tr>` +
		`<th>購入品</th><th>種別</th><th class="num">一台</th><th class="num">必要</th>` +
		`<th class="num">発注済</th><th class="num">残</th><th>発注書</th>` +
		`</tr></thead><tbody>`)
	for _, it := range p.Items {
		sb.WriteString(`<tr><td>` + stdhtml.EscapeString(it.Name) + `</td>` +
			`<td>` + stdhtml.EscapeString(it.Kind) + `</td>` +
			`<td class="num">` + strconv.Itoa(it.Per) + `</td>` +
			`<td class="num">` + strconv.Itoa(it.Required) + `</td>` +
			`<td class="num">` + strconv.Itoa(it.Ordered) + `</td>` +
			`<td class="num">` + procRemaining(it) + `</td>` +
			`<td class="proc-orders">` + procOrderLinks(it) + `</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}

// procRemaining は残りを描きます（0 なら手配完了の印）。
func procRemaining(it ProcurementItem) string {
	if it.Remaining == 0 && it.Required > 0 {
		// ⚠ **`✓` を付けるのは色だけに頼らないため**（検算の合格と同じ判断）。
		return `<span class="badge ok">✓ 0</span>`
	}
	return `<span class="badge ng">` + strconv.Itoa(it.Remaining) + `</span>`
}

// procOrderLinks は手配した発注書への link を並べます。
//
// ⚠ **`発注書番号` はページ番号**です（`弊社品番` と同じ形）。
// ⚠ **0件を黙りません**——「まだ発注していない」と「発注書に弊社品番が書かれていない」
// は別物なので、後者に気づけるよう**理由の形**で出します。
func procOrderLinks(it ProcurementItem) string {
	if len(it.Orders) == 0 {
		return `<span class="proc-none">⚠ 未手配（発注書に弊社品番がありますか）</span>`
	}
	var parts []string
	for _, o := range it.Orders {
		label := page.FormatID(o.PageID)
		title := o.Title
		if title != "" {
			label += "　" + title
		}
		parts = append(parts, `<a href="/`+page.FormatID(o.PageID)+`" title="`+
			stdhtml.EscapeString(title)+`">`+stdhtml.EscapeString(label)+`</a>`+
			`<span class="proc-oqty">（`+strconv.Itoa(o.Qty)+`）</span>`)
	}
	return strings.Join(parts, `<br/>`)
}
