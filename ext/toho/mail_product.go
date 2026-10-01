package toho

// ─────────────────────────────────────────────────────────────────────────
// メールの記録から加工製品ページを作る（2026-10-01）
//
// 利用者:「メールから作るボタンは「受注ページ作成」「加工製品ページ作成」「返信」ページのタイプは増える可能性があるので
// 「受注ページ」「加工製品ページ」をコンボボックスで選択して「作成」ボタンを押せばいいかも」。
// 見積の依頼には図面が付く（利用者:「見積依頼の時は図面も送ってきますので、加工製品ページを作ることが出来ます」）。
//
// 選ぶ欄（comm の `RegisterRecordMaker`）の「加工製品ページ」は、**テンプレート「加工製品」から空のページ**を
// メールの記録の子に作って、編集モードで開きます——機械は読まない（Gemini を呼ばない）:
//
//   - 図面ブロックの `客先` は差出人のアドレスから引いた連絡帳の取引先、`受信元` はメールのページ全体。
//   - 図面の枠（ファイル表示）は空のまま——メールの添付の ID を写して貼れば開く（DXF・STEP の図面でも）。
//   - 改訂明細の1版目（受領日は今日）は解析と同じに書く（版の数え方を揃える）。
//   - 図面名称を書けば整理に出る（整理が拾うのは図面番号か図面名称のあるページ）。
//   - 図面の PDF が付いていれば、添付の「🤖 解析」でも作れる（あちらは図面を読んで埋める）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailProductKind はメールの記録から作るページの種類「加工製品ページ」です。
const MailProductKind = "加工製品ページ"

// blankProductTitle はメールから作る空の加工製品ページの題です（人が書き替える）。
const blankProductTitle = "新しい加工製品"

func init() {
	comm.RegisterRecordMaker(comm.RecordMaker{
		Name: MailProductKind, Order: 20, Directions: []string{"受信"},
		Hint: "空の加工製品ページをこのメールの子に作って開きます（客先は差出人の取引先・図面はメールの添付の ID を貼る）",
		Make: makeProductFromMail,
		Made: mailProductPages,
	})
}

// buildBlankProductHTML はテンプレート「加工製品」から空の加工製品ページを組みます。
func buildBlankProductHTML(tmpl, hostPageID, customer string) (string, error) {
	d := cms.NewPageDraft(ProductTemplate, tmpl)
	d.SetTitle(blankProductTitle)
	blk, err := d.RequireContainer(drawingHeading)
	if err != nil {
		return "", err
	}
	d.AssignBlockID(blk.Node())
	setHeaderTag(blk, ClientNameTag, cms.NormalizeNameForIngest(customer))
	setHeaderTag(blk, SourceRefTag, hostPageID)
	if err := fillFirstRevision(d, ""); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// makeProductFromMail はメールの記録の子に空の加工製品ページを作ります（comm の口 POST /api/record-make から）。
func makeProductFromMail(user *auth.User, pageID string) (comm.MakeResult, error) {
	tmpl, err := cms.PageTemplateBody(ProductTemplate)
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusConflict, Message: "加工製品ページのテンプレートがありません: " + err.Error()}
	}
	idInt, _ := strconv.Atoi(pageID)
	customer := ""
	if addr := recordSenderAddress(idInt); addr != "" {
		customer, _ = contacts.PartnerTitleForAddress(user, addr)
	}
	body, err := buildBlankProductHTML(tmpl, pageID, customer)
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusConflict, Message: "加工製品ページを組めません: " + err.Error()}
	}
	newID, err := cms.CreateChildPage(pageID, user.Username, body)
	if err != nil {
		return comm.MakeResult{}, err
	}
	auth.Audit(user.Username, "mail-product", newID+" from "+pageID)
	say := "題・図面名称・品番を書いてください（図面名称を書くと整理に出ます）。図面はメールの添付の ID を写して図面の枠へ貼ると開きます。"
	if customer == "" {
		say += " ⚠ 差出人が連絡帳に無いので、客先は空です。"
	}
	return comm.MakeResult{
		Pages: []comm.MadePage{{PageID: newID, Title: blankProductTitle, Kind: "加工製品"}},
		Say:   say, Open: newID,
	}, nil
}

// mailProductPages は、このメールの記録から作った加工製品ページ（`受信元` がメールのページ全体で、受注ページでないもの）です。
// 添付の解析が作るページの `受信元` は「ページ-添付」なので混ざらない。
func mailProductPages(user *auth.User, pageID string) []comm.MadePage {
	ids, err := cms.PagesByTag(database.DB, SourceRefTag, pageID)
	if err != nil {
		return nil
	}
	var out []comm.MadePage
	for _, id := range ids {
		if !page.CanView(user, id) {
			continue
		}
		tags, err := cms.TagsOfPage(database.DB, id)
		if err != nil || isOrderPageTags(tags) {
			continue
		}
		out = append(out, comm.MadePage{PageID: formatID(id), Title: pageTitleOf(formatID(id)), Kind: "加工製品"})
	}
	return out
}
