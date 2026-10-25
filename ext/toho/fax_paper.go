package toho

import (
	"net/http"
	"os"
	"strings"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// FAX で送る紙（2026-10-25 夜・ext/comm/fax_send.go の FaxPrepare）。
//
// メールでは、外注加工の資料を部材発注書・部材見積依頼書とは別の添付で渡せます。FAX は1本の紙なので、資料のうち紙に
// できるもの（PDF・画像）を、紙のうしろに綴じて送ります（「📠 FAX・印刷用（資料を綴じる）」と同じ1本・bindAndSave）。
// 送ったと記録するのは、綴じる前の紙です（部材発注書の「送った部材発注書」・7年残す紙）。綴じられる資料が無ければ、紙だけを送る。

// faxPaperWithDocs は、メールの Prepare（prepare）で紙を用意し、k の決まりで資料を綴じた1本を送る FaxPrepare を返します。
func faxPaperWithDocs(prepare func(http.ResponseWriter, *http.Request, string) ([]comm.ComposeAttachment, bool), k boundKind) func(http.ResponseWriter, *http.Request, string) (comm.ComposeAttachment, comm.ComposeAttachment, bool) {
	return func(w http.ResponseWriter, r *http.Request, pageID string) (comm.ComposeAttachment, comm.ComposeAttachment, bool) {
		atts, ok := prepare(w, r, pageID)
		if !ok {
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		if len(atts) == 0 {
			cms.JSONFail(w, http.StatusInternalServerError, "FAX で送る紙（PDF）を用意できませんでした")
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		paper := atts[0]
		user := auth.CurrentUser(r)
		body, err := cms.ReadPageBody(pageID)
		if err != nil {
			cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		rows, err := k.rows(body)
		if err != nil {
			cms.JSONFail(w, http.StatusBadRequest, err.Error())
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		if docs, _ := orderDocs(user, rows); !hasPrintableDoc(docs) {
			return paper, paper, true // 綴じる資料が無い——紙だけを送る
		}
		path, found := page.AttachmentPath(pageID, paper.File)
		content, err := os.ReadFile(path)
		if !found || err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "FAX で送る紙（PDF）を読めませんでした")
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		// ⚠ 綴じられなければ送らない（図面の無い FAX が届くと、相手は加工できない）。
		b, err := bindAndSave(user, pageID, rows, content, k.name(rows, pageID), k.audit+".fax")
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "資料を綴じられなかったので、送っていません: "+err.Error())
			return comm.ComposeAttachment{}, comm.ComposeAttachment{}, false
		}
		send := comm.ComposeAttachment{PageID: pageID, File: b.File, Name: strings.TrimSuffix(paper.Name, ".pdf") + "＋資料.pdf", Checked: true}
		return paper, send, true
	}
}
