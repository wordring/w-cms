package toho

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
//	  │    ↓ 編集して → [部材発注書作成]
//	  └ 2026年／09月／発注 みなと商店
//
// （図は 2026-09-22 の形です。いまは、必要部材表〔もとの「未発注の表」〕と発注部材表の置き場がトップ直下の「製造」
// 〔2026-10-19 までは「部材発注」〕か製造ページで、部材発注書ページは `記録／年／月／製造記録`〔製造ページから作れば
// その子〕に「【弊社】部材発注　みなと商店」の題でできます——our_order_new.go。）
//
// ── なぜ本文に書いてよいのか ───────────────────────────────────────────
//
// ⚠ **人が足し引きした結果は「推測」ではなく「事実」だからです。**
// 「**今回は**自社加工する」「この消耗品も一緒に頼む」は**機械には導けません**——
// だから「印と事実を二重に持たない」（[【考察】製造という軸.md] §9）とぶつかりません。
//
// ⚠ **未手配の一覧（鏡）とは役割が違います。** あちらは受注明細表と部材発注明細表から
// **毎回計算される候補**で、こちらは**人が決めた結果**です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// fieldsOfHeader は表の見出し行の列を、宣言の機械キー（Field）で返します（宣言に無い見出しは空——値を入れない）。
// 見出し行が無ければ宣言の並びです。
func fieldsOfHeader(table *html.Node, vocabType string) []string {
	byLabel := map[string]string{}
	var all []string
	for _, c := range columnsOf(vocabType) {
		byLabel[c.Label] = c.Field
		all = append(all, c.Field)
	}
	rows := rowsOf(table)
	if len(rows) == 0 {
		return all
	}
	var out []string
	for _, h := range cellTexts(rows[0]) {
		out = append(out, byLabel[strings.TrimSpace(h)])
	}
	if len(out) == 0 {
		return all
	}
	return out
}

// CountOrderDrafts はページの本文にある発注部材表の数を返します。
//
// ⚠ **画面が「新しく作る／1枚目へ足す／2枚目へ足す」を出すため**に要ります。
func CountOrderDrafts(pageID int) int {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return 0
	}
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return 0
	}
	return len(tablesOfType(nodes, OrderDraftType))
}

// orderDraftHTML は発注部材表のHTMLを組みます。
//
// ⚠ **見出し行は宣言から組みます**（`headerRowHTML`）——手書きに戻すと、列を足した日に
// **足した列がどこからも読めません**。
//
// ⚠ **行が0でも表の骨は出します**——人がそこへ書き足せるように。**空の表は
// 「作れなかった」ではなく「これから書く」**です。
// （この説明は putLinesIntoDraft の上に離れて残っていたので、2026-10-20 にここへ戻しました。）
func orderDraftHTML(lines []ourOrderLine) string {
	// ⚠ **節（`<section>`）で包みます**（2026-09-28・`draftBoxOf`）——「部材発注書を作る」の欄を表の外に出すため。
	return tableOfLinesHTML(OrderDraftType, lines, true)
}

// draftBoxOf は発注部材表を包む節を返します（包まれていなければ表そのもの）。
//
// 利用者（2026-09-28）:「発注部材表の発注書作成部分がtfootに入っていますが、テーブルの外のブロックに
// 出来ませんか？」「表をsectionで囲っても無理ですか？」——表の中（tfoot）に置いた欄は表の横スクロールに
// 巻き込まれ、17列の表では右へ長く伸びていました。表の外へ素で出すと、エディタが**独立したブロック**として
// 扱う（ドラッグ・保存に紛れる）ので、**表を節で包み、欄はその節の中（表の直後）**に置きます。
// 発注部材表は**機械だけが作る**（利用者:「手で書き込むことはありません」——スラッシュメニューにも出さない）
// ので、作るときに包み、消すときは節ごと消します（節と表は一組）。
func draftBoxOf(table *html.Node) *html.Node {
	if p := table.Parent; p != nil && p.Data == "section" {
		return p
	}
	return table
}

