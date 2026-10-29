package toho

// 逃げ道（2026-10-21・【考察】製造単位の部材発注 §6.4）。
//
// 2026-10-21 利用者:「それで良いのですが、常に逃げ道は必要で、送った部材発注書を送っていなかったことにしたり、加工をミスったので追加で部材を発注するような仕組みも必要です」
// 2026-10-21 利用者:「メールで送った部材発注書も、未送付に戻してよいです。w-cmsはメモを残すことが出来るので、発注済み文書にリンクし取り消したことを書いておけます。」
// 2026-10-21 利用者:「追加の発注の行は、備考に「追加」と書く形でよいです」
//
//   - 送ったことを取り消す（8番）: 部材発注書の「↩ 未送付に戻す」で、発注済の行を未発注に戻します（メールで送ったものも）。
//     戻したことは監査に残ります。なぜ戻したかは、人がメモに書きます（取り消した部材発注書へのリンクを添えて）。
//     納品済の行があれば戻しません（物が届いている）。戻したあとは、また相乗りできます。
//   - 追加の発注（9番）: 製造ページの「➕ 追加の発注」で、この製造の部材を、手配済みを引かずに発注部材表へ出します（備考は「追加」）。
//     加工の失敗などで作り直すぶんです。備考が「追加」の行は、手配済みの数え方で数えません（procure_ledger.go の rowSupplies）——
//     数えると、作り直しに使うぶんが余りとして、ほかの案件の手当てに回ってしまうからです。人は要らない行を「↩ 外す」で外し、数を直します。

import (
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ExtraNote は追加の発注の行の備考です（この備考の行は手配済みに数えない）。
const ExtraNote = "追加"

// isExtraLine は備考が「追加」の行かです（「追加 …」と書き足してあってもよい）。
func isExtraLine(note string) bool {
	return strings.HasPrefix(strings.TrimSpace(note), ExtraNote)
}

// OrderUnsendAPIHandler は POST /api/our-order/unsend です。入力: {page_id}——部材発注書の発注済の行を未発注に戻します。
func OrderUnsendAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, pageID, ok := gatePagePost(w, r)
	if !ok {
		return
	}
	if pageKindOf(pageNum(pageID)) != PageKindOurPurchase {
		cms.JSONFail(w, http.StatusBadRequest, "部材発注書ではありません")
		return
	}
	rows, _ := cms.VocabTableRowsOf(database.DB, pageNum(pageID), ourOrderItemsType)
	for _, row := range rows {
		if strings.TrimSpace(row.Values["status"]) == OrderLineDelivered {
			cms.JSONFail(w, http.StatusConflict, "納品済の行があるので戻せません（届いた行は、行ごとに状態を直してください）")
			return
		}
	}
	changed := 0
	delivered := false
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, n, changes := setOrderLineStatusTracked(cur, 0, OrderLineUnsent)
		// 納品済の行は、錠の中のいまの本文でも確かめる（2026-10-26——上の確かめは索引で、錠の外。あいだに納品済になった行を
		// 未発注へ戻し、在庫を戻さず、届いた記録を消した）。
		for _, c := range changes {
			if strings.TrimSpace(c.from) == OrderLineDelivered {
				delivered = true
				return cur
			}
		}
		changed = n
		return out
	}) {
		return
	}
	if delivered {
		cms.JSONFail(w, http.StatusConflict, "納品済の行があるので戻せません（届いた行は、行ごとに状態を直してください）")
		return
	}
	auth.Audit(user.Username, "our-order.unsend", pageID+" "+strconv.Itoa(changed)+"行を未発注に戻した")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "rows": changed})
}

// unsendButtonHTML は部材発注書の送る欄の「↩ 未送付に戻す」です（送った行があるときだけ）。
func unsendButtonHTML(c OrderSendCounts) string {
	if c.Sent == 0 {
		return ""
	}
	return `<button type="button" class="chip-btn" data-order-unsend="1"` +
		` title="送ったことを取り消して、発注済の行を未発注に戻します（メールで送ったものも）。なぜ戻したかは、メモに書いてください">↩ 未送付に戻す</button>`
}

// mfgExtraLines は製造ページ mfgID の製造明細の行ごとに、その加工製品の部材を、手配済みを引かずに全部集めます（備考は「追加」）。
// sel が nil でなければ、選んだ行（0始まり）だけ（2026-10-21 夕）。
func mfgExtraLines(user *auth.User, mfgID int, sel map[int]bool) (lines []ourOrderLine, skipped int, err error) {
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		return nil, 0, err
	}
	mfg := page.FormatID(mfgID)
	for i, r := range rows {
		if sel != nil && !sel[i] {
			continue
		}
		pid, ok := refPageID(r.Values["our-item-id"])
		order, _ := refPageID(r.Values["order"])
		qty, okQ := estimateNumber(r.Values["quantity"])
		if !ok || !okQ || qty <= 0 || !page.CanView(user, pageNum(pid)) {
			skipped++
			continue
		}
		for _, it := range productNeeds(database.DB, pageNum(pid), int(qty)) {
			v := it.Values
			lines = append(lines, ourOrderLine{
				ProductID: pid, ForOrder: order, Mfg: mfg, Kind: it.Kind, No: v["番号"], ItemID: v["品番"], ItemName: v["品名"], WorkPartNo: v["加工部品番号"], WorkPartName: v["加工部品名"], Work: v["加工内容"],
				Material: v["材質"], Shape: v["形状"], Size: v["寸法"], Color: v["表面"], Spec: v["仕様"], Supplied: v["支給"],
				Quantity: strconv.Itoa(it.Required), Vendor: strings.TrimSpace(it.Vendor), Note: ExtraNote, Lot: fmtQty(qty),
			})
		}
	}
	return lines, skipped, nil
}

// （「➕ 追加の発注」の API /api/mfg/extra は 2026-10-21 夕に /api/mfg/parts の extra へ寄せた——追加の行は必要部材表へ出す・mfg_work.go。）
