package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積書をメールで送る——送る欄（部品）の用件「見積書」（2026-10-01）
//
// 発注書の用件（order_mail.go）と同じ形——宛先は見積先担当の人のアドレス（連絡帳のその会社の人に同じ名前が居れば）、
// 居なければ会社と会社の人のアドレス全部。件名「御見積書（№ ○）」・本文に「メールの署名」。送る直前に見積書の
// PDF を作って添える（このページの添付にも残る）。送れたら見積書ページに `送付日` のタグを書く。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// EstimateMailPurpose は送る欄の用件「見積書」です。
const EstimateMailPurpose = "見積書"

// EstimateSentTag は見積書を送った日のタグです。
const EstimateSentTag = "送付日"

func init() {
	comm.RegisterSendPurpose(EstimateMailPurpose, comm.SendPurpose{
		Defaults:  estimateMailDefaults,
		Prepare:   prepareEstimateMail,
		AfterSent: afterEstimateMail,
		NeedsPage: true,
	})
}

// estimateAddresses は見積書の宛先です——見積先担当の名前の人が連絡帳のその会社に居ればその人、居なければ会社全体。
func estimateAddresses(user *auth.User, client, person string) []string {
	if person = strings.TrimSpace(person); person != "" {
		if orgID, ok := contacts.PartnerByTitle(user, client); ok {
			for _, p := range contacts.PersonsOf(user, orgID) {
				if cms.NormalizeText(p.Title) != cms.NormalizeText(person) {
					continue
				}
				var out []string
				for _, v := range mailAddressesOfPage(pageNum(p.ID)) {
					if a := comm.BareAddress(v); a != "" {
						out = append(out, a)
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
	}
	return supplierAddresses(user, client)
}

func estimateMailDefaults(user *auth.User, pageID string) (comm.ComposeDraft, error) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.ComposeDraft{}, errors.New("見積書のページを読めません: " + err.Error())
	}
	head, _, _, err := readEstimateDoc(body)
	if err != nil {
		return comm.ComposeDraft{}, err
	}
	client, person := head[EstimateClientTag], head[EstimatePersonTag]
	to := client + "\n"
	if person != "" {
		to = client + "\n" + person + " 様\n"
	}
	var b strings.Builder
	b.WriteString(to + "\nいつもお世話になっております。\n御見積書をお送りいたします。ご査収のほど、よろしくお願いいたします。\n\n")
	if sig := contacts.MySignature(user, "メールの署名"); len(sig) > 0 {
		b.WriteString(strings.Join(sig, "\n") + "\n")
	}
	d := comm.ComposeDraft{
		Purpose: EstimateMailPurpose, PageID: pageID,
		To:       estimateAddresses(user, client, person),
		Subject:  "御見積書（№ " + head[EstimateNoTag] + "）",
		Body:     b.String(),
		SendNote: "印の付いた見積書のPDFは送るときに作って添付し（このページの添付にも残ります）、送れたら「" + EstimateSentTag + "」のタグに今日の日付を書きます。",
		// 送るときに作る PDF を添付の欄に印つきで並べる（2026-10-03 利用者:「見積書PDFも他と同じように表示し、ただし最初から
		// 添付に入っているように」）。作るのは送るとき（いつも最新の明細で）——印を外せば作らない。
		Generated: "御見積書 " + pageID + ".pdf",
		Reload:   true,
	}
	if len(d.To) == 0 {
		d.Notes = append(d.Notes, "⚠ 「"+client+"」の連絡先が連絡帳にありません（題が一致する組織ページか、その会社の人のページに「"+
			contacts.EmailTag+"」のタグを付けると、ここに出ます）。")
	}
	return d, nil
}

func prepareEstimateMail(w http.ResponseWriter, r *http.Request, pageID string) ([]comm.ComposeAttachment, bool) {
	user := auth.CurrentUser(r)
	if !requireWritableIdle(w, r, pageID) {
		return nil, false
	}
	made, ok := makeEstimatePDF(w, user, pageID)
	if !ok {
		return nil, false
	}
	return []comm.ComposeAttachment{{PageID: pageID, File: made.File, Name: "御見積書 " + pageID + ".pdf", Checked: true}}, true
}

func afterEstimateMail(user *auth.User, pageID, _ string) error {
	n, err := strconv.Atoi(pageID)
	if err != nil {
		return errors.New("見積書のページIDが不正です")
	}
	if !page.GetPerms(n).CanWrite(user) || cms.IsTemplateArea(pageID) {
		return errors.New("送付日を書けませんでした（このページを書き換える権限がありません）")
	}
	if holder, open := editlock.Locks.EditorOpen(n); open {
		return errors.New("送付日を書けませんでした（このページは編集中です（" + holder + "））")
	}
	today := time.Now().Format("2006-01-02")
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		return withTagValues(cur, EstimateSentTag, []string{today}, true)
	}); err != nil {
		return errors.New("送付日を書けませんでした: " + err.Error())
	}
	auth.Audit(user.Username, "estimate.sent", pageID+" mail")
	return nil
}
