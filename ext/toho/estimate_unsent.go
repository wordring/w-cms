package toho

// ─────────────────────────────────────────────────────────────────────────
// 未送付の見積書（2026-10-03）
//
// 利用者:「見積フォルダで見積書を創ったら送付するか、必要ないと記すまで、未送付としてフォルダにいて欲しい」。
//
// ⚠ **鏡です**（本文には空のマーカーだけ・発注フォルダの「未発注の発注書」と同じ形）——索引から毎回数えるので、見積書ページを
// 消せば一覧からも消える。並ぶのは見積書ページ（`見積番号` のタグを持つ・テンプレートは除く）のうち、
// `送付日` も `送付不要` も無いもの。抜ける道は3つ:
//
//   - ✉️ メールで送る（見積書ページの足元）——送れたら機械が `送付日` を書く（estimate_mail.go）
//   - 「送った（FAX・手渡し）」——`送付日` に今日
//   - 「送付不要」——`送付不要` に今日（いつ決めたかが残る）
//
// 間違えて押したら、見積書ページのそのタグを消せば一覧に戻る（記録は版に残る）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"sort"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// EstimateUnsentViewType は「未送付の見積書」の形式名です。
const EstimateUnsentViewType = "estimate-unsent"

// EstimateNoSendTag は「この見積書は送らない」と決めた日のタグです。
const EstimateNoSendTag = "送付不要"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type: EstimateUnsentViewType, DisplayName: "未送付の見積書", Category: "ビュー", Icon: "📮", Element: "section",
		View: true,
	})
	cms.RegisterView(EstimateUnsentViewType, estimateUnsentViewHTML)
}

// unsentEstimate は未送付の見積書ページ1枚です。
type unsentEstimate struct {
	PageID              int
	Title, Client, Date string
}

// unsentEstimates は、閲覧者に見える見積書ページのうち、送付日も送付不要も無いものを古い順に返します。
func unsentEstimates(user *auth.User) ([]unsentEstimate, error) {
	db := database.DB
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない・:memory: の罠）。
	rows, err := db.Query(`SELECT DISTINCT page_id FROM page_tags WHERE name = ?`, EstimateNoTag)
	if err != nil {
		return nil, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	sort.Ints(ids)
	canView := viewCheck(user)
	var out []unsentEstimate
	for _, id := range ids {
		if !canView(id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		tags, err := cms.TagsOfPage(db, id)
		if err != nil {
			continue
		}
		if strings.TrimSpace(cms.FirstTag(tags, EstimateSentTag)) != "" || strings.TrimSpace(cms.FirstTag(tags, EstimateNoSendTag)) != "" {
			continue
		}
		out = append(out, unsentEstimate{PageID: id, Title: cms.PageTitleByID(id),
			Client: cms.FirstTag(tags, EstimateClientTag), Date: cms.FirstTag(tags, EstimateDateTag)})
	}
	return out, nil
}

// estimateUnsentViewHTML は「未送付の見積書」の一覧を描きます。
func estimateUnsentViewHTML(user *auth.User, _ int) string {
	head := `<h3 class="materials-title">📮 未送付の見積書</h3>`
	list, err := unsentEstimates(user)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「全部送った」と「数えていない」を見分けるため。
		return head + `<p class="materials-empty">未送付の見積書はありません` +
			`（見積書ページを作ると、送るか「送付不要」にするまでここに並びます）。</p>`
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`<p class="unorder-help">メールで送ると自動で外れます。FAX・手渡しで送ったら「送った」、送らないなら「送付不要」を` +
		`押してください（見積書ページに「` + EstimateSentTag + `」か「` + EstimateNoSendTag + `」のタグで今日の日付が入ります——` +
		`間違えたらそのタグを消せば戻ります）。</p>`)
	b.WriteString(`<table class="materials-table estimate-unsent-table"><thead><tr>` +
		`<th>見積書</th><th>見積先</th><th>見積日</th><th></th></tr></thead><tbody>`)
	for _, e := range list {
		id := page.FormatID(e.PageID)
		title := e.Title
		if title == "" {
			title = id
		}
		b.WriteString(`<tr><td><a href="/` + id + `">` + stdhtml.EscapeString(title) + `</a></td>` +
			`<td>` + stdhtml.EscapeString(e.Client) + `</td>` +
			`<td>` + stdhtml.EscapeString(e.Date) + `</td>` +
			`<td class="estimate-unsent-ops">` +
			`<button type="button" data-est-mark="sent" data-est-page="` + id + `">送った（FAX・手渡し）</button> ` +
			`<button type="button" data-est-mark="nosend" data-est-page="` + id + `">送付不要</button>` +
			`</td></tr>`)
	}
	b.WriteString(`</tbody></table><div class="unorder-result" data-est-mark-result="1"></div>`)
	return b.String()
}

// EstimateMarkAPIHandler は POST /api/estimate/mark です。入力: {page_id, mark: "sent"|"nosend"}——
// 見積書ページに `送付日`（FAX・手渡しで送った）か `送付不要` を今日の日付で書きます（未送付の一覧から外れる）。
func EstimateMarkAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Mark   string `json:"mark"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	tag, how := "", ""
	switch req.Mark {
	case "sent":
		tag, how = EstimateSentTag, "FAX・手渡し"
	case "nosend":
		tag, how = EstimateNoSendTag, "送付不要"
	default:
		cms.JSONFail(w, http.StatusBadRequest, "印が違います（sent か nosend）")
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if cms.IsTemplateArea(pageID) || cms.PageTagValue(database.DB, pageNum(pageID), EstimateNoTag) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "見積書ページではありません")
		return
	}
	today := time.Now().Format("2006-01-02")
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		return withTagValues(cur, tag, []string{today}, true)
	}) {
		return
	}
	auth.Audit(user.Username, "estimate."+req.Mark, pageID+" "+how)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "tag": tag, "date": today})
}
