package cms

// ─────────────────────────────────────────────────────────────────────────
// 子ページの並べ替え（2026-09-03）
//
// ユーザー:「左レールの子ページ一覧をドラッグで順番を変えることを考えたうえで
// 判断してください」——その検討の結果が並び順キー（handler_tree.go の
// sortChildren）で、ここはそれを人の手で書き換える口です。
//
// **並べ替えたら、その親の子を全部振り直します。** 1件だけ書く「隣どうしの中間の
// 値」方式より書き込みは増えますが、`0000000010, 0000000020, …` という単純な
// 番号で済み、境目の計算（文字列のどこに割り込むか）という例外の出やすい部分が
// 丸ごと消えます。子の数はせいぜい数百なので、書き込みの数は問題になりません。
//
// **途中で失敗しても壊れません。** 各ページのキーは独立して有効な値なので、
// 一部が古いままでも順序が少しずれるだけです（前ページを指す連結リストなら、
// ここで鎖が切れて以降の順序が全部失われます——採らなかった理由の1つ）。
//
// 送るのは**その親の子の並び全部**です。「どこへ落としたか」ではなく「結果どう
// 並ぶか」を受け取るので、サーバーは前後関係を推測しません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// reorderSpacing は振り直しの間隔です。10 ずつ空けるのは、あとで
// 「1件だけ中間へ差し込む」方式を足したくなったときの余地です。
const reorderSpacing = 10

// ReorderKey は n 番目（0 起点）の並び順キーを返します。
//
// **ゼロ詰めの数字**にするのは、文字列として比べても数の順になるためです
// （`0000000090` < `0000000100`）。桁を固定しないと 9 と 10 が逆になります。
func ReorderKey(n int) string {
	return fmt.Sprintf("%010d", (n+1)*reorderSpacing)
}

// ReorderAPIHandler は POST /api/reorder?parent=P です。
// 入力: {"order": ["000123", "000456", ...]}——その親の子の**並び全部**。
func ReorderAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	parentID, ok := page.NormalizeID(r.URL.Query().Get("parent"))
	if !ok {
		JSONFail(w, http.StatusBadRequest, "親ページIDが不正です")
		return
	}
	// **並べ替えは親の中身をいじる操作**なので、親への書き込み権限を要求します
	// （子ページを作るのと同じ規則）。本文は変わらないので編集ロックは要りません。
	if !page.RequirePageWrite(w, r, parentID) {
		return
	}
	var req struct {
		Order []string `json:"order"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		JSONFail(w, http.StatusBadRequest, "親ページIDが不正です")
		return
	}

	user := auth.CurrentUser(r)
	changed := 0
	for i, raw := range req.Order {
		childID, ok := page.NormalizeID(raw)
		if !ok {
			JSONFail(w, http.StatusBadRequest, "ページIDが不正です: "+raw)
			return
		}
		// **本当にこの親の子か確かめます。** 確かめないと、並べ替えの口が
		// 「任意のページのサイドカーを書き換える口」になります。
		var actual int
		if err := database.DB.QueryRow(
			`SELECT COALESCE(parent_id, -1) FROM pages WHERE id = ?`,
			mustAtoi(childID)).Scan(&actual); err != nil || actual != parentInt {
			JSONFail(w, http.StatusBadRequest, "この親の子ではありません: "+childID)
			return
		}
		meta, ok := page.ReadSidecar(childID)
		if !ok {
			continue // サイドカーが無いページは飛ばす（正本が無いものは触らない）
		}
		key := ReorderKey(i)
		if meta.SortKey == key {
			continue
		}
		meta.SortKey = key
		if err := page.WriteSidecar(childID, meta); err != nil {
			JSONFail(w, http.StatusInternalServerError, "並び順を保存できません: "+err.Error())
			return
		}
		changed++
	}
	if user != nil && changed > 0 {
		auth.Audit(user.Username, "reorder", parentID+" ("+strconv.Itoa(changed)+"件)")
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "changed": changed})
}

// mustAtoi はゼロ詰めIDを数値にします（NormalizeID を通った値だけを渡すこと）。
func mustAtoi(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}