// orderLineValue は画面から来た1行から、列の値を取り出します。
//
// ⚠ **`buildOurOrderHTML` と同じ規則**を通します——材料の行に `品名` を書かない・
// 単価 `0` は書かない。**発注部材表と部材発注書で値が違うと、人はどちらを信じてよいか
// 分かりません。**
func orderLineValue(ln ourOrderLine, field string) string {
	switch field {
	case "our-item-id":
		return strings.TrimSpace(ln.ProductID)
	case "for-order":
		return strings.TrimSpace(ln.ForOrder)
	case "mfg":
		return strings.TrimSpace(ln.Mfg)
	case "breakdown":
		return strings.TrimSpace(ln.Breakdown)
	case "vendor":
		return strings.TrimSpace(ln.Vendor)
	case "item-id":
		return strings.TrimSpace(ln.ItemID)
	case "kind":
		return strings.TrimSpace(ln.Kind)
	case "no":
		return strings.TrimSpace(ln.No)
	case "work":
		return strings.TrimSpace(ln.Work)
	case "spec":
		return strings.TrimSpace(ln.Spec)
	case "supplied":
		return strings.TrimSpace(ln.Supplied)
	case "color":
		return strings.TrimSpace(ln.Color)
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
	case "lot":
		return strings.TrimSpace(ln.Lot)
	case "unit":
		return strings.TrimSpace(ln.Unit)
	case "cost":
		return costCell(ln.Cost)
	case "note":
		return strings.TrimSpace(ln.Note)
	case "status":
		// ⚠ **発注部材表には「状態」を書きません**——まだ発注していないので、
		//    `未納品` と書くと**もう注文したように見えます**（部材発注書の行の既定は 2026-09-22 から `未発注`——
		//    our_order_new.go。発注部材表はそれも書きません）。
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

// removeDraftAfterOrder は、部材発注書を作り終えた元の発注部材表を消します。
//
// ⚠ **表は消します**——中身は部材発注書ページへ移ったので、**残すと古い写しになり、
// 次の発注のときに混ざります**。
//
// ⚠ **リンクは残しません**（2026-09-24 に変えた）。09-22 はユーザーの案（「発注書
// ページが出来て、実際に発注するまで発注ページに発注書ページへのリンクが残れば良い
// のでは？」）でリンクに化けさせていましたが、**本文に書いたリンクは出しても消しても
// 残り**、「⚠ まだ発注していません（4行）」がゴミとして溜まりました（ユーザー報告）。
// いまは部材発注フォルダ（2026-10-19 からはトップ直下の「製造」）の「未発注の部材発注書」（`unsent_orders.go`）が**DBから毎回数える**ので、
// 出せば消え、部材発注書を消せば消えます。
//
// ⚠ **うまくいかなくても部材発注書は取り消しません**——**紙のほうが重い**ので、
// 「表が残ってしまった」は人が消せば済みます。返すのは**添える一文**だけです。
func removeDraftAfterOrder(user *auth.User, draftPage, draftIndex string) string {
	if strings.TrimSpace(draftPage) == "" || strings.TrimSpace(draftIndex) == "" {
		return "" // どの表から作ったか分からない（画面が古いときなど）
	}
	pageID, ok := page.NormalizeID(draftPage)
	if !ok {
		return "⚠ 元の発注部材表を片付けられません（ページIDが不正です）"
	}
	n, err := strconv.Atoi(strings.TrimSpace(draftIndex))
	if err != nil || n < 1 {
		return "⚠ 元の発注部材表を片付けられません（何枚目か分かりません）"
	}
	// ⚠ **開いている人が居たら触りません**——オートセーブと上書きし合います。
	// （2026-10-12 から、ブラウザは編集権を取らないので、触らないのは道具〔`/api/lock`〕が握っているときだけ。
	// 人の保存とは `cms.RewriteBody` の錠で順に並びます。）
	done := false
	if werr := cms.RewriteBodyUnlessHeld(pageID, user.Username, func(cur string) string {
		out, ok := replaceDraftTable(cur, n, "")
		done = ok
		if !ok {
			return cur
		}
		return out
	}); werr != nil {
		if held := (*cms.HeldError)(nil); errors.As(werr, &held) {
			return "⚠ 元の発注部材表はそのままです（誰かが部材発注ページを編集中です）"
		}
		return "⚠ 元の発注部材表を片付けられません: " + werr.Error()
	}
	if !done {
		return "⚠ 元の発注部材表が見つかりませんでした（画面を読み直してください）"
	}
	return ""
}

// replaceDraftTable は本文の n 枚目の発注部材表を、渡したHTMLで置き換えます。
//
// ⚠ **木で探して木で差し替えます**——文字列でやると、`<caption>` で名乗った表や
// 入れ子の `</table>` で切り損ねます。
func replaceDraftTable(body string, n int, replacementHTML string) (string, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, OrderDraftType)
	if n < 1 || n > len(tables) {
		return body, false
	}
	repl, rerr := htmldoc.ParseFragment(replacementHTML)
	if rerr != nil {
		return body, false
	}
	// ⚠ トップレベルの表には `Parent` が無い（`spliceNodes` がその罠を引き受ける）。
	// 節で包まれていれば節ごと差し替える（`draftBoxOf`）。
	return spliceNodes(nodes, draftBoxOf(tables[n-1]), repl, false)
}

// RemoveOrderDraftRowAPIHandler は POST /api/our-order/draft/remove です。
//
// 発注部材表から行を1つ外します（入力: {page_id, table, row}）。
//
// ユーザー（2026-09-22）:「**発注部材表から未手配の一覧へ戻す方法がありません**」
//
// ⚠ **「戻す」は「外す」です。** 一覧は**毎回計算される鏡**なので、発注部材表から
// 消せば**自動的に戻ってきます**——戻す先へ何かを書く必要はありません。
//
// ⚠ **閲覧モードから押せることが肝**です。本文の表なのでエディタでも消せますが、
// **一覧を見ながら出し入れする**のに、いちいち編集モードへ入るのは道が遠すぎます。
// （2026-10-12 に編集モードをやめました。いまは閲覧モードでも書ける形でも押せます。）
func RemoveOrderDraftRowAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Table  int    `json:"table"` // 何枚目の発注部材表か（1始まり）
		Row    int    `json:"row"`   // 見出しを除いた何行目か（1始まり）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	// （弊社品番の無い行を臨時部材表へ戻す道は、2026-10-21 に臨時部材表ごと外した——いまは外すだけ。
	// 利用者:「臨時部材表と手配不要表もなくし、必要部材表もなくしてください。」「一度無くして、適切な形で作り直します」）
	done := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, _, ok := takeDraftRow(cur, req.Table, req.Row)
		if !ok {
			return cur
		}
		done = true
		return out
	}) {
		return
	}
	if !done {
		cms.JSONFail(w, http.StatusConflict,
			"その行が見つかりません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "order-draft.remove",
		pageID+" 表"+strconv.Itoa(req.Table)+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// removeDraftRow は n 枚目の発注部材表から row 行目（見出しを除く）を外します。
//
// ⚠ **見出し行は数えません**——人が画面で見ている「何行目」と揃えるためです。
// ⚠ **鏡が足した `<tfoot>` の行も数えません**（あれは本文ではありません）。
func removeDraftRow(body string, n, row int) (string, bool) {
	out, _, ok := takeDraftRow(body, n, row)
	return out, ok
}

// takeDraftRow は removeDraftRow と同じく行を外し、**外した行の中身**も返します
// （弊社品番の無い行を臨時部材表へ戻すため・2026-09-25）。
func takeDraftRow(body string, n, row int) (string, ourOrderLine, bool) {
	// ⚠ **最後の1行を外したら、表ごと消します**（2026-09-22 ユーザー:「**戻しても部材表から消えません**」）。
	//    **見出しだけの表が残ると、部材発注ページに空の表が溜まります**——しかも「何枚目へ足すか」の選択肢に並ぶので、
	//    **押し間違いの元**になります。⚠ **空の表を作る道は残します**（何も選ばずに「発注部材表へ入れる」）。
	out, lines, ok := takeTableRows(body, OrderDraftType, n, []int{row}, true)
	if !ok || len(lines) == 0 {
		return body, ourOrderLine{}, false
	}
	return out, lines[0], true
}

