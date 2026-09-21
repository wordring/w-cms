package subcon

// ─────────────────────────────────────────────────────────────────────────
// 未手配の一覧（2026-09-21）——ここから発注書を1枚作ります
//
// ⚠ **受注を横断します。** ユーザー:「弊社の発注書は、**受注明細の単位とは無関係に、
// 納期のグループなどから発行**されます」——**いま買わなければならないもの全部**を
// 1つの表に出し、**人が相手を決めて**まとめます。
//
// ⚠ **機械は候補まで。仕入先を決めるのは人**です（相見積もりを見て決めるので、
// 機械には決められません）。参考単価とその仕入先は**添えるだけ**。
//
// ⚠ **選ばれていない行は何もしません。** 既定は**未選択**で、押した行だけが
// 発注書に入ります（整理の「二つ目の図面」と同じ流儀——既定で動かさない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// UnorderedViewType はビューの形式名です。
const UnorderedViewType = "unordered-items"

// unorderedViewHTML は未手配の一覧と「発注書を作る」欄を描きます。
func unorderedViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">🧾 未手配の一覧（受注横断）</h3>`
	list, err := UnorderedItems(user)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「手配し終えた」と「そもそも受注明細に弊社品番が
		//    無くて引けていない」は別物です。
		return head + `<p class="materials-empty">未手配の購入品はありません` +
			`（⚠ 受注明細に<strong>弊社品番</strong>が無い行は、ここに出ません）。</p>`
	}

	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`<p class="unorder-help">行を選び、仕入先を入れて「発注書を作る」を` +
		`押すと、<strong>発注／年／月</strong> に1枚できます。` +
		`⚠ 発注書は<strong>1枚に1社</strong>です。</p>`)
	b.WriteString(`<table class="materials-table unorder-table"><thead><tr>` +
		`<th class="unorder-pick">選</th><th>納期</th><th>客先</th><th>弊社品番</th>` +
		`<th>購入品</th><th class="num">残</th><th>参考単価</th>` +
		`</tr></thead><tbody>`)
	for i, u := range list {
		b.WriteString(`<tr class="unorder-row"` + unorderedRowAttrs(u) + `>`)
		b.WriteString(`<td class="unorder-pick"><input type="checkbox" class="unorder-check"` +
			` data-unorder-row="` + strconv.Itoa(i) + `"/></td>`)
		b.WriteString(`<td>` + stdhtml.EscapeString(orDash(u.Due)) + `</td>`)
		b.WriteString(`<td>` + stdhtml.EscapeString(orDash(u.Client)) + `</td>`)
		b.WriteString(`<td><a href="/` + page.FormatID(u.ProductPageID) + `">` +
			page.FormatID(u.ProductPageID) + `</a>` + unorderedMigratingMark(u) + `</td>`)
		b.WriteString(`<td>` + stdhtml.EscapeString(u.Name) + `</td>`)
		b.WriteString(`<td class="num">` + strconv.Itoa(u.Remaining) + `</td>`)
		b.WriteString(`<td class="unorder-cost">` + unorderedCostHTML(u) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(unorderedFormHTML())
	return b.String()
}

// orDash は空欄を「—」にします（空白と見分けが付くように）。
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// unorderedRowAttrs は行に「発注書へ入れる値」を持たせます。
//
// ⚠ **画面が本文から値を読み直しません**——表示は丸めることがあるので、
// **送る値は属性で持ちます**（受注残表の編集と同じ作り）。
func unorderedRowAttrs(u UnorderedItem) string {
	at := func(k, v string) string {
		return ` data-` + k + `="` + stdhtml.EscapeString(v) + `"`
	}
	return at("product", page.FormatID(u.ProductPageID)) +
		at("material", u.Material) + at("shape", u.Shape) + at("size", u.Size) +
		at("itemname", u.Name) + at("qty", strconv.Itoa(u.Remaining)) +
		at("cost", unorderedCostValue(u))
}

// unorderedMigratingMark は移行の確認前の印です。
func unorderedMigratingMark(u UnorderedItem) string {
	if !u.Migrating {
		return ""
	}
	// ⚠ **必要数そのものが当てになりません**（材料表がワンノートの形のままなら）。
	return `<span class="proc-migrating"> ⚠ 移行の確認前</span>`
}

// unorderedCostHTML は参考単価を出します（引けなければ黙らずにそう言う）。
func unorderedCostHTML(u UnorderedItem) string {
	if u.Cost <= 0 {
		return `<span class="matsearch-none">⚠ 記録なし</span>`
	}
	s := comma(u.Cost) + "円"
	if u.Supplier != "" {
		s += `　<span class="matsearch-src">` + stdhtml.EscapeString(u.Supplier) + `</span>`
	}
	return s
}

// unorderedFormHTML は仕入先などを入れる欄です。
//
// ⚠ **候補を出します**（`推奨業者` と同じ出どころ）——打つより選ぶほうが速く、
// 速ければ表記が揃います。
func unorderedFormHTML() string {
	f := func(id, label, ph, typ string) string {
		return searchFieldHTML("unorder", id, label, ph, typ)
	}
	return `<div class="matsearch-form unorder-form">` +
		f("supplier", "仕入先", "みなと商店", "text") +
		f("order_at", "発注日", "", "date") +
		f("due", "納期", "", "date") +
		f("note", "備考", "定尺で結構です", "text") +
		`<button type="button" class="matsearch-go" data-unorder-go="1">発注書を作る</button>` +
		`</div><div class="unorder-result" data-unorder-result="1"></div>`
}

// unorderedCostValue は発注書へ送る単価です。
//
// ⚠ **参考単価が無いときは空を送ります。** `0` と書くと、紙の上では
// **「0円で発注した」**という意味になります——「まだ分からない」と「ただ」は
// 別のことで、⚠ **仕入先は紙に書いてあるとおりに読みます**。
func unorderedCostValue(u UnorderedItem) string {
	if u.Cost <= 0 {
		return ""
	}
	return strconv.Itoa(u.Cost)
}
