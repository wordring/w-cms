package toho

// ─────────────────────────────────────────────────────────────────────────
// あとから改定にする——並べた図面の片方を旧版へ（2026-10-04）
//
// 利用者:「加工製品に図面を追加しました。するともともとあった図面と変更が見つかりました。
// そこで追加した図面を改定図面とし、元々あった図面を古い図面として子ページにしたいのです」。
//
// 整理で「図面追加」を選ぶと、図面は同じページに並びます（`mergeAsDrawing`）。あとで中身を
// 見比べて改定だったと分かっても、整理の仮のページはもうゴミ箱なので、整理からはやり直せません。
// ここは**整理の「図面改定」（`mergeAsRevision`）と同じ形へ、あとから組み替えます**:
//
//   - 古い図面は `旧版 <図面番号> <図面名称>` の子ページへ**ブロックごと**移る（`createOldVersionPage`）
//   - 新しい図面は古い図面のいた場所へ（古い図面が上にあったなら、新しい図面が上に来る）
//   - 改訂履歴に新しい図面の行を1つ足し、古い図面の行を旧版ページへのリンクにする
//   - 品名が変わっていれば、品名を2つとも残す（`renamedItemNames`）
//
// **どちらが新しいかは人が選びます**——機械には改定と別の図面（部品図と溶接図）の区別が
// つかない（revision.go の「二つ目の図面として追加」）。画面はファイルの「⋯」から、押した図面を
// 新しい図面として、古い図面を選ばせ、両方の名前を出して確かめてから送ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	stdhtml "html"
	"net/http"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// drawingPick は画面が選んだ図面です——本文の最上位の図面ブロックの何番目か（0から）と、その図面番号。
//
// ⚠ **ブロックID（data-id）では指しません**——手で書いたページの節には付いていないことがある（2026-10-04 に
// E2E で踏んだ）。番号は**確かめ**のため——画面を開いてから本文が変わっていたら（図面が足された・動かされた）、
// 何番目かがずれて別の図面を旧版へ送るので、図面番号が合わなければ断ります。
type drawingPick struct {
	Index int    `json:"index"`
	No    string `json:"no"`
}

// drawingSectionAt は本文の最上位の図面ブロックのうち、p.Index 番目で図面番号が p.No のものの位置を返します。
func drawingSectionAt(body string, p drawingPick) (sec string, open, end int, ok bool) {
	n := 0
	eachSection(body, func(s string, o, e int) {
		if !isDrawingSection(s) {
			return
		}
		if n == p.Index && strings.TrimSpace(stdhtml.UnescapeString(drawingNoOf(s))) == strings.TrimSpace(p.No) {
			sec, open, end, ok = s, o, e, true
		}
		n++
	})
	return
}

// supersedeDrawing は、ページに並んだ図面のうち oldPick を旧版の子ページへ移し、newPick をその改定図面にします。
// 返すのは作った旧版ページのID。
//
// 順序は `mergeAsRevision` と同じ——旧版ページを先に作り、次に本文を書き換えます。途中で失敗しても、
// 図面がどこにも無い状態は生まれません（旧版ページだけが残り、本文には両方がいるまま）。
func supersedeDrawing(user *auth.User, pageID string, newPick, oldPick drawingPick) (string, error) {
	if newPick.Index == oldPick.Index || newPick.Index < 0 || oldPick.Index < 0 {
		return "", errors.New("新しい図面と古い図面を、別々に選んでください")
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return "", err
	}
	oldSec, oldOpen, oldEnd, ok := drawingSectionAt(body, oldPick)
	if !ok {
		return "", errors.New("古い図面がこのページに見つかりません（ページが変わったかもしれません——開き直してください）")
	}
	newSec, newOpen, newEnd, ok := drawingSectionAt(body, newPick)
	if !ok {
		return "", errors.New("新しい図面がこのページに見つかりません（ページが変わったかもしれません——開き直してください）")
	}

	oldNo, oldName := drawingNoOf(oldSec), drawingNameOf(oldSec)
	oldPageID, err := createOldVersionPage(user, pageID, oldNo, []string{oldSec})
	if err != nil {
		return "", err
	}

	// 新しい図面を古い図面の場所へ。新しい図面が既に上にあるなら、古い図面を外すだけ。
	var next string
	if newOpen > oldOpen {
		next = body[:oldOpen] + newSec + body[oldEnd:newOpen] + body[newEnd:]
	} else {
		next = body[:oldOpen] + body[oldEnd:]
	}
	newNo := drawingNoOf(newSec)
	// リンクを先に付けてから行を足す——番号を変えない改定で新しい行にリンクが付かないように（mergeAsRevision と同じ）。
	next = linkRevisionRow(next, oldNo, oldPageID)
	next = InsertRevisionRow(next, newNo)
	if names := renamedItemNames(oldName, pageTitleOf(pageID), drawingNameOf(newSec)); len(names) > 0 {
		next = withTagValues(next, ItemNameTag, names, false)
	}
	if err := cms.RewriteBody(pageID, user.Username, func(string) string { return next }); err != nil {
		return oldPageID, err
	}
	auth.Audit(user.Username, "drawing.supersede", pageID+": "+oldNo+" → "+newNo+"（旧版 "+oldPageID+"）")
	return oldPageID, nil
}

// DrawingSupersedeAPIHandler は POST /api/drawing/supersede です
// （入力: {page_id, new:{index, no}, old:{index, no}}——本文の最上位の図面の何番目か〔0から〕とその図面番号）。
// 応答は {success, old_page, title}。
// 書けない・編集中（自分でも）・テンプレートの中は断ります（`requireWritableIdle`）。
func DrawingSupersedeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		New    drawingPick `json:"new"`
		Old    drawingPick `json:"old"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	oldPage, err := supersedeDrawing(user, pageID, req.New, req.Old)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "old_page": oldPage, "title": pageTitleOf(oldPage)})
}
