package toho

// ─────────────────────────────────────────────────────────────────────────
// メールの本文から受注ページを作る（2026-10-01）
//
// 利用者:「発注書が無くても、メールから簡単に発注ページを作れませんか？」→（どちらのページかを聞いて）「受注ページ」。
// 客先がメールの本文だけで注文してくる（発注書の PDF が付かない）ことがあり、🤖解析は添付（PDF・Excel）しか
// 読めませんでした。メールの記録のページの「🤖 本文から受注ページ」を押すと、件名・差出人・日付・本文を Gemini へ
// 渡し、添付の解析と同じ形（orderJudgment）で受け取って、同じテンプレートから受注ページを作ります。
//
//   - 受注ページは**メールの記録の子**として生まれます（添付の解析と同じ・整理で `受注／年／月` へ）。
//   - `受信元` はメールのページ（ページ全体への参照）。原本の PDF の枠は作りません（`DropFileView`）。
//   - 発注元が本文から読めなければ、差出人のアドレスから連絡帳の取引先を引きます。
//   - ⚠ **受注かどうかを決めるのは人**です——押すのは「ハッキリと注文だ」と分かったメールだけ（解析は人が押したときだけ）。
//     Gemini が「注文ではない」と答えたら作りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// judgeOrderMail はメールの判定の入口です（試験が偽物へ差し替えられるよう変数）。
var judgeOrderMail = judgeOrderMailWithGemini

// mailPreamble はメールを渡すときに頼み文の頭へ足す断りです。
const mailPreamble = `（ここで渡すのはPDFではなく、メールの件名・差出人・日付・本文です。下の「このPDF」「書面」はこのメールのことと読んでください。
- 品物と数量が書かれていて、当社に作って（納めて）ほしいという注文の意思が読み取れるメールを "order" とします。
  見積もりの依頼・問い合わせ・お礼・日程の連絡などは "other" です。
- 「>」で始まる行は前のやり取りの引用です。注文の明細は、このメールで新しく頼んでいるものだけを入れてください。
- 発注書番号は、本文に注文番号として書かれているときだけ入れてください（無ければ空文字）。
- 発注日が本文に書かれていなければ、メールの日付を YYYY-MM-DD で入れてください。
- 発行元（customer）は、本文や署名に書かれている会社名です（書かれていなければ空文字）。
- source_table には、本文に書かれている明細を表にして入れてください（見出しは本文の言葉で）。
- 小計・消費税・合計は、本文に書かれているときだけ入れてください。）

`

func judgeOrderMailWithGemini(text string) (*orderJudgment, error) {
	respText, err := cms.GeminiGenerateBlobs(mailPreamble+orderJudgePrompt,
		genai.Blob{MIMEType: "text/plain", Data: []byte(text)})
	if err != nil {
		return nil, err
	}
	return parseOrderJudgment(respText)
}

// mailTextMaxRunes は Gemini へ渡す本文の上限です（長い引用の鎖で膨らんだメールを切る）。
const mailTextMaxRunes = 30000

// mailForJudgment はメールの記録を、Gemini へ渡す文字にします（件名・差出人・日付・本文）。
func mailForJudgment(pageIDInt int, title, body string) string {
	tags, _ := cms.TagsOfPage(database.DB, pageIDInt)
	date := cms.FirstTag(tags, comm.ReceivedAtTag)
	if date == "" {
		date = cms.FirstTag(tags, comm.SentAtTag)
	}
	text := comm.RecordBodyText(body)
	if r := []rune(text); len(r) > mailTextMaxRunes {
		text = string(r[:mailTextMaxRunes]) + "\n（以下略）"
	}
	return "件名: " + title + "\n差出人: " + cms.FirstTag(tags, comm.FromTag) + "\n日付: " + date + "\n\n" + text
}

// MailOrderKind はメールの記録から作るページの種類「受注ページ」です（2026-10-01・comm の選ぶ欄に出る）。
const MailOrderKind = "受注ページ"

