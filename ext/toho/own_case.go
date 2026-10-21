package toho

// ─────────────────────────────────────────────────────────────────────────
// 自社案件（2026-10-21・【考察】製造単位の部材発注 §6.6 の11番）
//
//	2026-10-21 利用者（§4 問い6「案件に結び付かない部材」に）:「自社案件として、案件ページに追加するようにしては？通信箱の手書きメモからスタートするのが良いかもしれませんね」
//
//   - 消耗品・工具・社内で使う物など、客先の注文に結び付かない部材も、自社を発注元とする案件（`ページ種類：自社案件`）にします。
//     あとは、ほかの案件と同じく、案件残表 → 製造待ち → 製造ページ → 発注部材表 → 部材発注書、です。
//   - 始まりは通信箱の記録（手書きのメモ・「＋ 記録する」）。その記録の「作るページ」に「自社案件ページ」。
//     題は「【自社】案件　<記録の題>」、発注元は作った人の署名の組織（連絡帳の自社のページ）、受信元はその記録。
//     置き場は `記録／年／月／依頼`（作った日）。案件明細表は空のまま——弊社品番・品名・数量は人が書きます。
//   - 自社案件は売上ではないので、案件ページの足元に受注残高・赤字・検算は出しません。納品書も作りません（delivery.go は顧客発注書だけ）。
//     全部納めることが無いので、製造ページは手で閉じます（「📁 この製造を閉じる」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailOwnCaseKind は通信の記録から作るページの種類「自社案件ページ」です。
const MailOwnCaseKind = "自社案件ページ"

func init() {
	comm.RegisterRecordMaker(comm.RecordMaker{
		// 受信の記録と、向きの無い記録（手書きのメモ・「＋ 記録する」）に出す。送った控えには出さない。
		Name: MailOwnCaseKind, Order: 16, Directions: []string{"受信", ""},
		Hint: "自社を発注元とする案件ページを作ります（消耗品・工具・社内用など・記録／年／月／依頼）。弊社品番・品名・数量は、できたページの案件明細表に書きます",
		Make: makeOwnCaseFromRecord,
		Made: func(user *auth.User, pageID string) []comm.MadePage {
			var out []comm.MadePage
			for _, id := range casesFromRecord(user, pageID, PageKindOwnCase) {
				out = append(out, comm.MadePage{PageID: id, Title: pageTitleOf(id), Kind: "自社案件"})
			}
			return out
		},
	})
}

// ownCompanyTitle は user の既定の署名の人が属する組織（連絡帳の自社のページ）の題です（無ければ空）。
func ownCompanyTitle(user *auth.User) string {
	s, ok := DefaultSigner(user, Signers(user))
	if !ok {
		return ""
	}
	org, _, ok := contacts.PartnerOfPage(s.PageID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(cms.PageTitleByID(org))
}

// buildOwnCaseHTML は自社案件ページの本文を、テンプレート「案件ページ」を写して組みます。
func buildOwnCaseHTML(tmpl, subject, client, source string) string {
	d := cms.NewPageDraft(OrderPageTemplate, tmpl)
	title := "【自社】案件"
	if s := strings.TrimSpace(subject); s != "" {
		title += "　" + s
	}
	d.SetTitle(title)
	if client != "" {
		setHeaderTag(d.DraftBlock, OrderClientTag, client)
	}
	if source != "" {
		setHeaderTag(d.DraftBlock, SourceRefTag, source)
	}
	d.SetTag(PageKindTag, PageKindOwnCase)
	return d.HTML()
}

// makeOwnCaseFromRecord は通信の記録から自社案件ページを作ります（comm の口 POST /api/record-make から）。
func makeOwnCaseFromRecord(user *auth.User, pageID string) (comm.MakeResult, error) {
	tmpl, err := cms.PageTemplateBody(OrderPageTemplate)
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: 409, Message: "案件ページのテンプレートがありません: " + err.Error()}
	}
	body := buildOwnCaseHTML(tmpl, pageTitleOf(pageID), ownCompanyTitle(user), pageID)
	folder, err := recordFolderOr("", user, RecordRequestFolder, time.Now())
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: createFailStatus(err, http.StatusInternalServerError), Message: err.Error()}
	}
	newID, err := cms.CreateChildPage(folder, user.Username, body)
	if err != nil {
		return comm.MakeResult{}, errors.New("自社案件ページを作れません: " + err.Error())
	}
	auth.Audit(user.Username, "own-case.new", newID+" from "+pageID)
	return comm.MakeResult{Pages: []comm.MadePage{{PageID: newID, Title: pageTitleOf(newID), Kind: "自社案件"}},
		Say: "自社案件ページを作りました。案件明細表に弊社品番・品名・数量を書いてください。", Open: newID}, nil
}

// casesFromRecord は、その記録から作った種類 kind の案件ページ（受信元がその記録）を返します（読めるものだけ）。
func casesFromRecord(user *auth.User, pageID, kind string) []string {
	ids, err := cms.PagesByTag(database.DB, SourceRefTag, pageID)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if page.CanView(user, id) && pageKindOf(id) == kind {
			out = append(out, page.FormatID(id))
		}
	}
	return out
}
