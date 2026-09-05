package cms

// ─────────────────────────────────────────────────────────────────────────
// 「対応：不要」を付ける口（2026-09-05）
//
// 未処理の一覧から**1クリックで片付けられる**ようにするための、コアの小さなAPIです。
//
// **これが無いと一覧が信用されなくなります。** 案内・お礼・広告のように何も作らなくて
// よい受信は、放っておくと永久に未処理として残ります。いままではページを開いて
// タグを手で打つしかなく、**過去分を取り込むと数百件がその状態で積み上がりました**
// ——「未処理が472件」は、片付ける手段が無かったことの結果です。
//
// **これが「済んだ」を決める唯一の印**です（2026-09-05）。当初は「向き：送信＝済み」と
// 見なしていましたが、**かけた電話で受注することもある**ので崩れました——向きは
// どちら向きの出来事かしか語りません。
//
// **印は「済んだ」の宣言であり、責任が次へ移った印**です（2026-09-05 ユーザー:
// 「通信箱は受注処理など次の作業に割り振った時点で処理済みとし、**処理の責任は
// 次の作業に移ります**」）。だから `済` と `不要` の2つがあります——次へ渡したのか、
// ここで終わりなのかは、あとから見て意味が違います。
//
// **付けるのはコアの語彙だけ**（`対応`）。「見積依頼にする」「受注にする」は
// 業種の語彙なので拡張の仕事で、ここには入りません——コアは「届いた／片付いた」しか
// 知らない、という線です（2026-09-05 の決定）。
//
// **編集ロックは要りません。** 人が編集中の本文を奪う操作ではなく、タグを1つ足すだけ
// ——取り込みや解析と同じ理屈です（write 権限は要ります）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// MarkHandledAPIHandler は POST /api/intake/handled です。
// 入力: {"page_ids": ["010678", ...], "value": "済"}——まとめて押せます。
// `value` を省くと `済` です（**次へ割り振った、が普通の道**）。
func MarkHandledAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		PageIDs []string `json:"page_ids"`
		Value   string   `json:"value"` // 済 / 不要（省略時は 済）
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.PageIDs) == 0 {
		JSONFail(w, http.StatusBadRequest, "対象がありません")
		return
	}
	// **値は表引きで閉じます**——自由に書けると `済` と `完了` が混ざり、
	// あとから「何件が不要だったか」を数えられなくなります。
	value := strings.TrimSpace(req.Value)
	if value != HandledNotNeeded {
		value = HandledDone
	}

	done, failed := 0, 0
	for _, raw := range req.PageIDs {
		pageID, ok := page.NormalizeID(raw)
		if !ok {
			failed++
			continue
		}
		// **1件の権限不足で全体を止めません**（整理の実行と同じ流儀）。
		// まとめて押したときに、押せたものは押せたままにしておくほうが親切です。
		idInt, err := strconv.Atoi(pageID)
		if err != nil || !page.GetPerms(idInt).CanWrite(user) {
			failed++
			continue
		}
		if err := MarkHandled(pageID, user.Username, value); err != nil {
			failed++
			continue
		}
		auth.Audit(user.Username, "intake.handled", pageID+" ("+value+")")
		done++
	}
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "handled": done, "failed": failed,
	})
}

// MarkHandled はページへ `対応` のタグを足します（値は 済 / 不要）。
//
// **既に付いていれば何もしません**（二重に押しても増えない）。付ける先は最初の
// 可変タグの並びで、無ければ h1 の直後に新しく作ります——参照タグの描画が
// 可変タグの中だけを見るのと同じで、**タグは可変タグの中に居るのが本来**です。
func MarkHandled(pageID, author, value string) error {
	pair := `<dt>` + html.EscapeString(HandledTag) + `</dt><dd>` +
		html.EscapeString(value) + `</dd>`
	return RewriteBody(pageID, author, func(current string) string {
		if hasHandledTag(current) {
			return current
		}
		if at := endOfFirstTagList(current); at >= 0 {
			return current[:at] + pair + current[at:]
		}
		return insertAfterH1(current, `<dl data-type="tags">`+pair+`</dl>`)
	})
}

// hasHandledTag は「対応：不要」が既に在るかを見ます。
//
// **文字列で見ます**——HTMLを解析し直すほどの判定ではなく、ここで拾いたいのは
// 「二度押しで2つ付く」を防ぐことだけだからです。取りこぼしても害は
// 「同じタグが2つ並ぶ」で、索引の逆引きは変わりません。
func hasHandledTag(body string) bool {
	return strings.Contains(body, `<dt>`+html.EscapeString(HandledTag)+`</dt>`)
}

// endOfFirstTagList は最初の可変タグの並びの終わり（`</dl>` の位置）を返します。
// 見つからなければ -1。
func endOfFirstTagList(body string) int {
	i := strings.Index(body, `<dl data-type="tags">`)
	if i < 0 {
		return -1
	}
	end := strings.Index(body[i:], "</dl>")
	if end < 0 {
		return -1
	}
	return i + end
}

// insertAfterH1 は h1 の直後へ差し込みます（h1 が無ければ先頭）。
func insertAfterH1(body, fragment string) string {
	if i := strings.Index(body, "</h1>"); i >= 0 {
		at := i + len("</h1>")
		return body[:at] + fragment + body[at:]
	}
	return fragment + body
}
