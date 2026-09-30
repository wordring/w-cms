package toho

// ─────────────────────────────────────────────────────────────────────────
// 受注の行を、あとからできた加工製品ページへ結び直す（2026-09-30 夜）
//
// 利用者:「品番〈図番〉の加工製品ページはあるのに、受注残の表に弊社品番なしになるのは何故でしょう？」——
// 受注明細の `弊社品番` を埋めるのは3つの時点だけ（受注の 🤖解析・受注の整理・図面の整理——link_item.go）で、
// **ワンノートの取り込みは整理を通らずに加工製品ページを作るので結ばない**。受注が先に整理され、加工製品
// ページがあとから取り込みで生まれると、空のまま残る（開けば鏡が「弊社品番が空です（…と思われます）」と言う）。
//
// POST /api/order-items/relink（管理者）——受注明細を持つページを全部見て、空いている `弊社品番` を
// いま在る加工製品ページで埋める。歯止めは整理のときと同じ（linkProductsToOrder）——**候補がちょうど
// 1件のときだけ・人の入れた値は触らない**。
//
//   - ⚠ **編集中の受注ページは飛ばす**（オートセーブと黙って上書きし合う）——飛ばした枚数を返す。
//   - ⚠ **何をどこへ結んだかを返す**——結び先の題に【旧】が付くなど、「本当にその品物か」を人が見られるように。
//   - 引き金は人（管理者が呼ぶ・ワンノートの製造の道具が本番の回の最後に呼ぶ）。**裏では回さない**
//     （解析も整理も「人の操作の直後だけ」という流儀に揃える）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// orderItemPageIDs は受注明細の表を持つページを返します（索引から）。
func orderItemPageIDs() []int {
	rows, err := database.DB.Query(
		`SELECT DISTINCT page_id FROM vocab_index WHERE data_type = ? ORDER BY page_id`, clientOrderItemsType)
	if err != nil {
		return nil
	}
	// ⚠ 先に読み切ってから書く（行を読みながら別のクエリ・書き込みを投げない）。
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	return ids
}

// relinkResult は結び直しの結果です。
type relinkResult struct {
	Pages   int         `json:"pages"`   // 見た受注ページ
	Rows    int         `json:"rows"`    // 埋めた行
	Editing []string    `json:"editing"` // 編集中で飛ばした受注ページ
	Links   []orderLink `json:"links"`   // 何をどこへ結んだか
}

// relinkAllOrders は受注明細を持つページを全部結び直します。
func relinkAllOrders(user *auth.User) relinkResult {
	res := relinkResult{Editing: []string{}, Links: []orderLink{}}
	for _, id := range orderItemPageIDs() {
		res.Pages++
		if _, open := editlock.Locks.EditorOpen(id); open {
			res.Editing = append(res.Editing, page.FormatID(id))
			continue
		}
		for _, l := range linkProductsToOrder(user, page.FormatID(id)) {
			res.Rows += l.Rows
			res.Links = append(res.Links, l)
		}
	}
	return res
}

// OrderItemsRelinkAPIHandler は POST /api/order-items/relink です（管理者）。
func OrderItemsRelinkAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	// ⚠ 管理者だけ——全部の受注ページの本文を書き換える口なので（整理は1枚ずつ・その人が書ける箱の中）。
	if !page.RequireAdmin(w, r) {
		return
	}
	res := relinkAllOrders(user)
	auth.AuditRequest(r, "order-items.relink", "受注 "+strconv.Itoa(res.Pages)+"枚・埋めた行 "+strconv.Itoa(res.Rows)+
		"・編集中で飛ばした "+strconv.Itoa(len(res.Editing))+"枚")
	cms.WriteJSON(w, res)
}
