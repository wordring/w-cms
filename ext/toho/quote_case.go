package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積の依頼を案件にする（2026-10-19・【考察】製造ページ §7 の答え・作る順の4番の最初の段）
//
//	2026-10-19 利用者:「見積もりは、見積もりを受注したということにして、受注ページに並べてそこから作るのはどうですか？」
//
//   - 見積の依頼は、`ページ種類：顧客見積依頼` の案件ページにします（テンプレート「案件ページ」を写す）。案件残表に「見積」として並び、
//     そこから製造ページを始められます。まだ注文ではないので、受注残高・赤字の知らせ・必要部材表・受注の単価には入らず、納品書も作れません。
//   - 通信の記録（受信メール・電話のメモ）の「作るページ」に「見積依頼ページ」。題は「【顧客】見積依頼　<件名>」、発注元は差出人の取引先、
//     受信元はその記録。置き場は `記録／年／月／依頼`（作った日）。案件明細表は空のまま——品番・品名・数量（ロット）は人が書きます。
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
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailQuoteKind は通信の記録から作るページの種類「見積依頼ページ」です。
const MailQuoteKind = "見積依頼ページ"

func init() {
	comm.RegisterRecordMaker(comm.RecordMaker{
		Name: MailQuoteKind, Order: 15, Directions: []string{"受信"},
		Hint: "見積の依頼の案件ページを作ります（記録／年／月／依頼）。品番・品名・数量は、できたページの案件明細表に書きます",
		Make: makeQuoteCaseFromRecord,
		Made: func(user *auth.User, pageID string) []comm.MadePage {
			var out []comm.MadePage
			for _, id := range quoteCasesFrom(user, pageID) {
				out = append(out, comm.MadePage{PageID: id, Title: pageTitleOf(id), Kind: "見積"})
			}
			return out
		},
	})
}

// buildQuoteCaseHTML は見積の依頼の案件ページの本文を、テンプレート「案件ページ」を写して組みます。
func buildQuoteCaseHTML(tmpl, subject, client, source string) string {
	d := cms.NewPageDraft(OrderPageTemplate, tmpl)
	title := "見積依頼"
	if s := strings.TrimSpace(subject); s != "" {
		title += "　" + s
	}
	d.SetTitle(issuerTitle(PageKindCustomerQuote, title))
	if client != "" {
		setHeaderTag(d.DraftBlock, OrderClientTag, client)
	}
	if source != "" {
		setHeaderTag(d.DraftBlock, SourceRefTag, source)
	}
	d.SetTag(PageKindTag, PageKindCustomerQuote)
	return d.HTML()
}

// makeQuoteCaseFromRecord は通信の記録から見積の依頼の案件ページを作ります（comm の口 POST /api/record-make から）。
func makeQuoteCaseFromRecord(user *auth.User, pageID string) (comm.MakeResult, error) {
	tmpl, err := cms.PageTemplateBody(OrderPageTemplate)
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: 409, Message: "案件ページのテンプレートがありません: " + err.Error()}
	}
	idInt, _ := strconv.Atoi(pageID)
	client := ""
	if addr := recordSenderAddress(idInt); addr != "" {
		client, _ = contacts.PartnerTitleForAddress(user, addr)
	}
	body := buildQuoteCaseHTML(tmpl, pageTitleOf(pageID), client, pageID)
	folder, err := recordFolderOr("", user, RecordRequestFolder, time.Now())
	if err == nil {
		err = requireWriteTo(user, folder) // 作る先のフォルダにも書けること（ほかの書類と同じ・2026-10-26）
	}
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: createFailStatus(err, http.StatusInternalServerError), Message: err.Error()}
	}
	newID, err := cms.CreateChildPage(folder, user.Username, body)
	if err != nil {
		return comm.MakeResult{}, errors.New("見積依頼ページを作れません: " + err.Error())
	}
	auth.Audit(user.Username, "quote-case.new", newID+" from "+pageID)
	return comm.MakeResult{Pages: []comm.MadePage{{PageID: newID, Title: pageTitleOf(newID), Kind: "見積"}},
		Say: "見積依頼ページを作りました。案件明細表に品番・品名・数量を書いてください。", Open: newID}, nil
}

// quoteCasesFrom は、その記録から作った見積の依頼の案件ページ（受信元がその記録）を返します（読めるものだけ）。
func quoteCasesFrom(user *auth.User, pageID string) []string {
	ids, err := cms.PagesByTag(database.DB, SourceRefTag, pageID)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if page.CanView(user, id) && pageKindOf(id) == PageKindCustomerQuote {
			out = append(out, page.FormatID(id))
		}
	}
	return out
}
