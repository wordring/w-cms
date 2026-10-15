package toho

// ─────────────────────────────────────────────────────────────────────────
// 本文を書き換える口の前口上（2026-09-23 に寄せた）
//
// 09-22 に一日で書かれた口（発注部材表を作る・行を外す・発注済の印・送った印・
// PDFを作る・受注明細表のセル）が、**同じ3段を6回**写していました——ページIDを
// 6桁へ畳む → そのページの write を確かめる → 誰かが編集中なら断る。そして
// `RewriteBody` の失敗を 500 で返す4行も5回。ここへ寄せ、以後はここを通します。
//
// ⚠ **順序がこの関門の全部です。** 畳めないIDで権限を引くと「読めない」と
// 「存在しない」の区別が漏れ、編集中の判定を write より先にすると、書けない人に
// 「誰が開いているか」を教えることになります。
//
// （2026-10-12 から）ブラウザは編集権を取らなくなりました。3段目の「誰かが編集中なら断る」は、
// 道具が `/api/lock` で一瞬握るとき以外は素通りです。人の保存とぶつからないのは、
// `RewriteBody` がページごとの書き込みの錠（`cms.LockPageBody`）の中で書くからです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"

	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// requireWritableIdle は「そのページへ write できて、テンプレートの中ではなく、
// 道具が編集権を握っていない」を確かめます。
//
// ⚠ **機械が既存ページの本文を書き換えるときの関門**です。
// 2026-10-12 までは、エディタが開いているとオートセーブと機械の書き込みが黙って上書きし合うので、
// 誰かが開いていれば（自分でも）断っていました。いまはブラウザが編集権を取らないので、
// 最後の `editlock.RefuseWhileEditing` は、道具（`/api/lock` → `/api/save`）が一瞬握るとき以外は素通りです。
// 人の保存とは、`RewriteBody` の書き込みの錠（`cms.LockPageBody`）で順に並び、
// 同じブロックがぶつかれば画面が両方を並べます（機械も「ほかの書き手の1人」）。
// `RequireEditLock` は使えません（一覧の画面はトークンを持てず必ず 409 になる・2026-09-14 の決定）。
func requireWritableIdle(w http.ResponseWriter, r *http.Request, pageID string) bool {
	if !page.RequirePageWrite(w, r, pageID) {
		return false
	}
	// ⚠ **テンプレートの中へは書きません**（2026-09-27 利用者:「テンプレート以下では鏡もボタンも
	// 機能しないようにしましょう」）——テンプレートの上で押した発注部材表などが、そこから作る
	// ページ全部へコピーされるため（コアの `RefuseTemplateArea`）。権限の関門より後に置く。
	if !cms.RefuseTemplateArea(w, pageID) {
		return false
	}
	return editlock.RefuseWhileEditing(w, pageID)
}

// gateWritablePage は上の2つを続けて通し、通れば6桁のページIDを返します。
func gateWritablePage(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	pageID, ok := cms.PageIDOrFail(w, raw)
	if !ok {
		return "", false
	}
	if !requireWritableIdle(w, r, pageID) {
		return "", false
	}
	return pageID, true
}

// rewriteBodyOrFail は本文を書き換えます。書けなければ 500 を返して false。
func rewriteBodyOrFail(w http.ResponseWriter, pageID, username string, fn func(string) string) bool {
	if err := cms.RewriteBody(pageID, username, fn); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+err.Error())
		return false
	}
	return true
}