func init() {
	// 利用者:「「受注ページ」「加工製品ページ」をコンボボックスで選択して「作成」ボタンを押せばいいかも」（2026-10-01）。
	comm.RegisterRecordMaker(comm.RecordMaker{
		Name: MailOrderKind, Order: 10, Directions: []string{"受信"},
		Hint: "🤖 本文を読んで受注ページを作ります（発注書の PDF が付いていない注文のメール）",
		Make: makeOrderFromMail,
		Made: func(user *auth.User, pageID string) []comm.MadePage {
			var out []comm.MadePage
			for _, r := range mailOrderPages(user, pageID) {
				out = append(out, comm.MadePage{PageID: r.PageID, Title: r.Title, Kind: r.Kind})
			}
			return out
		},
	})
}

// makeOrderFromMail はメールの記録の本文を読んで受注ページを作ります（comm の口 POST /api/record-make から）。
// 権限（write）と、記録であること（`チャネル` のタグ）は口が先に確かめている。
func makeOrderFromMail(user *auth.User, pageID string) (comm.MakeResult, error) {
	idInt, _ := strconv.Atoi(pageID)
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.MakeResult{}, errors.New("本文を読めません: " + err.Error())
	}
	if strings.TrimSpace(comm.RecordBodyText(body)) == "" {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusBadRequest, Message: "このメールには本文がありません"}
	}
	// テンプレートは**聞く前に**確かめる（聞いたあとで無いと分かると、有料の問い合わせが無駄になる）。
	orderTmpl, err := cms.PageTemplateBody(OrderPageTemplate)
	if err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusConflict, Message: "解析の結果を書くテンプレートがありません: " + err.Error()}
	}
	j, err := judgeOrderMail(mailForJudgment(idInt, cms.PageTitleByID(idInt), body))
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			return comm.MakeResult{}, &comm.MakeError{Status: http.StatusServiceUnavailable,
				Message: "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。"}
		}
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusBadGateway, Message: "解析に失敗しました: " + err.Error()}
	}
	if !j.IsClientOrder {
		return comm.MakeResult{Say: "注文のメールではないと判定されました（ページは作っていません）。"}, nil
	}
	sheets := j.orderList()
	// 発注元は本文の会社名を揃えて（連絡帳の題へ）、読めなければ差出人のアドレスから取引先を引く。
	partner := ""
	if addr := recordSenderAddress(idInt); addr != "" {
		partner, _ = contacts.PartnerTitleForAddress(user, addr)
	}
	bodies := make([]string, 0, len(sheets))
	for _, o := range sheets {
		o.Customer = contacts.OrgNameForPage(user, o.Customer)
		if strings.TrimSpace(o.Customer) == "" {
			o.Customer = partner
		}
		b, err := buildOrderPageHTML(orderTmpl, pageID, "", o)
		if err != nil {
			return comm.MakeResult{}, &comm.MakeError{Status: http.StatusConflict, Message: "受注ページを作れません: " + err.Error()}
		}
		bodies = append(bodies, b)
	}
	var res comm.MakeResult
	linked := 0
	for _, b := range bodies {
		newID, err := cms.CreateChildPage(pageID, user.Username, b)
		if err != nil {
			return res, errors.New("受注ページを作れません（" + strconv.Itoa(len(res.Pages)) + "枚目まで作成済み）: " + err.Error())
		}
		auth.Audit(user.Username, "analyze-mail", newID+" from "+pageID)
		linked += LinkProductsToOrder(user, newID)
		res.Pages = append(res.Pages, comm.MadePage{PageID: newID, Title: pageTitleOf(newID), Kind: "受注"})
	}
	if linked > 0 {
		res.Say = "加工製品と " + strconv.Itoa(linked) + " 行を結びました。"
	}
	return res, nil
}

// mailOrderPages は、メールの記録の本文から作った受注ページ（`受信元` がそのページ全体）を返します（読めるものだけ）。
func mailOrderPages(user *auth.User, pageID string) []analyzedResult {
	rows, err := database.DB.Query(`SELECT page_id FROM page_tags WHERE name = ? AND value = ?`, SourceRefTag, pageID)
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []analyzedResult
	for _, id := range ids {
		if !page.CanView(user, id) || kindOfPage(id) != "受注" {
			continue
		}
		out = append(out, analyzedResult{Kind: "受注", PageID: formatID(id), Title: pageTitleOf(formatID(id))})
	}
	return out
}
