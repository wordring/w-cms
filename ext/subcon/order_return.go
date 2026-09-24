package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注明細の行を「必要部材表」へ戻す（2026-09-24）
//
// ユーザー:「行の末尾には、『必要部材表』へ戻すボタンがあります。このボタンは
// 編集モードでは消えます。ボタンを押すと、**発注明細表から行が消え**、必要部材表へ
// 戻ります」「発注書の送付後でも押せます」。
//
// ⚠ **戻す先へは何も書きません。** 必要部材表は毎回計算される鏡なので、行が
// 発注書から消えれば、その部材は**どこにも手配済みとして数えられなくなり**、
// 自動的に必要部材表へ現れます（発注部材表の「↩ 戻す」と同じ理屈）。
//
// ⚠ **取消とは別の操作です。** 取消は「その部材はもう発注しない」（行は残り、
// 手配済みに数える）。戻すは「この発注書では発注しない」（行が消え、また選べる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// OrderReturnRowAPIHandler は POST /api/our-order/return-row です。
//
// 入力: {page_id, row}——`row` は**見出しを除いたデータ行**（1始まり）。
func OrderReturnRowAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := cms.PageIDOrFail(w, req.PageID)
	if !okID {
		return
	}
	if req.Row < 1 {
		cms.JSONFail(w, http.StatusBadRequest, "戻す行を指定してください")
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	removed := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, ok := removeOrderRow(cur, req.Row)
		removed = ok
		return out
	}) {
		return
	}
	if !removed {
		cms.JSONFail(w, http.StatusConflict,
			"戻す行がありません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "our-order.return-row", pageID+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// removeOrderRow は最初の発注明細から、`row` 番目のデータ行を取り除きます。
//
// ⚠ **最後の1行を外しても表は残します**——発注部材表と違い、発注書ページは
// **発注書そのもの**です。表ごと消すと、行を足し直す場所が無くなります。
// ⚠ **2枚目以降の発注明細は見ません**（送信欄と同じく、1ページ1枚が前提）。
func removeOrderRow(body string, row int) (string, bool) {
	if row < 1 {
		return body, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return body, false
	}
	rows := rowsOf(tables[0]) // 先頭は見出し行
	if row >= len(rows) {
		return body, false
	}
	tr := rows[row]
	tr.Parent.RemoveChild(tr)
	return htmldoc.Render(nodes), true
}
