package toho

// ─────────────────────────────────────────────────────────────────────────
// 顧客へ出す見積書——見積計算表から作る（2026-10-01）
//
// 利用者:「業者へ見積の依頼と、顧客へ見積書の発行を考えましょう」→「トップ直下に見積フォルダを作りましょう」「顧客へので
// 良いです」→（作り方を聞いて）「見積計算表から」・宛名は「会社＋担当者」。紙の欄は利用者の見本（御見積書）に合わせる
// ——日付・№・宛名（会社 担当者 様）・差出人・受渡期日・受渡場所・取引方法・有効期限・税率・明細（摘要・数量・単価・
// 金額〈税抜〉）・合計（税抜）・備考。正本は [【考察】見積の依頼と見積書.md] §3。
//
//	加工製品ページの見積計算表 ──「見積書に入れる」（客先・担当者・新しい見積書か作りかけへ足すか）
//	   → 見積書ページ（見積／年／月・テンプレート「見積書」を写す）の見積明細に1行
//	     （弊社品番・品番・品名・数量＝ロット・単位・単価＝確定単価・備考＝単価の行の備考）
//
//   - **単価はその時点の値で写して固定**します（出した紙の値は、あとで見積計算表を直しても変わらない）。
//   - **見積番号はページ番号**（発注書番号と同じ）。見積日は作った日。取引方法・有効期限は設定の既定（estimate_tags）。
//   - 見積担当（差出人）は連絡帳の署名を持つ人——初期値はログイン名と同じ題の人（発注書と同じ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 見積書ページの言葉。
const (
	// EstimateTemplate は見積書ページを作るテンプレートの題です。
	EstimateTemplate = "見積書"
	// EstimateItemsType は見積明細の表の形式です。
	EstimateItemsType = "estimate-items"

	EstimateNoTag       = "見積番号"
	EstimateClientTag   = "見積先"
	EstimatePersonTag   = "見積先担当"
	EstimateDateTag     = "見積日"
	EstimateDueTag      = "受渡期日"
	EstimatePlaceTag    = "受渡場所"
	EstimateTradeTag    = "取引方法"
	EstimateValidTag    = "有効期限"
	EstimateSignerTag   = "見積担当"
	estimateNoteHeading = "備考"
)

// estimateHeads は見積書の紙の上に刷るタグ（並び順）です——見本の並び。
var estimateHeads = []string{EstimateDueTag, EstimatePlaceTag, EstimateTradeTag, EstimateValidTag}

// estimateItemColumns は見積明細の列です（受注明細と同じ名前・同じ型——「前いくらで出したか」を品番で引けるように）。
func estimateItemColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "our-item-id", Label: "弊社品番", Type: cms.ColRef},
		{Field: "item-id", Label: "品番", Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		{Field: "price", Label: "単価", Type: cms.ColNumber},
		{Field: "note", Label: "備考", Type: cms.ColText},
	}
}

// estimateLine は見積明細の1行です。
type estimateLine struct {
	ProductID, ItemID, ItemName, Quantity, Unit, Price, Note string
}

func (l estimateLine) values() map[string]string {
	return map[string]string{
		"our-item-id": l.ProductID, "item-id": l.ItemID, "item-name": l.ItemName,
		"quantity": l.Quantity, "unit": l.Unit, "price": l.Price, "note": l.Note,
	}
}

// estimateLineOf は加工製品ページの index 枚目（0から）の見積計算表から、見積明細の1行を組みます。
func estimateLineOf(productID int, index int) (estimateLine, error) {
	body, err := cms.ReadPageBody(page.FormatID(productID))
	if err != nil {
		return estimateLine{}, err
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return estimateLine{}, err
	}
	tables := tablesOfType(nodes, estimateType)
	if index < 0 || index >= len(tables) {
		return estimateLine{}, errString("その見積計算表が見つかりません（ページを開き直してください）")
	}
	t := tables[index]
	def, hasDef := EstimateProfitRate()
	r := estimateOf(t, def, hasDef)
	if !r.OK {
		return estimateLine{}, errString("確定単価が出ていません: " + r.Why)
	}
	ln := estimateLine{ProductID: page.FormatID(productID), Price: strconv.Itoa(r.Final), Unit: "個"}
	// 数量はロットの行、備考は単価の行の備考（「塗装無し」など——見本の表にあった書き方）。
	rows := rowsOf(t)
	col := headerIndex(rows[0])
	at := func(cells []string, label string) string {
		if i, ok := col[label]; ok && i < len(cells) {
			return strings.TrimSpace(cells[i])
		}
		return ""
	}
	for _, tr := range rows[1:] {
		cells := cellTexts(tr)
		switch at(cells, "工程") {
		case "ロット":
			if v, ok := estimateNumber(at(cells, "数")); ok {
				ln.Quantity = strconv.FormatFloat(v, 'f', -1, 64)
				if u := at(cells, "単位"); u != "" {
					ln.Unit = u
				}
			}
		case estimateUnitPriceRow:
			ln.Note = at(cells, "備考")
		}
	}
	// 品番・品名は加工製品ページのタグから（品番→図面番号、品名→図面名称→題）。
	tags, _ := cms.TagsOfPage(database.DB, productID)
	ln.ItemID = cms.FirstTag(tags, "品番")
	if ln.ItemID == "" {
		ln.ItemID = cms.FirstTag(tags, DrawingNoTag)
	}
	ln.ItemName = cms.FirstTag(tags, ItemNameTag)
	if ln.ItemName == "" {
		ln.ItemName = cms.FirstTag(tags, DrawingNameTag)
	}
	if ln.ItemName == "" {
		ln.ItemName = cms.PageTitleByID(productID)
	}
	return ln, nil
}

