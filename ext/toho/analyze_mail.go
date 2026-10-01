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
	"encoding/json"
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

// AnalyzeMailOrderAPIHandler は POST /api/analyze-mail-order です。入力: {page_id}——メールの記録のページ。
func AnalyzeMailOrderAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	// 子ページを作る操作なので write 権限（本文は変えないので編集ロックは不要——添付の解析と同じ理屈）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	idInt, _ := strconv.Atoi(pageID)
	if cms.PageTagValue(database.DB, idInt, comm.ChannelTag) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "メールの記録ではありません")
		return
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を読めません: "+err.Error())
		return
	}
	if strings.TrimSpace(comm.RecordBodyText(body)) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "このメールには本文がありません")
		return
	}
	// テンプレートは**聞く前に**確かめる（聞いたあとで無いと分かると、有料の問い合わせが無駄になる）。
	orderTmpl, err := cms.PageTemplateBody(OrderPageTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "解析の結果を書くテンプレートがありません: "+err.Error())
		return
	}
	j, err := judgeOrderMail(mailForJudgment(idInt, cms.PageTitleByID(idInt), body))
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, "解析に失敗しました: "+err.Error())
		return
	}
	if !j.IsClientOrder {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "is_client_order": false})
		return
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
			cms.JSONFail(w, http.StatusConflict, "受注ページを作れません: "+err.Error())
			return
		}
		bodies = append(bodies, b)
	}
	made := []map[string]any{}
	linked := 0
	for _, b := range bodies {
		newID, err := cms.CreateChildPage(pageID, user.Username, b)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError,
				"受注ページを作れません（"+strconv.Itoa(len(made))+"枚目まで作成済み）: "+err.Error())
			return
		}
		auth.Audit(user.Username, "analyze-mail", newID+" from "+pageID)
		linked += LinkProductsToOrder(user, newID)
		made = append(made, map[string]any{"page_id": newID, "title": pageTitleOf(newID)})
	}
	out := map[string]any{
		"success": true, "is_client_order": true,
		"page_id": made[0]["page_id"], "title": made[0]["title"], "linked_items": linked,
	}
	if len(made) > 1 {
		out["pages"] = made
	}
	json.NewEncoder(w).Encode(out)
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
