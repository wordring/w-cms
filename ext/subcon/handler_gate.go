package subcon

// ─────────────────────────────────────────────────────────────────────────
// 本文を書き換える口の前口上（2026-09-23 に寄せた）
//
// 09-22 に一日で書かれた口（発注部材表を作る・行を外す・発注済の印・送った印・
// PDFを作る・受注明細のセル）が、**同じ3段を6回**写していました——ページIDを
// 6桁へ畳む → そのページの write を確かめる → 誰かが編集中なら断る。そして
// `RewriteBody` の失敗を 500 で返す4行も5回。ここへ寄せ、以後はここを通します。
//
// ⚠ **順序がこの関門の全部です。** 畳めないIDで権限を引くと「読めない」と
// 「存在しない」の区別が漏れ、編集中の判定を write より先にすると、書けない人に
// 「誰が開いているか」を教えることになります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"

	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// normalizePageIDOrFail はページIDを6桁へ畳みます。畳めなければ 400 を返して false。
func normalizePageIDOrFail(w http.ResponseWriter, raw string) (string, bool) {
	pageID, ok := page.NormalizeID(raw)
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return "", false
	}
	return pageID, true
}

// requireWritableIdle は「そのページへ write できて、誰も編集していない」を確かめます。
//
// ⚠ **機械が既存ページの本文を書き換えるときの関門**です——`RewriteBody` は読んで・
// 変えて・書くので、エディタが開いているとオートセーブと黙って上書きし合います。
// `RequireEditLock` は使えません（一覧の画面はトークンを持てず必ず 409 になる・
// 2026-09-14 の決定）。⚠ **自分が開いていても断ります。**
func requireWritableIdle(w http.ResponseWriter, r *http.Request, pageID string) bool {
	if !page.RequirePageWrite(w, r, pageID) {
		return false
	}
	return editlock.RefuseWhileEditing(w, pageID)
}

// gateWritablePage は上の2つを続けて通し、通れば6桁のページIDを返します。
func gateWritablePage(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	pageID, ok := normalizePageIDOrFail(w, raw)
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
