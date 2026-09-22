package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注部材表——人が足し引きする候補の表（2026-09-22）
//
// ユーザー:「候補の表は**発注ページの未発注の表の下**で良いのでは？未発注の表の
// チェックボックスをクリックして（**必要なければ何もクリックしなくても**）**表作成
// ボタン**をクリックすると**発注部材表**が開き、そこを編集して**発注書作成**を
// クリックすると、**発注書ページ**ができ…」
//
//	発注（ページ）
//	  ├ 未発注の表     … 鏡（毎回計算・読み取り専用）
//	  │    ↓ チェック（何もクリックしなくてもよい）→ [表作成]
//	  ├ 発注部材表     … **本文の表**（人が足し引きする）  ← ここ
//	  │    ↓ 編集して → [発注書作成]
//	  └ 2026年／09月／発注 みなと商店
//
// ── なぜ本文に書いてよいのか ───────────────────────────────────────────
//
// ⚠ **人が足し引きした結果は「推測」ではなく「事実」だからです。**
// 「**今回は**自社加工する」「この消耗品も一緒に頼む」は**機械には導けません**——
// だから「印と事実を二重に持たない」（[【考察】製造という軸.md] §9）とぶつかりません。
//
// ⚠ **未手配の一覧（鏡）とは役割が違います。** あちらは受注明細と発注明細から
// **毎回計算される候補**で、こちらは**人が決めた結果**です。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// NewOrderDraftAPIHandler は POST /api/our-order/draft です。
//
// 発注ページの本文へ**発注部材表を1つ**足します（入力: {page_id, lines}）。
//
// ⚠ **行が0でも作ります**（ユーザー:「必要なければ何もクリックしなくても」）——
// **空の表から始められる**ことが、加工製品ページに無い部材（消耗品・治具）だけを
// 買うときの道です。
func NewOrderDraftAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string         `json:"page_id"`
		Lines  []ourOrderLine `json:"lines"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := page.NormalizeID(req.PageID)
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// 本文へ表を足すので write が要ります。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// ⚠ **機械が既存ページの本文を書き換えるときの関門**（`RewriteBody` は読んで・
	//    変えて・書くので、エディタが開いているとオートセーブと上書きし合います）。
	//    ⚠ **自分が開いていても断ります。**
	if !editlock.RefuseWhileEditing(w, pageID) {
		return
	}

	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return
	}
	// ⚠ **2枚目は作りません。** 流れは「表作成 → 編集 → 発注書作成」が一続きなので、
	//    表が2つあると**どちらから発注書を作るのか**が決まりません。
	if isTableOfTypeInBody(body, OrderDraftType) {
		cms.JSONFail(w, http.StatusConflict,
			"発注部材表が既にあります（発注書を作るか、表を消してから作り直してください）")
		return
	}

	table := orderDraftHTML(req.Lines)
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		return cur + table
	}); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "order-draft.create", pageID+" "+itoa(len(req.Lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "rows": len(req.Lines)})
}

// orderDraftHTML は発注部材表のHTMLを組みます。
//
// ⚠ **見出し行は宣言から組みます**（`headerRowHTML`）——手書きに戻すと、列を足した日に
// **足した列がどこからも読めません**。
//
// ⚠ **行が0でも表の骨は出します**——人がそこへ書き足せるように。**空の表は
// 「作れなかった」ではなく「これから書く」**です。
func orderDraftHTML(lines []ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<table data-type="` + OrderDraftType + `"><caption>` +
		stdhtml.EscapeString(displayNameOf(OrderDraftType)) + `</caption><tbody>`)
	b.WriteString(headerRowHTML(OrderDraftType))
	for _, ln := range lines {
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(OrderDraftType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(orderLineValue(ln, c.Field)) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	if len(lines) == 0 {
		// ⚠ **空の行を1つ置きます**——見出しだけの表は、エディタで行を足す取っ掛かりが
		//    ありません（人が「ここへ書く」と分かる形にする）。
		b.WriteString(`<tr>`)
		for range columnsOf(OrderDraftType) {
			b.WriteString(`<td></td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// orderLineValue は画面から来た1行から、列の値を取り出します。
//
// ⚠ **`buildOurOrderHTML` と同じ規則**を通します——材料の行に `品名` を書かない・
// 単価 `0` は書かない。**発注部材表と発注書で値が違うと、人はどちらを信じてよいか
// 分かりません。**
func orderLineValue(ln ourOrderLine, field string) string {
	switch field {
	case "our-item-id":
		return strings.TrimSpace(ln.ProductID)
	case "item-name":
		return itemNameOf(ln)
	case "material":
		return strings.TrimSpace(ln.Material)
	case "shape":
		return strings.TrimSpace(ln.Shape)
	case "size":
		return strings.TrimSpace(ln.Size)
	case "quantity":
		return strings.TrimSpace(ln.Quantity)
	case "unit":
		return strings.TrimSpace(ln.Unit)
	case "cost":
		return moneyOrEmpty(ln.Cost)
	case "note":
		return strings.TrimSpace(ln.Note)
	case "status":
		// ⚠ **発注部材表には「状態」を書きません**——まだ発注していないので、
		//    `未納品` と書くと**もう注文したように見えます**。
		return ""
	}
	return ""
}

// isTableOfTypeInBody は本文にその形式の表があるかを返します。
//
// ⚠ **文字列では探しません**——表は `data-type` でも `<caption>` でも名乗れるので
// （2026-09-20）、`strings.Contains` だと**キャプションで名乗った表を見落とします**。
func isTableOfTypeInBody(body, vocabType string) bool {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	return len(tablesOfType(nodes, vocabType)) > 0
}

// itoa は小さな数を文字列にします（監査の文のため）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
