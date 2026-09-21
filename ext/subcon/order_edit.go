package subcon

// ─────────────────────────────────────────────────────────────────────────
// 受注残表から受注明細を編集する（2026-09-21）
//
// ユーザー:「**受注トップページの受注残集計表を鏡にして受注ページの受注明細表が
// 編集される**ようにしましょう」「表の中はだいたい編集できる」。
//
// ⚠ **これは計算ビューで初めての「書ける鏡」です。** いままでの計算ビューは
// 読むだけのクロームでした。書けるようにすると、**押した先が別のページの本文**に
// なります——取り違えると**別の受注を壊します**。歯止めを4つ置きます:
//
//  1. **行を2つの手掛かりで照合する**（行番号＋その行の `品番`）。片方だけだと、
//     間に行が挿されたときに**隣の行を書き換えます**。
//  2. **いまの値を送ってもらい、食い違えば断る**（compare-and-swap）。誰かが先に
//     直していたら、**黙って上書きしません**。
//  3. **編集ロックの関門**——その受注ページをエディタで開いている人が居れば断る
//     （オートセーブと上書き合戦になるため。2026-09-14 の決定）。
//  4. **書ける列を宣言で絞る**。`残` は計算、`弊社品番`・`品番`・`品名` は**行を
//     言い当てる鍵**なので、ここからは触らせません（鍵を書き換えると 1. が壊れます）。
//
// ⚠ **失敗したら画面を元へ戻すこと**（呼ぶ側の責任・assets/app.js）。押せたのに
// 保存できていない、がいちばん困る壊れ方です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// editableOrderFields は受注残表から書ける列です（見出しの表示文字）。
//
// ⚠ **鍵になる列は入れません。** `弊社品番`・`品番`・`品名` は行を言い当てる
// 手掛かりで、ここから書き換えると**次の書き込みが隣の行に当たります**。
// 直すのは受注ページの上で（そちらは本文を直に編集するので、鍵の問題が起きません）。
// ⚠ `残` は `数量 - 出荷済み` の計算なので、そもそも本文にありません。
var editableOrderFields = map[string]bool{
	"数量":    true,
	"出荷済み":  true,
	"状態":    true,
	"備考":    true,
	"材料発注":  true,
	"納品書発行": true,
	"請求書発行": true,
}

// errCellConflict は「いまの値が思っていたものと違う」です（誰かが先に直した）。
var errCellConflict = errors.New("この行は別の場所で変わっています。読み込み直してください")

// OrderItemEditAPIHandler は POST /api/order-item です。
//
// 入力: {page_id, row, item_no, field, old, value}
//   - row     … 受注明細の**データ行の番号**（見出しを除いて0から）
//   - item_no … その行の `品番`（⚠ **行番号と2つで照合します**）
//   - field   … 書き換える列（見出しの表示文字）
//   - old     … いまの値（⚠ **食い違えば断ります**）
func OrderItemEditAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
		ItemNo string `json:"item_no"`
		Field  string `json:"field"`
		Old    string `json:"old"`
		Value  string `json:"value"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := page.NormalizeID(req.PageID)
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	if !editableOrderFields[req.Field] {
		// ⚠ **知らない列は断ります**——鍵の列や、計算の列を書かせないため。
		cms.JSONFail(w, http.StatusBadRequest, req.Field+" はここからは編集できません")
		return
	}
	// 受注ページ自身への write（残表のページの権限では足りません）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// ⚠ **開いている人が居れば断ります。** `RequireEditLock` は使えません——
	// 一覧の画面はトークンを持てず必ず409になるためです（2026-09-14 の決定）。
	if !editlock.RefuseWhileEditing(w, pageID) {
		return
	}

	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "受注ページを読めません: "+err.Error())
		return
	}
	fixed, err := setOrderItemCell(body, req.Row, req.ItemNo, req.Field, req.Old, req.Value)
	if err != nil {
		// ⚠ **食い違いは 409**（相手が悪いのではなく、状態がずれている）。
		code := http.StatusBadRequest
		if errors.Is(err, errCellConflict) {
			code = http.StatusConflict
		}
		cms.JSONFail(w, code, err.Error())
		return
	}
	if fixed == body {
		// 値が同じなら書きません（版と更新日時を無駄に進めない）。
		json.NewEncoder(w).Encode(map[string]any{"success": true, "changed": false})
		return
	}
	if err := cms.RewriteBody(pageID, user.Username, func(string) string { return fixed }); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "order-item.edit",
		pageID+" 行"+req.ItemNo+" "+req.Field+"="+req.Value)
	json.NewEncoder(w).Encode(map[string]any{"success": true, "changed": true})
}

// setOrderItemCell は受注明細の1セルを書き換えた本文を返します。
//
// ⚠ **照合は2つ**——行番号と、その行の `品番`。⚠ **いまの値も見ます**（食い違えば
// `errCellConflict`）。3つとも通らないと1文字も書きません。
func setOrderItemCell(body string, row int, itemNo, field, oldVal, newVal string) (string, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", errors.New("本文を読めません")
	}
	var table *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" && isOrderItemsTable(n) {
			table = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if table == nil {
		return "", errors.New("受注明細の表が見つかりません")
	}

	rows := rowsOf(table)
	if len(rows) < 2 || row < 0 || row+1 >= len(rows) {
		return "", errCellConflict
	}
	head := map[string]int{}
	i := 0
	for c := rows[0].FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
			head[strings.TrimSpace(textOf(c))] = i
			i++
		}
	}
	col, okCol := head[field]
	itemCol, okItem := head[ItemNoTag]
	if !okCol || !okItem {
		// ⚠ **宣言に列を足しても、既にあるページの本文には現れません。**
		// 新しく解析したページにだけ出ます（2026-09-21 に実機で踏みました——
		// ユーザー報告:「チェックボタンを押すと、材料発注 の列がありませんと出てます」）。
		//
		// ⚠ **自動で足す枝は置きません**（ユーザー決定・同日:「無ければ足すコードは
		// 必要ないです。**今は開発中なので**」）。解析し直せば揃うあいだは、
		// **機構を増やさない**ほうが良いという判断です。運用に入ったあとで同じことが
		// 起きたら、そのとき**移行として1度だけ**走らせる形を考えること
		// ——書き込みのたびに表の形を変える口を常設すると、想定外の列が生えます。
		return "", errors.New(field + " の列がこのページの受注明細にありません" +
			"（あとから宣言に足した列は、解析し直すまで出ません）")
	}
	cells := cellsOf(rows[row+1])
	if col >= len(cells) || itemCol >= len(cells) {
		return "", errCellConflict
	}
	// ⚠ **行が入れ替わっていないか。** 行番号だけだと、間に行が挿されたときに
	// **隣の行を書き換えます**——番号は動くが `品番` は動かない、という非対称を使います。
	if strings.TrimSpace(textOf(cells[itemCol])) != strings.TrimSpace(itemNo) {
		return "", errCellConflict
	}
	// ⚠ **誰かが先に直していないか**（compare-and-swap）。黙って上書きしません。
	if strings.TrimSpace(textOf(cells[col])) != strings.TrimSpace(oldVal) {
		return "", errCellConflict
	}

	cell := cells[col]
	for cell.FirstChild != nil {
		cell.RemoveChild(cell.FirstChild)
	}
	if v := strings.TrimSpace(newVal); v != "" {
		cell.AppendChild(&html.Node{Type: html.TextNode, Data: v})
	}
	return htmldoc.Render(nodes), nil
}
