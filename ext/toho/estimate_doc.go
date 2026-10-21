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
//	   → 見積書ページ（記録／年／月／依頼・テンプレート「見積書」を写す）の見積明細表に1行
//	     （弊社品番・品番・品名・数量＝ロット・単位・単価＝確定単価・備考＝単価の行の備考）
//
// 2026-10-15 からは、写す元は**見積表**（見積計算表の結果の1行・estimate.go）です——欄は見積表の足元、数量＝見積表のロット、
// 単価＝見積表に記録した単価、備考＝見積表の表面処理と備考。2026-10-16 からは、見積表は見積計算書のページにも置かれ、
// 行の弊社品番・品番・品名はその `弊社品番` の加工製品ページから引きます（estimateOptionsOf）。
//
//   - **単価はその時点の値で写して固定**します（出した紙の値は、あとで見積計算表を直しても変わらない）。
//   - **見積番号はページ番号**（部材発注書番号と同じ）。見積日は作った日。取引方法・有効期限は設定の既定（estimate_tags）。
//   - 見積担当（差出人）は連絡帳の署名を持つ人——初期値はログイン名と同じ題の人（部材発注書と同じ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"math"
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
	// EstimateItemsType は見積明細表の形式です。
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

// estimateItemColumns は見積明細表の列です（受注明細表と同じ名前・同じ型——「前いくらで出したか」を品番で引けるように）。
func estimateItemColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "our-item-id", Label: "弊社品番", Type: cms.ColRef},
		{Field: "item-id", Label: "品番", Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		{Field: "price", Label: "単価", Type: cms.ColNumber},
		{Field: "note", Label: "備考", Type: cms.ColText},
		// order はどの案件への見積か（2026-10-19・quote_done.go）——製造ページから作るときに写す。見積を出すと、その案件の行が完了になる。
		{Field: "order", Label: "案件", Type: cms.ColRef},
		// mfg はどの製造ページの分か（2026-10-21・【考察】製造単位の部材発注 §6.1）——書類は記録の年月に置くので、製造ページとは、この列でつながる。紙には刷らない。
		{Field: "mfg", Label: "製造", Type: cms.ColRef},
	}
}

// estimateLine は見積明細表の1行です。
type estimateLine struct {
	ProductID, ItemID, ItemName, Quantity, Unit, Price, Note string
	Order                                                    string // 案件（2026-10-19）
	Mfg                                                      string // 製造ページ（2026-10-21）
}

func (l estimateLine) values() map[string]string {
	return map[string]string{
		"our-item-id": l.ProductID, "item-id": l.ItemID, "item-name": l.ItemName,
		"quantity": l.Quantity, "unit": l.Unit, "price": l.Price, "note": l.Note, "order": l.Order, "mfg": l.Mfg,
	}
}

// estimateLineOf はページ productID（加工製品ページか見積計算書）の index 枚目（0から）の見積表から、見積明細表の1行を組みます。
func estimateLineOf(productID int, index int) (estimateLine, error) {
	opts, err := estimateOptionsOf(productID)
	if err != nil {
		return estimateLine{}, err
	}
	if index < 0 || index >= len(opts) {
		return estimateLine{}, errString("その見積表が見つかりません（ページを開き直してください）")
	}
	if why := opts[index].Why; why != "" {
		return estimateLine{}, errString("単価がありません: " + why)
	}
	return opts[index].Line, nil
}

// estimateOption は加工製品ページの見積表1枚から組んだ見積明細表の1行です（単価が無ければ Why に理由）。
type estimateOption struct {
	Index int
	Line  estimateLine
	Why   string
}

// estimateOptionsOf はページ pageID の見積表を全部、表の順に見積明細表の行にします（見積表が無ければ空）。
// 1つの加工製品に見積表は何枚あってもよい（ロットごと・表面処理ごと——2026-10-15 の形・estimate.go）ので、1枚ずつ組む。
// ⚠ 写すのは**見積表に記録した単価**です（見積計算表から毎回計算しない——2026-10-15 利用者:「結果の単価のみDBに入れるべき」）。
//
// pageID は加工製品ページか見積計算書（2026-10-16〜・estimate_calc_doc.go）です。見積計算書なら、行の弊社品番・品番・品名は
// その `弊社品番` の加工製品ページから引きます。
func estimateOptionsOf(pageID int) ([]estimateOption, error) {
	productID := productOfEstimatePage(pageID)
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return nil, err
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, err
	}
	recs := tablesOfType(nodes, estimateRecordType)
	if len(recs) == 0 {
		return nil, nil
	}
	itemID, itemName := estimateItemNamesOf(productID)
	out := make([]estimateOption, 0, len(recs))
	for i, t := range recs {
		ln, why := estimateLineFromRecord(estimateRecordOf(t))
		if why != "" {
			out = append(out, estimateOption{Index: i, Why: why})
			continue
		}
		ln.ProductID = page.FormatID(productID)
		ln.ItemID, ln.ItemName = itemID, itemName
		out = append(out, estimateOption{Index: i, Line: ln})
	}
	return out, nil
}

