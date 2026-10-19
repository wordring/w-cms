package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページを記録へ移す（2026-10-19・【考察】製造ページ・作る順の5番）
//
//	2026-10-19 利用者（§7 の答え）: 製造ページは、請求書の発行の時点で 記録／年／月 へ移す。
//	（5番の案に）「その三つはそのままで良いです」——
//	  1. 請求書を発行したとき、全部の行を納め終えて請求も済んだ製造ページだけを移す
//	  2. 移す先は `記録／年／月／製造記録`（年月は請求書の請求日）
//	  3. 手でも閉じられる（「この製造を閉じる」——見積だけで終わった製造は請求書が無い）
//
//   - 「済んだ」は、製造明細の行ごとに、この製造ページの子の納品書（`ページ種類：弊社納品書`）で数量ぶん納めていて、
//     その納品書の行が全部どれかの請求書に載っていること（deliveredUnder・billedKeys）。
//   - 移すのは、トップ直下の「製造」の直下にある製造ページだけ（もう閉じたものは動かさない）。書けない製造ページは飛ばす。
//   - 移しても、製造ページの中身・子の書類はそのまま（案件残表の「製造中」のリンクも、ページ番号は変わらないので切れない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// mfgIsOpen は、製造ページ mfgID がトップ直下の「製造」の直下にあるか（まだ閉じていないか）です。
func mfgIsOpen(mfgID int) bool {
	box, ok := cms.TopLevelPageByTitle(MfgBoxTitle)
	if !ok {
		return false
	}
	meta, has := page.ReadSidecar(page.FormatID(mfgID))
	return has && meta.ParentID == box
}

// mfgFinished は、製造ページ mfgID の製造明細を全部納め、その納品書の行が全部請求書に載っているかです。
func mfgFinished(mfgID int, billed map[string]bool) bool {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return false
	}
	delivered := deliveredUnder(mfgID)
	some := false
	for _, r := range rows {
		qty, ok := estimateNumber(r.Values["quantity"])
		if !ok || qty <= 0 {
			continue
		}
		some = true
		if delivered[shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])] < qty {
			return false
		}
	}
	if !some {
		return false
	}
	// 子の納品書の行が、全部どれかの請求書に載っているか。
	dl, err := cms.VocabRowsOfType(database.DB, DeliveryItemsType)
	if err != nil {
		return false
	}
	mine := map[int]bool{}
	for _, r := range dl {
		ok, seen := mine[r.PageID]
		if !seen {
			meta, has := page.ReadSidecar(page.FormatID(r.PageID))
			ok = has && meta.ParentID == page.FormatID(mfgID) && pageKindOf(r.PageID) == PageKindOurDelivery
			mine[r.PageID] = ok
		}
		if !ok {
			continue
		}
		if q, okQ := estimateNumber(r.Values["quantity"]); !okQ || q <= 0 {
			continue
		}
		if !billed[invoiceKey(page.FormatID(r.PageID), r.Values["item-id"], r.Values["item-name"])] {
			return false
		}
	}
	return true
}

// mfgPagesOfInvoice は、請求書の請求明細の行が指す納品書の親の製造ページ（重複なし・番号の順）を返します。
// rows は請求明細表の行（見出しの表示文字が鍵——itemRowsOf）。
func mfgPagesOfInvoice(rows []map[string]string) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range rows {
		dlv, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(v["納品書"]), "/"))
		if !ok {
			continue
		}
		meta, has := page.ReadSidecar(dlv)
		if !has || meta.ParentID == "" {
			continue
		}
		id := pageNum(meta.ParentID)
		if seen[id] || pageKindOf(id) != PageKindMfg {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// moveMfgToRecord は製造ページを `記録／年／月／製造記録`（年月は t）へ移します。
func moveMfgToRecord(user *auth.User, mfgID int, t time.Time) error {
	monthID, err := recordFolder(user.Username, RecordWorkFolder, t)
	if err != nil {
		return err
	}
	_, _, err = cms.SetPageParent(user, page.FormatID(mfgID), monthID)
	return err
}

// closeFinishedMfgPages は、請求書（本文 body）が指す製造ページのうち、済んだものを記録へ移します（請求書の発行のとき）。
// 年月は請求書の請求日（読めなければ today）。移したページの番号を返します。
func closeFinishedMfgPages(user *auth.User, body string, today time.Time) []int {
	head, rows, err := readInvoiceDoc(body)
	if err != nil {
		return nil
	}
	when := today
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(head[InvoiceDateTag])); err == nil {
		when = t
	}
	billed := billedKeys()
	var moved []int
	for _, id := range mfgPagesOfInvoice(rows) {
		if !mfgIsOpen(id) || !canWritePage(user, id) || !mfgFinished(id, billed) {
			continue
		}
		if err := moveMfgToRecord(user, id, when); err != nil {
			continue
		}
		auth.Audit(user.Username, "mfg.close", page.FormatID(id)+" ← 請求書の発行")
		moved = append(moved, id)
	}
	return moved
}

// MfgCloseAPIHandler は POST /api/mfg/close です。入力: {page_id}（製造ページ）。
// 製造ページを `記録／年／月／製造記録`（今日の年月）へ移します（「この製造を閉じる」——済んでいなくても閉じられる）。
func MfgCloseAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, pageID, ok := gatePagePost(w, r)
	if !ok {
		return
	}
	if !requireMfgPage(w, pageID) {
		return
	}
	id := pageNum(pageID)
	if !mfgIsOpen(id) {
		cms.JSONFail(w, http.StatusConflict, "この製造はもう閉じています（「"+MfgBoxTitle+"」の下にありません）")
		return
	}
	now := time.Now()
	if err := moveMfgToRecord(user, id, now); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "記録へ移せません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "mfg.close", pageID+" ← 手で閉じた")
	cms.WriteJSON(w, map[string]any{"success": true, "place": recordPath(RecordWorkFolder, now)})
}
