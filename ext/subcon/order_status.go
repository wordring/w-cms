package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注済みの印は、発注書の**行ごと**に付く（2026-09-22）
//
// ユーザー:「**発注書の表の一品ずつに発注済みの印をつけます**。すると表がDBに入り
// 検索可能になります。ここに価格が入ると、価格の検索もできるようになります。これは、
// **メールの送信など自明な時は、自動で印をつければ良い**し、発注書を印刷して手渡し
// するような場合は、**人間が発注済みの印をつける**と思います。大事なことは、
// **発注の取り消しもある**ということです」。
//
// ⚠ **ページのタグにしませんでした。** 1枚の発注書の中でも、**行ごとに事情が違います**
// ——1品だけ取り消す、1品だけ先に納まる。ページに1つの印を置くと、**そのどれも
// 表せません**。
//
// ── 4つの値 ────────────────────────────────────────────────────────────
//
//	未発注 … 紙はできたが、まだ出していない
//	発注済 … 出した（メール・FAX・手渡し）
//	納品済 … 届いた
//	取消   … **取り消した**。手配していないのと同じ扱いに戻る
//
// ⚠ **順に進むのは事実で、印ではありません。** `未発注 → 発注済` が進むのは
// **紙が外へ出たとき**だけで、機械が勝手には進めません——メールは送信の成功が
// その事実なので自動、FAX・手渡しは**人が押したこと**がその事実です。
//
// ── 読み手が守ること ──────────────────────────────────────────────────
//
// ⚠ **`取消` の行を「手配した」に数えないこと。** 数えると、**取り消した材料が
// 未手配の一覧から消えたまま**になり、**誰も買わないまま納期が来ます**。
// ⚠ **`未発注` は数えます**——紙はできているので、**二重に発注しないため**です。
// ⚠ **`未発注` を「買った値段」に数えないこと**（`latestMaterialPrices`）。
// 出していない紙の単価は、まだ誰も承諾していません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// 発注明細の `状態` の値です。
const (
	OrderLineUnsent    = "未発注"
	OrderLineSent      = "発注済"
	OrderLineDelivered = "納品済"
	OrderLineCancelled = "取消"

	// orderLineLegacySent は 2026-09-22 より前に作った発注書の値です。
	//
	// ⚠ **当時は「紙を作る＝発注した」でした**（送る段が無かった）。だから
	// **`発注済` と同じに読みます**——`未発注` に読むと、**既に出した発注書が
	// 全部「まだ出していない」に化けます**。
	orderLineLegacySent = "未納品"
)

// orderLineStatuses は `状態` の選べる値です（宣言と画面で共有）。
func orderLineStatuses() []string {
	return []string{OrderLineUnsent, OrderLineSent, OrderLineDelivered, OrderLineCancelled}
}

// orderLineCancelled は「取り消した行」かを返します。
func orderLineCancelled(status string) bool {
	return strings.TrimSpace(status) == OrderLineCancelled
}

// orderLineSent は「もう出した行」かを返します（価格の記録に数えてよいか）。
func orderLineSent(status string) bool {
	switch strings.TrimSpace(status) {
	case OrderLineSent, OrderLineDelivered, orderLineLegacySent:
		return true
	}
	return false
}

// OrderSendCounts は発注書1枚の行の内訳です（リンクの鏡が使います）。
type OrderSendCounts struct {
	Total     int // 取消を除いた行数
	Sent      int // 発注済・納品済
	Cancelled int
}

// AllSent は「取消を除く全部が出た」かを返します。
//
// ⚠ **0行なら false** です——行の無い紙を「発注済み」と言わないため
// （`0 == 0` で真になるのが自然に見えますが、それは**沈黙を成功と読む**形です）。
func (c OrderSendCounts) AllSent() bool { return c.Total > 0 && c.Sent == c.Total }

// OrderSendStateOf は発注書ページの行の内訳を数えます。
func OrderSendStateOf(db cms.ReadOnlyDB, pageID int) OrderSendCounts {
	var c OrderSendCounts
	rows, err := cms.VocabTableRowsOf(db, pageID, ourOrderItemsType)
	if err != nil {
		return c
	}
	for _, r := range rows {
		st := r.Values["status"]
		if orderLineCancelled(st) {
			c.Cancelled++
			continue
		}
		c.Total++
		if orderLineSent(st) {
			c.Sent++
		}
	}
	return c
}

// OrderLineStatusAPIHandler は POST /api/our-order/line-status です。
//
// 入力: {page_id, row, value}——`row` は**見出しを除いたデータ行**（1始まり）。
// `row` が 0 なら**取消でない行すべて**を書き換えます（送信のときの自動の印）。
//
// ⚠ **閲覧モードから押せることが肝**です。紙を出したあとに印を付けるのに、
// いちいち編集モードへ入るのは道が遠すぎます。
func OrderLineStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
		Value  string `json:"value"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := normalizePageIDOrFail(w, req.PageID)
	if !okID {
		return
	}
	value := strings.TrimSpace(req.Value)
	if !isOrderLineStatus(value) {
		// ⚠ **知らない値は断ります**——自由に書けると、読み手の場合分けを
		//    どれも通らない行ができ、**黙って「未発注」扱い**になります。
		cms.JSONFail(w, http.StatusBadRequest, "状態は "+
			strings.Join(orderLineStatuses(), "・")+" のどれかです")
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	changed := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, n := setOrderLineStatus(cur, req.Row, value)
		changed = n
		return out
	}) {
		return
	}
	if changed == 0 {
		cms.JSONFail(w, http.StatusConflict,
			"書き換える行がありません（画面を読み直してください）")
		return
	}
	auth.Audit(user.Username, "our-order.line-status",
		pageID+" 行"+strconv.Itoa(req.Row)+" → "+value+"（"+strconv.Itoa(changed)+"行）")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "rows": changed})
}

// isOrderLineStatus は登録されている値かを返します。
func isOrderLineStatus(s string) bool {
	for _, v := range orderLineStatuses() {
		if v == s {
			return true
		}
	}
	return false
}

// setOrderLineStatus は発注明細の `状態` 列を書き換え、書き換えた行数を返します。
//
// row が 0 なら**取消でない行すべて**。⚠ **取消の行は巻き込みません**——
// まとめて「発注済」にする操作で、**取り消したはずのものが復活します**。
func setOrderLineStatus(body string, row int, value string) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return body, 0
	}
	changed := 0
	for _, t := range tables {
		rows := rowsOf(t)
		if len(rows) < 2 {
			continue
		}
		si := headerIndexOf(rows[0], "状態")
		if si < 0 {
			continue
		}
		for i, tr := range rows[1:] {
			if row > 0 && i+1 != row {
				continue
			}
			cells := cellsOf(tr)
			if si >= len(cells) {
				continue
			}
			cur := strings.TrimSpace(textOf(cells[si]))
			if row == 0 && orderLineCancelled(cur) {
				continue // まとめ書きは取消を起こさない
			}
			if cur == value {
				continue
			}
			setCellText(cells[si], value)
			changed++
		}
	}
	if changed == 0 {
		return body, 0
	}
	return htmldoc.Render(nodes), changed
}