// estimateLineFromRecord は見積表の記録から、見積明細表の行の数量（ロット）・単位・単価・備考（表面処理と備考）を組みます。
func estimateLineFromRecord(r estimateRecord) (estimateLine, string) {
	unit, ok := estimateNumber(r.Unit)
	if !ok {
		return estimateLine{}, "単価がまだ記録されていません（見積表の下の「📌 この単価を記録」）"
	}
	ln := estimateLine{Price: strconv.Itoa(int(math.Round(unit))), Unit: "個"}
	if v, ok := estimateNumber(r.Lot); ok {
		ln.Quantity = strconv.FormatFloat(v, 'f', -1, 64)
	}
	var note []string
	for _, s := range []string{r.Surface, r.Note} {
		if s = strings.TrimSpace(s); s != "" {
			note = append(note, s)
		}
	}
	ln.Note = strings.Join(note, "　")
	return ln, ""
}
// estimateItemNamesOf は見積明細表に書く品番・品名を加工製品ページのタグから引きます（品番→図面番号、品名→図面名称→題）。
func estimateItemNamesOf(productID int) (itemID, itemName string) {
	tags, _ := cms.TagsOfPage(database.DB, productID)
	itemID = cms.FirstTag(tags, "品番")
	if itemID == "" {
		itemID = cms.FirstTag(tags, DrawingNoTag)
	}
	itemName = cms.FirstTag(tags, ItemNameTag)
	if itemName == "" {
		itemName = cms.FirstTag(tags, DrawingNameTag)
	}
	if itemName == "" {
		itemName = cms.PageTitleByID(productID)
	}
	return itemID, itemName
}

// buildEstimateHTML は見積書ページの本文を、テンプレート tmpl を埋めて組みます。
func buildEstimateHTML(tmpl, pageID, client, person, date, signerID string, lines []estimateLine) (string, error) {
	d := cms.NewPageDraft(EstimateTemplate, tmpl)
	title := "見積　" + client
	if strings.TrimSpace(client) == "" {
		title = "見積（客先未定）"
	}
	d.SetTitle(issuerTitle(PageKindOurEstimate, title)) // 題の頭に【弊社】（2026-10-16・page_kind.go）
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

// appendEstimateLine は見積書の本文の見積明細表へ1行足します（空の取っ掛かりの行は取り除く・見出しに合わせて並べる）。
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
	if len(rows) == 0 {
		return body, false // 見出しの行も無い表（見出しが分からないので足せない・2026-10-22 の見直しまで panic していた）
	}
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
// ページ product の index 枚目（0から）の見積表を、見積書へ1行入れます。into が空なら新しい見積書を作ります
// （記録／年／月／依頼）。product は押した見積表のあるページ（加工製品ページか見積計算書——2026-10-16〜）で、
// 2026-10-15 までは見積計算表の確定単価でした。
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
			cms.JSONFail(w, http.StatusConflict, "その見積書に見積明細表がありません")
			return
		}
		auth.Audit(user.Username, "estimate.add", estID+" ← "+productID)
		cms.WriteJSON(w, map[string]any{"success": true, "page_id": estID, "new": false})
		return
	}
	// 新しい見積書。
	newID, client, ok := newEstimatePage(w, user, req.Client, req.Person, req.Signer, []estimateLine{ln}, "")
	if !ok {
		return
	}
	auth.Audit(user.Username, "estimate.new", newID+" ← "+productID+" "+client)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "new": true})
}

// newEstimatePage は見積書ページを `記録／年／月／依頼` に作り、lines を見積明細表に入れます（テンプレート「見積書」を写す）。
// 断ったときは w に書いて ok=false。client は社名を書く口（OrgNameForPage）を通した見積先です。
// 見積表の「見積書に入れる」と、ビュー「見積書を作る」（装置フォルダから選ぶ・estimate_folder.go）と、製造ページの
// 「見積書を作る」（quote_done.go）が同じ道を通る。
// mfg が空でなければ、明細の「製造」の列にその製造ページを書きます（2026-10-21 から、製造ページから作っても置き場は記録——
// それまでは製造ページの子に作っていた）。
func newEstimatePage(w http.ResponseWriter, user *auth.User, clientIn, personIn, signerIn string, lines []estimateLine, mfg string) (newID, client string, ok bool) {
	for i := range lines {
		lines[i].Mfg = mfg
	}
	// 差出人は署名を持つ人の中に居るかを確かめる（口は誰でも叩ける——trustedSigner）。
	signerID := trustedSigner(user, signerIn)
	client = contacts.OrgNameForPage(user, strings.TrimSpace(clientIn))
	person := strings.Join(strings.Fields(personIn), " ")
	tmpl, err := cms.PageTemplateBody(EstimateTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return "", "", false
	}
	now := time.Now()
	build := func(pageID string) (string, error) {
		return buildEstimateHTML(tmpl, pageID, client, person, now.Format("2006-01-02"), signerID, lines)
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return "", "", false
	}
	// 置き場は `記録／年／月／依頼`（2026-10-16・record_box.go。月は作った日）。製造ページから作るときも同じ（2026-10-21）。
	monthID, err := recordFolderOr("", user, RecordRequestFolder, now)
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return "", "", false
	}
	newID, err = newBuiltPage(monthID, user, "見積書", build)
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return "", "", false
	}
	return newID, client, true
}

// estimateAddFormHTML は見積表の足元の「見積書に入れる」欄です（書ける人にだけ出す）。productID は見積表のあるページ
// （加工製品ページか見積計算書）で、index はそのページの何枚目の見積表か（0から）。
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
