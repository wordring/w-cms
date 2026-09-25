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
	head := `<h3 class="materials-title">🧾 必要部材表（受注横断）</h3>`
	list, err := UnorderedItems(user)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	// ⚠ **加工製品ページに結べない受注明細を数えて言います**（2026-09-25・P1）。
	//    それまでは0件のときに「弊社品番が無い行は、ここに出ません」と一般論を言う
	//    だけで、**受注明細が7行あるのに空の表**を「壊れた」と読まれました。
	//    ⚠ **表が空でないときも出します**——結べない行の材料は、どのみちここに出ません。
	head += unlinkedOrdersHTML(UnlinkedOrders(user))
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「手配し終えた」と「そもそも加工製品に結べて
		//    いない」は別物です（後者は上の警告が数えて言う）。
		//
		// ⚠ **それでもフォームは出します**（2026-09-22 に実データで踏んだ）。
		//    ユーザー:「**必要なければ何もクリックしなくても**表作成ボタンをクリック
		//    すると発注部材表が開き」——**加工製品ページに無い部材（消耗品・治具）だけを
		//    買う道**がここです。⚠ **0件のときこそ、その道が要ります**——
		//    早期に戻ると、**空の表から始める手段が画面から消えます**。
		return head + `<p class="materials-empty">必要部材はありません。` +
			`加工製品ページに無い部材は、上の<strong>臨時部材表</strong>に書くと、ここに並びます。` +
			`</p>` +
			unorderedFormHTML(user, pageIDInt)
	}

	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`<p class="unorder-help">行を選んで「発注部材表へ入れる」を押すと、` +
		`下に<strong>発注部材表</strong>ができます` +
		`——そこで<strong>足し引き</strong>してから発注書にします` +
		`（⚠ <strong>何も選ばずに押してもかまいません</strong>。` +
		`加工製品ページに無い部材は、上の臨時部材表に書くとここに並びます）。` +
		`並びは<strong>臨時部材が先頭</strong>、あとは<strong>納期順</strong>、同じ納期の中は<strong>装置順</strong>。` +
		`⚠ 発注書は<strong>1枚に1社</strong>なので、` +
		`<strong>同じ業者のものだけ</strong>を選んでください。</p>`)
	b.WriteString(`<table class="materials-table unorder-table"><thead><tr>` +
		`<th class="unorder-pick">選</th><th>納期</th><th>客先</th><th>装置</th><th>弊社品番</th>` +
		`<th>購入品</th><th class="num">残</th><th>参考単価</th>` +
		`</tr></thead><tbody>`)
	for i, u := range list {
		b.WriteString(`<tr class="unorder-row"` + unorderedRowAttrs(u) + `>`)
		b.WriteString(`<td class="unorder-pick"><input type="checkbox" class="unorder-check"` +
			` data-unorder-row="` + strconv.Itoa(i) + `"/></td>`)
		b.WriteString(`<td>` + stdhtml.EscapeString(orDash(u.Due)) + `</td>`)
		b.WriteString(`<td>` + stdhtml.EscapeString(orDash(u.Client)) + `</td>`)
		// ⚠ **並べ替えの根拠は見えていること。** 装置順に並ぶのに装置が見えないと、
		//    「なぜこの順なのか」が分からず、**並びが壊れても気づけません**。
		b.WriteString(`<td>` + stdhtml.EscapeString(orDash(u.Machine)) + `</td>`)
		if u.TempRow > 0 {
			// ⚠ 臨時部材は加工製品ページを持ちません（`/000000` へのリンクを出さない）。
			b.WriteString(`<td>—</td>`)
		} else {
			b.WriteString(`<td><a href="/` + page.FormatID(u.ProductPageID) + `">` +
				page.FormatID(u.ProductPageID) + `</a>` + unorderedMigratingMark(u) + `</td>`)
		}
		b.WriteString(`<td>` + stdhtml.EscapeString(u.Name) + `</td>`)
		b.WriteString(`<td class="num">` + strconv.Itoa(u.Remaining) + `</td>`)
		b.WriteString(`<td class="unorder-cost">` + unorderedCostHTML(u) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(unorderedFormHTML(user, pageIDInt))
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
	if u.TempRow > 0 {
		// ⚠ **臨時部材の行**（2026-09-25）——弊社品番は持たず、品番・表面・単位・備考と
		//    「臨時部材表の何行目か」を運びます。発注部材表へ入れたら、サーバーが臨時部材表
		//    からその行を消します。⚠ **単価は人が書いた値を優先**（無ければ参考単価）。
		cost := strings.TrimSpace(u.CostRaw)
		if cost == "" {
			cost = unorderedCostValue(u)
		}
		return at("material", u.Material) + at("shape", u.Shape) + at("size", u.Size) +
			at("itemid", u.ItemID) + at("itemname", u.ItemName) + at("color", u.Color) +
			at("qty", strconv.Itoa(u.Remaining)) + at("unit", u.Unit) +
			at("cost", cost) + at("note", u.Note) +
			at("temp-page", u.TempPage) + at("temp-row", strconv.Itoa(u.TempRow))
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

// unorderedFormHTML は「発注部材表へ入れる」欄です。
//
// ⚠ **ここに「発注書を作る」は置きません**（2026-09-22 ユーザー訂正:「**発注部材表から
// 発注書を作るので「発注書を作る」ボタンは発注部材表の下にあるはずです**」）。
// **ボタンは、その相手の隣にあるべき**です——仕入先と差出人も発注部材表の足元で
// 決めます（**1枚＝1社**が決まるのは、表を作り終えたときだから）。
func unorderedFormHTML(user *auth.User, pageIDInt int) string {
	// ⚠ **ページIDは属性で渡します**——画面の配線は別のスコープに居て
	//    `currentPageId` が見えません（受注残表の書き戻しも同じ手です）。
	return `<div class="matsearch-form unorder-form" data-unorder-page="` +
		page.FormatID(pageIDInt) + `">` +
		draftTargetHTML(pageIDInt) +
		`<button type="button" class="matsearch-go" data-unorder-draft="1">` +
		`発注部材表へ入れる</button>` +
		`</div><div class="unorder-result" data-unorder-result="1"></div>`
}

// draftTargetHTML は「新しく作る／どれに足すか」の欄です（2026-09-22）。
//
// ユーザー訂正:「**発注部材表は複数できて良いはずで、新規に作るのか、どれかに足すのか
// 聞くべきです**」——⚠ **最初の実装は2枚目を 409 で断っていました**。**1枚＝1社**なので、
// **業者ごとに同時に進める**のが普通の形です。
//
// ⚠ **既定は「新しく作る」**です——**黙って既存の表へ混ぜるほうが危ない**
// （別の業者の行が1つの紙に混ざり、**紙にしてから気づきます**）。
func draftTargetHTML(pageIDInt int) string {
	n := CountOrderDrafts(pageIDInt)
	var b strings.Builder
	b.WriteString(`<label class="matsearch-field"><span>入れる先</span>`)
	b.WriteString(`<select class="matsearch-input" data-unorder="into">`)
	b.WriteString(`<option value="">新しく作る</option>`)
	for i := 1; i <= n; i++ {
		b.WriteString(`<option value="` + strconv.Itoa(i) + `">` +
			strconv.Itoa(i) + `枚目の発注部材表へ足す</option>`)
	}
	b.WriteString(`</select></label>`)
	return b.String()
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

// signerFieldHTML は差出人を選ぶ欄です（2026-09-22）。
//
// ユーザー:「担当者によって変わるので**都度選ぶ**しかないのでは？」——だから
// **毎回ここで選びます**。
//
// ⚠ **候補はサーバーが描きます**（`<select>`）。`推奨業者` のような打ちながらの候補に
// しないのは、差出人が**打つものではなく選ぶもの**だからです——連絡帳に居る人しか
// なれません。
//
// ⚠ **初期値はログイン名と同じ題の人**（`DefaultSigner`）。当たらなければ**未選択**で、
// 人が選びます。⚠ **勝手に先頭の人を選ばないこと**——**誰の名前で紙が出るか**は、
// 黙って決めてよいことではありません。
func signerFieldHTML(user *auth.User) string {
	list := Signers(user)
	def, hasDef := DefaultSigner(user, list)

	var b strings.Builder
	b.WriteString(`<label class="matsearch-field"><span>差出人</span>`)
	b.WriteString(`<select class="matsearch-input" data-unorder="signer">`)
	b.WriteString(`<option value="">（選んでください）</option>`)
	for _, sg := range list {
		sel := ""
		if hasDef && sg.PageID == def.PageID {
			sel = ` selected`
		}
		label := sg.Title
		if sg.Org != "" {
			label += "（" + sg.Org + "）"
		}
		b.WriteString(`<option value="` + page.FormatID(sg.PageID) + `"` + sel + `>` +
			stdhtml.EscapeString(label) + `</option>`)
	}
	b.WriteString(`</select></label>`)
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「誰も居ない」と「連絡帳を作っていない」は別です。
		b.WriteString(`<p class="unorder-help">⚠ 差出人の候補がありません` +
			`（連絡帳の担当者ページに「<strong>` + OrderSignatureHeading +
			`</strong>」の見出しで署名を書くと、ここに出ます）。</p>`)
	}
	return b.String()
}
