package comm

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
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"w-cms/internal/cms"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// MarkHandledAPIHandler は POST /api/intake/handled です。
// 入力: {"page_ids": ["010678", ...], "value": "済"}——まとめて押せます。
// `value` を省くと `済` です（**次へ割り振った、が普通の道**）。
func MarkHandledAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageIDs []string `json:"page_ids"`
		Value   string   `json:"value"` // 済 / 不要（省略時は 済）
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.PageIDs) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "対象がありません")
		return
	}
	// **値は表引きで閉じます**——自由に書けると `済` と `完了` が混ざり、
	// あとから「何件が不要だったか」を数えられなくなります。
	// `未処理` は印を外す（2026-09-30——メールのページで対応を選べるようにした日に足した）。
	value := strings.TrimSpace(req.Value)
	switch value {
	case HandledNotNeeded, HandledUndo:
	default:
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
		// **開いている人が居たら飛ばします**（2026-09-14）。`MarkHandled` は本文を
		// 読んで・変えて・書くので、エディタが開いていると上書きし合います。
		// ここも1件ずつ——押せたものは押せたままにするのがこの口の流儀です。
		if _, open := editlock.Locks.EditorOpen(idInt); open {
			failed++
			continue
		}
		if err := SetHandled(pageID, user.Username, value); err != nil {
			failed++
			continue
		}
		auth.Audit(user.Username, "intake.handled", pageID+" ("+value+")")
		done++
	}
	cms.WriteJSON(w, map[string]any{"success": true, "handled": done, "failed": failed})
}

// HandledUndo は「印を外して未処理に戻す」の指示です（タグの値ではない・2026-09-30）。
const HandledUndo = "未処理"

// handledPairRe は本文の `対応` のタグ（dt と dd の組）です（字下げ・属性・空の dd も拾う）。
var handledPairRe = regexp.MustCompile(`(?s)<dt[^>]*>\s*` + regexp.QuoteMeta(html.EscapeString(HandledTag)) +
	`\s*</dt>\s*<dd[^>]*>.*?</dd>`)

// SetHandled は `対応` の印を value にします（済 / 不要）。value が HandledUndo なら外して未処理に戻します
// （2026-09-30 利用者:「そのメールへの対応が終わったかどうかは、受信フォルダではなく、メールページで選択したいです」
// ——一覧の「済」「不要」は付けるだけだった〔旧 MarkHandled・既に付いていれば何もしない〕が、メールのページでは
// 付け替えも戻すもできる）。付ける先は最初の可変タグの並びで、無ければ h1 の直後に新しく作ります——**タグは
// 可変タグの中に居るのが本来**。1つだけ置くので、二度押しても増えません。
// ⚠ **呼ぶ前に `editlock.RefuseWhileEditing` を通すこと。** 本文を読んで・変えて・書くので、誰かがエディタを
// 開いていると上書きし合います（`append_page.go` の「ロックは呼ぶ側が取ります」の一件）。
func SetHandled(pageID, author, value string) error {
	if value != HandledUndo && value != HandledDone && value != HandledNotNeeded {
		return errors.New("対応の値は " + HandledDone + "・" + HandledNotNeeded + "・" + HandledUndo + " のどれかです")
	}
	return cms.RewriteBody(pageID, author, func(current string) string {
		out := handledPairRe.ReplaceAllString(current, "")
		if value == HandledUndo {
			return out
		}
		pair := `<dt>` + html.EscapeString(HandledTag) + `</dt><dd>` + html.EscapeString(value) + `</dd>`
		if at := cms.EndOfFirstTagList(out); at >= 0 {
			return out[:at] + pair + out[at:]
		}
		return cms.InsertAfterH1(out, `<dl data-type="tags">`+pair+`</dl>`)
	})
}