// buildEstimateHTML は見積書ページの本文を、テンプレート tmpl を埋めて組みます。
func buildEstimateHTML(tmpl, pageID, client, person, date, signerID string, lines []estimateLine) (string, error) {
	d := cms.NewPageDraft(EstimateTemplate, tmpl)
	title := "見積　" + client
	if strings.TrimSpace(client) == "" {
		title = "見積（客先未定）"
	}
	d.SetTitle(title)
	setHeaderTag(d.DraftBlock, EstimateNoTag, pageID)
	setHeaderTag(d.DraftBlock, EstimateClientTag, client)
	setHeaderTag(d.DraftBlock, EstimatePersonTag, person)
	setHeaderTag(d.DraftBlock, EstimateDateTag, date)
	for k, v := range EstimateTagDefaults() {
		setHeaderTag(d.DraftBlock, k, v)
	}
	setHeaderTag(d.DraftBlock, EstimateSignerTag, signerID)
	rows := make([]map[string]string, 0, len(lines))
	for _, ln := range lines {
		rows = append(rows, ln.values())
	}
	if _, err := fillVocabTable(d.DraftBlock, EstimateItemsType, rows); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// appendEstimateLine は見積書の本文の見積明細へ1行足します（空の取っ掛かりの行は取り除く・見出しに合わせて並べる）。
func appendEstimateLine(body string, ln estimateLine) (string, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	tables := tablesOfType(nodes, EstimateItemsType)
	if len(tables) == 0 {
		return body, false
	}
	t := tables[0]
	rows := rowsOf(t)
	for _, tr := range rows[1:] {
		empty := true
		for _, c := range cellTexts(tr) {
			if strings.TrimSpace(c) != "" {
				empty = false
			}
		}
		if empty {
			tr.Parent.RemoveChild(tr)
		}
	}
	tbody := lastChild(t, "tbody")
	if tbody == nil {
		tbody = t
	}
	vals := ln.values()
	tr := &html.Node{Type: html.ElementNode, Data: "tr"}
	for _, f := range fieldsOfHeader(t, EstimateItemsType) {
		td := &html.Node{Type: html.ElementNode, Data: "td"}
		if v := strings.TrimSpace(vals[f]); v != "" {
			td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
		}
		tr.AppendChild(td)
	}
	tbody.AppendChild(tr)
	return htmldoc.Render(nodes), true
}

// estimatePage は見積書のページ1枚（選ぶ欄に出す）です。
type estimatePage struct {
	ID    int
	Title string
	Date  string
}

// estimatesFor は、その客先の見積書ページを新しい順に返します（読めて書けるもの・最大 limit 枚）。
func estimatesFor(user *auth.User, client string, limit int) []estimatePage {
	want := foldCustomer(client)
	if want == "" {
		return nil
	}
	rows, err := database.DB.Query(`SELECT page_id, value FROM page_tags WHERE name = ?`, EstimateClientTag)
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		var v string
		if rows.Scan(&id, &v) == nil && foldCustomer(v) == want {
			ids = append(ids, id)
		}
	}
	rows.Close()
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	var out []estimatePage
	for _, id := range ids {
		if !canWritePage(user, id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		out = append(out, estimatePage{ID: id, Title: cms.PageTitleByID(id),
			Date: cms.PageTagValue(database.DB, id, EstimateDateTag)})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// AddToEstimateAPIHandler は POST /api/estimate/add です。入力: {product, index, into, client, person, signer}——
// 加工製品ページ product の index 枚目（0から）の見積計算表を、見積書へ1行入れます。into が空なら新しい見積書を作ります
// （見積／年／月）。
func AddToEstimateAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Product string `json:"product"`
		Index   int    `json:"index"`
		Into    string `json:"into"`
		Client  string `json:"client"`
		Person  string `json:"person"`
		Signer  string `json:"signer"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	productID, ok := cms.PageIDOrFail(w, req.Product)
	if !ok {
		return
	}
	pid := pageNum(productID)
	if !page.CanView(user, pid) {
		cms.JSONFail(w, http.StatusNotFound, "加工製品ページが見つかりません")
		return
	}
	ln, err := estimateLineOf(pid, req.Index)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	// 作りかけの見積書へ足す。
	if into := strings.TrimSpace(req.Into); into != "" {
		estID, okID := gateWritablePage(w, r, into)
		if !okID {
			return
		}
		done := false
		if !rewriteBodyOrFail(w, estID, user.Username, func(cur string) string {
			out, ok := appendEstimateLine(cur, ln)
			done = ok
			return out
		}) {
			return
		}
		if !done {
			cms.JSONFail(w, http.StatusConflict, "その見積書に見積明細の表がありません")
			return
		}
		auth.Audit(user.Username, "estimate.add", estID+" ← "+productID)
		cms.WriteJSON(w, map[string]any{"success": true, "page_id": estID, "new": false})
		return
	}
	// 新しい見積書。差出人は署名を持つ人の中に居るかを確かめる（口は誰でも叩ける）。
	signerID := ""
	if want := strings.TrimSpace(req.Signer); want != "" {
		for _, sg := range Signers(user) {
			if page.FormatID(sg.PageID) == want {
				signerID = want
				break
			}
		}
	}
	client := contacts.OrgNameForPage(user, strings.TrimSpace(req.Client))
	person := strings.Join(strings.Fields(req.Person), " ")
	tmpl, err := cms.PageTemplateBody(EstimateTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return
	}
	now := time.Now()
	build := func(pageID string) (string, error) {
		return buildEstimateHTML(tmpl, pageID, client, person, now.Format("2006-01-02"), signerID, []estimateLine{ln})
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return
	}
	boxID, err := cms.EnsureTopLevelBox(EstimateBoxTitle, user.Username)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+EstimateBoxTitle+"」ページを用意できません: "+err.Error())
		return
	}
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, now)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "年月のフォルダを作れません: "+err.Error())
		return
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積書ページを作れません: "+err.Error())
		return
	}
	body, err := build(newID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積書ページを組めません: "+err.Error())
		return
	}
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return
	}
	auth.Audit(user.Username, "estimate.new", newID+" ← "+productID+" "+client)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "new": true})
}

// estimateAddFormHTML は見積計算表の足元の「見積書に入れる」欄です（書ける人にだけ出す）。
func estimateAddFormHTML(user *auth.User, productID int, index int) string {
	db := database.DB
	client := productCustomerOf(db, productID)
	var b strings.Builder
	b.WriteString(`<div class="estimate-add-form" data-estimate-product="` + page.FormatID(productID) +
		`" data-estimate-index="` + strconv.Itoa(index) + `">`)
	b.WriteString(`<span class="estimate-add-head">📄 見積書に入れる</span> `)
	b.WriteString(`<select class="estimate-add-into">`)
	b.WriteString(`<option value="">新しい見積書</option>`)
	for _, e := range estimatesFor(user, client, 5) {
		label := page.FormatID(e.ID) + "　" + e.Title
		if e.Date != "" {
			label += "（" + e.Date + "）"
		}
		b.WriteString(`<option value="` + page.FormatID(e.ID) + `">` + stdhtml.EscapeString(label) + ` へ足す</option>`)
	}
	b.WriteString(`</select> `)
	b.WriteString(`<input type="text" class="estimate-add-client" placeholder="客先" value="` + stdhtml.EscapeString(client) + `"/> `)
	// 担当者の候補は連絡帳のその会社の人（手で打ってもよい）。
	listID := "estimate-persons-" + page.FormatID(productID) + "-" + strconv.Itoa(index)
	b.WriteString(`<input type="text" class="estimate-add-person" placeholder="担当者（空なら御中）" list="` + listID + `"/>`)
	b.WriteString(`<datalist id="` + listID + `">`)
	if orgID, ok := contacts.PartnerByTitle(user, client); ok {
		for _, p := range contacts.PersonsOf(user, orgID) {
			b.WriteString(`<option value="` + stdhtml.EscapeString(p.Title) + `"></option>`)
		}
	}
	b.WriteString(`</datalist> `)
	b.WriteString(estimateSignerSelect(user))
	b.WriteString(` <button type="button" class="estimate-add-go">入れる</button>`)
	b.WriteString(` <span class="estimate-add-say"></span></div>`)
	return b.String()
}

// estimateSignerSelect は見積担当（差出人）を選ぶ欄です（初期値はログイン名と同じ題の人）。
func estimateSignerSelect(user *auth.User) string {
	list := Signers(user)
	def, hasDef := DefaultSigner(user, list)
	var b strings.Builder
	b.WriteString(`<select class="estimate-add-signer" title="見積担当（差出人）">`)
	b.WriteString(`<option value="">差出人（選ぶ）</option>`)
	for _, sg := range list {
		sel := ""
		if hasDef && sg.PageID == def.PageID {
			sel = ` selected`
		}
		b.WriteString(`<option value="` + page.FormatID(sg.PageID) + `"` + sel + `>` + stdhtml.EscapeString(sg.Title) + `</option>`)
	}
	b.WriteString(`</select>`)
	return b.String()
}
