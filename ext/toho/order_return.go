package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材発注明細表の行を「必要部材表」へ戻す（2026-09-24）
//
// ユーザー:「行の末尾には、『必要部材表』へ戻すボタンがあります。このボタンは
// 編集モードでは消えます。ボタンを押すと、**発注明細表から行が消え**、必要部材表へ
// 戻ります」「発注書の送付後でも押せます」。
// （2026-10-12 に編集モードをやめました。2026-10-19 までは書ける形〔`body[edit-mode]`・パソコンの既定〕で
// 隠れ、閲覧モードでだけ出ていました。2026-10-19 からは書ける形でも出します——`our_order_mirror.go` の
// `orderRowReturnButtonHTML`。）
//
// ⚠ **戻す先へは何も書きません。** 必要部材表は毎回計算される鏡なので、行が
// 部材発注書から消えれば、その部材は**どこにも手配済みとして数えられなくなり**、
// 自動的に必要部材表へ現れます（発注部材表の「↩ 戻す」と同じ理屈）。
//
// ⚠ **取消とは別の操作です。** 取消は「その部材はもう発注しない」（行は残り、
// 手配済みに数える）。戻すは「この部材発注書では発注しない」（行が消え、また選べる）。
//
// （2026-10-21 から、必要部材表と臨時部材表は無い。ボタンの名前は「↩ 発注から外す」で、外した部材は、
// 製造ページの「📦 この製造の部材を発注部材表へ」で、また出せる。弊社品番の無い行は、外すと消える。）
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"reflect"
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
	// （次の4行は 2026-10-21 までの説明——いまは臨時部材表が無く、弊社品番の無い行も外すだけ。下の「（弊社品番の無い行を…）」の注記。）
	// ⚠ **弊社品番の無い行は、部材発注フォルダの臨時部材表へ戻します**（2026-09-25。2026-10-19 からはトップ直下の「製造」の臨時部材表）——
	//    必要部材表の計算に乗らないので、外すだけだと消えます（09-24 ユーザー報告:
	//    「必要部材表へ戻すを押すと発注明細から消えますが、必要部材表へは表示されません」）。
	//    ⚠ **先に臨時部材表へ足してから、部材発注書から外します**——途中で失敗したとき、
	//    行が**消える**より**2か所に残る**ほうへ倒すためです（残ったほうは人が消せる）。
	cur, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "部材発注書のページを読めません: "+err.Error())
		return
	}
	_, first, ok := takeOrderRow(cur, req.Row)
	if !ok {
		cms.JSONFail(w, http.StatusConflict, "戻す行がありません（画面を読み直してください）")
		return
	}
	// （弊社品番の無い行を臨時部材表へ戻す道は、2026-10-21 に臨時部材表ごと外した——いまは外すだけ。）
	// ⚠ **外すのは、読んだときと同じ中身の行だけ**です（2026-10-20）——行は錠の外で読んで臨時部材表へ写したので、そのあいだに
	//    ほかの人の保存で行がずれると、番号だけで外すと**写した行とは別の行が消えていた**。中身が違えば外さずに断ります。
	removed, moved := false, false
	if rebuildBeforeWrite != nil {
		rebuildBeforeWrite(pageID) // 試験だけ（読んでから書くまでのあいだに、ほかの人の保存を挟む）
	}
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, ln, ok := takeOrderRow(cur, req.Row)
		if !ok {
			return cur
		}
		if !reflect.DeepEqual(ln, first) {
			moved = true
			return cur
		}
		removed = true
		return out
	}) {
		return
	}
	if moved || !removed {
		cms.JSONFail(w, http.StatusConflict, "戻す行が変わっています（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "our-order.return-row", pageID+" 行"+strconv.Itoa(req.Row))
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// removeOrderRow は最初の部材発注明細表から、`row` 番目のデータ行を取り除きます。
//
// ⚠ **最後の1行を外しても表は残します**——発注部材表と違い、部材発注書ページは
// **部材発注書そのもの**です。表ごと消すと、行を足し直す場所が無くなります。
// ⚠ **2枚目以降の部材発注明細表は見ません**（送信欄と同じく、1ページ1枚が前提）。
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
