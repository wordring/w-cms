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
	// ⚠ **弊社品番の無い行は、発注フォルダの臨時部材表へ戻します**（2026-09-25）——
	//    必要部材表の計算に乗らないので、外すだけだと消えます（09-24 ユーザー報告:
	//    「必要部材表へ戻すを押すと発注明細から消えますが、必要部材表へは表示されません」）。
	//    ⚠ **先に臨時部材表へ足してから、発注書から外します**——途中で失敗したとき、
	//    行が**消える**より**2か所に残る**ほうへ倒すためです（残ったほうは人が消せる）。
	returned := false
	if cur, err := cms.ReadPageBody(pageID); err == nil {
		if _, ln, ok := takeOrderRow(cur, req.Row); ok && needsTempParts(ln) {
			if err := returnToTempParts(user, ln); err != nil {
				cms.JSONFail(w, http.StatusConflict, "臨時部材表へ戻せません: "+err.Error())
				return
			}
			returned = true
		}
	}
	removed := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, _, ok := takeOrderRow(cur, req.Row)
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
	to := "必要部材表"
	if returned {
		to = "臨時部材表"
	}
	auth.Audit(user.Username, "our-order.return-row", pageID+" 行"+strconv.Itoa(req.Row)+" → "+to)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "to": to})
}

// removeOrderRow は最初の発注明細から、`row` 番目のデータ行を取り除きます。
//
// ⚠ **最後の1行を外しても表は残します**——発注部材表と違い、発注書ページは
// **発注書そのもの**です。表ごと消すと、行を足し直す場所が無くなります。
// ⚠ **2枚目以降の発注明細は見ません**（送信欄と同じく、1ページ1枚が前提）。
func removeOrderRow(body string, row int) (string, bool) {
	out, _, ok := takeOrderRow(body, row)
	return out, ok
}

// takeOrderRow は removeOrderRow と同じく行を外し、**外した行の中身**も返します。
func takeOrderRow(body string, row int) (string, ourOrderLine, bool) {
	if row < 1 {
		return body, ourOrderLine{}, false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, ourOrderLine{}, false
	}
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return body, ourOrderLine{}, false
	}
	rows := rowsOf(tables[0]) // 先頭は見出し行
	if row >= len(rows) {
		return body, ourOrderLine{}, false
	}
	tr := rows[row]
	line := lineOfRow(rows[0], tr)
	tr.Parent.RemoveChild(tr)
	return htmldoc.Render(nodes), line, true
}
