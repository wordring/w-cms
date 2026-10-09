package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼書ページを作る（2026-10-03・段2——【考察】見積の依頼と見積書 §2.0d）
//
// 利用者（2026-10-02）:「見積依頼部材表を確認し、差出人、仕入れ先等を入力しボタンを押せば見積依頼書ページが出来ます」
// →（2026-10-03）「見積依頼部材表の下で諸々入力してボタンを押すと「見積依頼ページ」が出来ます」。
//
//	見積依頼部材表（見積依頼の置き場・業者ごとに1枚）──足元の欄（差出人・仕入先・見積依頼日・備考）
//	   →「見積依頼書ページを作る」→ 見積依頼書ページ（見積依頼／年／月・テンプレート「見積依頼書」を写す）
//	     タグ: 見積依頼番号（ページ番号）・仕入先・見積依頼日・見積依頼担当（差出人）・送付日・回答日（空で始まる）
//	     見積依頼明細: 見積依頼部材表の行（状態は「未回答」・単価は空——業者の返事を書く欄）
//	     備考の節: 欄の備考（「○○用」など——紙では明細の下）
//	   → 元の見積依頼部材表は消す（中身は見積依頼書ページへ移った——発注部材表と同じ）
//
//   - **行は本文から読みます**（画面は表の番号だけ送る——ほかの見積依頼の口と同じ）。
//   - **単価は空で写します**——見積依頼部材表に単価があっても、それは業者に聞く前の値（移すときにも空にしている）。
//   - 見積依頼番号は**ページ番号**（発注書番号・見積番号と同じ）。
//   - 紙（PDF）と送る道は次の段（【要求】見積依頼 §2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// 見積依頼書ページの言葉。仕入先は発注書と同じ SupplierTag、送付日は見積書と同じ EstimateSentTag。
const (
	// RFQTemplate は見積依頼書ページを作るテンプレートの題です。
	RFQTemplate       = "見積依頼書"
	RFQNoTag          = "見積依頼番号"
	RFQDateTag        = "見積依頼日"
	RFQSignerTag      = "見積依頼担当"
	RFQAnsweredTag    = "回答日"
	rfqNoteHeading    = "備考"
	rfqLineUnanswered = "未回答"
	rfqLineAnswered   = "回答あり"
)

func init() {
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     RFQTemplate,
		Extension: "toho",
		Why: "見積依頼部材表の「見積依頼書ページを作る」が写します。キャプション「見積依頼明細」の表が要ります（タグ 見積依頼番号・" +
			"仕入先・見積依頼日・見積依頼担当・送付日・回答日と、見出し「備考」の節は、あれば埋めます）。",
	})
}

// rfqDocFormHTML は見積依頼部材表の足元の「見積依頼書ページを作る」欄です（idx は何枚目の見積依頼部材表か・1始まり）。
func rfqDocFormHTML(user *auth.User, pageID string, idx int) string {
	f := func(id, label, ph, typ string) string {
		return searchFieldHTML("rfqdoc", id, label, ph, typ)
	}
	return `<div class="matsearch-form rfq-doc-form" data-rfq-page="` + stdhtml.EscapeString(pageID) +
		`" data-rfq-table="` + strconv.Itoa(idx) + `">` +
		signerFieldHTML(user) +
		f("supplier", "仕入先", "みなと商店", "text") +
		f("date", "見積依頼日", "", "date") +
		f("note", "備考", "○○用", "text") +
		`<button type="button" class="matsearch-go" data-rfq-doc-go="1">見積依頼書ページを作る</button>` +
		`</div><div class="unorder-result" data-rfq-doc-result="1"></div>`
}

// buildRFQDocHTML は見積依頼書ページの本文を、テンプレート tmpl を埋めて組みます。
//
// answered なら返事を貰った見積（過去の見積もりの移し——RFQImportAPIHandler）: 単価を写し、状態は「回答あり」、回答日は date。
func buildRFQDocHTML(tmpl, pageID, supplier, date, note, signerID string, lines []ourOrderLine, answered bool) (string, error) {
	d := cms.NewPageDraft(RFQTemplate, tmpl)
	d.SetTitle("見積依頼　" + supplier)
	setHeaderTag(d.DraftBlock, RFQNoTag, pageID)
	setHeaderTag(d.DraftBlock, SupplierTag, supplier)
	setHeaderTag(d.DraftBlock, RFQDateTag, date)
	setHeaderTag(d.DraftBlock, RFQSignerTag, signerID)
	if answered {
		setHeaderTag(d.DraftBlock, RFQAnsweredTag, date)
	}
	rows := make([]map[string]string, 0, len(lines))
	for _, ln := range lines {
		row := map[string]string{
			"our-item-id": ln.ProductID,
			"for-order":   ln.ForOrder,
			"kind":        ln.Kind,
			"no":          ln.No,
			"work":        ln.Work,
			"spec":        ln.Spec,
			"supplied":    ln.Supplied,
			"item-id":     ln.ItemID,
			"item-name":   itemNameOf(ln),
			"material":    ln.Material,
			"shape":       ln.Shape,
			"size":        ln.Size,
			"color":       ln.Color,
			"quantity":    ln.Quantity,
			"unit":        ln.Unit,
			"note":        ln.Note,
			// 単価は空——業者の返事を書く欄（紙では空欄で刷る）。状態は「未回答」から。
			"status": rfqLineUnanswered,
		}
		if answered {
			row["cost"] = moneyOrEmpty(ln.Cost)
			row["status"] = rfqLineAnswered
		}
		rows = append(rows, row)
	}
	if _, err := fillVocabTable(d.DraftBlock, RFQItemsType, rows); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, ln := range strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			b.WriteString(`<p>` + stdhtml.EscapeString(ln) + `</p>`)
		}
	}
	if b.Len() > 0 {
		sec, err := d.RequireContainer(rfqNoteHeading)
		if err != nil {
			return "", err
		}
		sec.SetContent(b.String())
	}
	return d.HTML(), nil
}

// tableRowNumbers は本文の n 枚目（1始まり）の vocabType の表の行番号（見出しを除いた 1..k）を返します。
func tableRowNumbers(body, vocabType string, n int) []int {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	tables := tablesOfType(nodes, vocabType)
	if n < 1 || n > len(tables) {
		return nil
	}
	k := len(rowsOf(tables[n-1])) - 1
	out := make([]int, 0, k)
	for i := 1; i <= k; i++ {
		out = append(out, i)
	}
	return out
}

// rfqDraftLinesOf は本文の n 枚目の見積依頼部材表の行（空でないもの）を読みます（本文は変えない）。
func rfqDraftLinesOf(body string, n int) ([]ourOrderLine, bool) {
	_, lines, ok := takeTableRows(body, RFQDraftType, n, tableRowNumbers(body, RFQDraftType, n), true)
	if !ok {
		return nil, false
	}
	var out []ourOrderLine
	for _, ln := range lines {
		if !lineIsEmpty(ln) {
			out = append(out, ln)
		}
	}
	return out, true
}

// RFQNewDocAPIHandler は POST /api/rfq/new です。入力: {page_id, table, supplier, signer, date, note}——
// page_id の table 枚目（1始まり）の見積依頼部材表から、見積依頼書ページを1枚作ります（その表は消す）。
//
// ⚠ **仕入先が無ければ作りません**——見積依頼書は1枚に1社（相見積もりは業者ごとに1枚ずつ）。
func RFQNewDocAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID   string `json:"page_id"`
		Table    int    `json:"table"`
		Supplier string `json:"supplier"`
		Signer   string `json:"signer"`
		Date     string `json:"date"`
		Note     string `json:"note"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	supplier := strings.TrimSpace(req.Supplier)
	if supplier == "" {
		cms.JSONFail(w, http.StatusBadRequest, "仕入先を入れてください（見積依頼書は1枚に1社です）")
		return
	}
	boxPage, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	cur, err := cms.ReadPageBody(boxPage)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を読めません: "+err.Error())
		return
	}
	lines, found := rfqDraftLinesOf(cur, req.Table)
	if !found {
		cms.JSONFail(w, http.StatusConflict, "その見積依頼部材表が見つかりません（ページを読み直してください）")
		return
	}
	if len(lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼部材表に行がありません")
		return
	}
	when := time.Now()
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(req.Date)); err == nil {
		when = t
	}
	// 差出人は署名を持つ人の中に居るかを確かめる（口は誰でも叩ける——trustedSigner）。
	signerID := trustedSigner(user, req.Signer)
	// ページを作る前にテンプレートと器を確かめる（作ってから断ると「作成中」のページが残る）。
	tmpl, err := cms.PageTemplateBody(RFQTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積依頼書ページを作れません: "+err.Error())
		return
	}
	build := func(pageID string) (string, error) {
		return buildRFQDocHTML(tmpl, pageID, supplier, when.Format("2006-01-02"), req.Note, signerID, lines, false)
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積依頼書ページを作れません: "+err.Error())
		return
	}
	boxID, err := cms.EnsureTopLevelBox(RFQBoxTitle, user.Username)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+RFQBoxTitle+"」ページを用意できません: "+err.Error())
		return
	}
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "年月のフォルダを作れません: "+err.Error())
		return
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積依頼書ページを作れません: "+err.Error())
		return
	}
	body, err := build(newID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積依頼書ページを組めません: "+err.Error())
		return
	}
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return
	}
	auth.Audit(user.Username, "rfq.new", newID+" "+supplier+" "+strconv.Itoa(len(lines))+"行 ← "+boxPage+"#"+strconv.Itoa(req.Table))

	// 元の見積依頼部材表を消す（中身は見積依頼書ページへ移った）。⚠ 失敗しても見積依頼書は取り消さない——理由を添えるだけ。
	out := map[string]any{"success": true, "page_id": newID, "rows": len(lines)}
	removed := false
	if err := cms.RewriteBody(boxPage, user.Username, func(cur string) string {
		next, _, ok := takeTableRows(cur, RFQDraftType, req.Table, tableRowNumbers(cur, RFQDraftType, req.Table), true)
		removed = ok
		if !ok {
			return cur
		}
		return next
	}); err != nil || !removed {
		out["draft_note"] = "⚠ 元の見積依頼部材表を片付けられませんでした（手で消してください）"
	}
	cms.WriteJSON(w, out)
}

// RFQImportAPIHandler は POST /api/rfq/import です（2026-10-04・**管理者だけ**）——過去の見積もり（ワンノートから加工製品ページに
// 入った「日付　業者　見積　ロット　価格」の行）を見積依頼書ページへ移す口。入力: {supplier, date, note, lines}（lines の cost が単価）。
//
// 利用者:「専用の見積依頼書ページを作り、そこに入れるようにしましょうか？」——業者の値段の置き場を見積依頼明細の単価の列1つに
// そろえる（あとで作る単価表は見積依頼明細だけを読めばよい）。返事を貰った見積なので、単価に値段・状態は「回答あり」・
// 回答日は date（見積依頼日も date——依頼した日は分からない）。置き場は 見積依頼／年／月（date の年月）。
// 1回で1枚（業者×日付）。何枚も作り、二重に作らない記録を持つのは移す道具の仕事（実名を含む一覧を読むのでリポジトリの外）。
func RFQImportAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "管理者だけが使えます（過去の見積もりの移し）")
		return
	}
	var req struct {
		Supplier string         `json:"supplier"`
		Date     string         `json:"date"`
		Note     string         `json:"note"`
		Lines    []ourOrderLine `json:"lines"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	newID, code, err := createAnsweredRFQPage(user, req.Supplier, req.Date, req.Note, req.Lines)
	if err != nil {
		cms.JSONFail(w, code, err.Error())
		return
	}
	auth.Audit(user.Username, "rfq.import", newID+" "+strings.TrimSpace(req.Supplier)+" "+strings.TrimSpace(req.Date)+" "+strconv.Itoa(len(req.Lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "rows": len(req.Lines)})
}

// createAnsweredRFQPage は返事の来た見積依頼書ページを `見積依頼／年／月` に1枚作ります——単価入り・回答あり・回答日と見積依頼日は date
// （過去の見積もりの移し〔RFQImportAPIHandler〕と、移行期の返事から作る〔rfq_reply_make.go〕が共有する・2026-10-05 に切り出した）。
// 断るときは HTTP の状態とともに返す。
func createAnsweredRFQPage(user *auth.User, supplier, date, note string, lines []ourOrderLine) (string, int, error) {
	supplier = strings.TrimSpace(supplier)
	when, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if supplier == "" || err != nil || len(lines) == 0 {
		return "", http.StatusBadRequest, errString("業者・日付（YYYY-MM-DD）・行が要ります")
	}
	tmpl, err := cms.PageTemplateBody(RFQTemplate)
	if err != nil {
		return "", http.StatusConflict, errString("見積依頼書ページを作れません: " + err.Error())
	}
	day := when.Format("2006-01-02")
	build := func(pageID string) (string, error) {
		return buildRFQDocHTML(tmpl, pageID, supplier, day, note, "", lines, true)
	}
	if _, err := build(""); err != nil {
		return "", http.StatusConflict, errString("見積依頼書ページを作れません: " + err.Error())
	}
	boxID, err := cms.EnsureTopLevelBox(RFQBoxTitle, user.Username)
	if err != nil {
		return "", http.StatusInternalServerError, errString("「" + RFQBoxTitle + "」ページを用意できません: " + err.Error())
	}
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		return "", http.StatusInternalServerError, errString("年月のフォルダを作れません: " + err.Error())
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		return "", http.StatusInternalServerError, errString("見積依頼書ページを作れません: " + err.Error())
	}
	body, err := build(newID)
	if err != nil {
		return "", http.StatusInternalServerError, errString("見積依頼書ページを組めません: " + err.Error())
	}
	// 品番・品名が空の行は加工製品ページから埋める（利用者:「品番と品名の欄が埋まらない事には、人間には見てわかりません」）。
	body, _ = fillRFQItemNames(body)
	if err := cms.RewriteBody(newID, user.Username, func(string) string { return body }); err != nil {
		return "", http.StatusInternalServerError, errString("本文を書けません: " + err.Error())
	}
	return newID, http.StatusOK, nil
}

// fillRFQItemNames は見積依頼明細の行のうち、品番・品名が空のセルを弊社品番の加工製品ページから埋めます（埋めたセルの数を返す）。
// 品番は加工製品の 品番（無ければ図面番号）、品名は 品名（無ければ図面名称・題）——estimateItemNamesOf と同じ引き方。
// 利用者（2026-10-04）:「品番と品名の欄が埋まらない事には、人間には見てわかりません」——過去の見積もりを移した行は弊社品番（ページ番号）と
// 「塗装」だけで、何の値段か読めなかった。⚠ 空のセルだけ埋める（元の行から読んだ品名「ホイールカバー（上）」などは残す）。
func fillRFQItemNames(body string) (string, int) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, 0
	}
	filled := 0
	memo := map[int][2]string{}
	for _, t := range tablesOfType(nodes, RFQItemsType) {
		rows := rowsOf(t)
		if len(rows) < 2 {
			continue
		}
		col := headerIndex(rows[0])
		pc, okP := col["弊社品番"]
		ic, okI := col["品番"]
		nc, okN := col["品名"]
		if !okP || (!okI && !okN) {
			continue
		}
		for _, tr := range rows[1:] {
			cells := cellsOf(tr)
			if pc >= len(cells) {
				continue
			}
			pid, ok := page.NormalizeID(strings.TrimSpace(textOf(cells[pc])))
			if !ok {
				continue
			}
			n := pageNum(pid)
			names, seen := memo[n]
			if !seen {
				id, name := estimateItemNamesOf(n)
				names = [2]string{id, name}
				memo[n] = names
			}
			if okI && ic < len(cells) && strings.TrimSpace(textOf(cells[ic])) == "" && names[0] != "" {
				setCellText(cells[ic], names[0])
				filled++
			}
			if okN && nc < len(cells) && strings.TrimSpace(textOf(cells[nc])) == "" && names[1] != "" {
				setCellText(cells[nc], names[1])
				filled++
			}
		}
	}
	if filled == 0 {
		return body, 0
	}
	return htmldoc.Render(nodes), filled
}

// RFQFillNamesAPIHandler は POST /api/rfq/fill-names です（2026-10-04・管理者だけ）——見積依頼書ページの見積依頼明細の空の品番・品名を
// 加工製品ページから埋めます（過去の見積もりを移したページの後始末。新しく移すぶんは RFQImportAPIHandler が埋める）。
func RFQFillNamesAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "管理者だけが使えます")
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return
	}
	filled := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, n := fillRFQItemNames(cur)
		filled = n
		return out
	}) {
		return
	}
	if filled > 0 {
		auth.Audit(user.Username, "rfq.fill-names", pageID+" "+strconv.Itoa(filled)+"セル")
	}
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "filled": filled})
}

// RFQSetCellsAPIHandler は POST /api/rfq/set-cells です（2026-10-04・管理者だけ）——見積依頼明細（1枚目）のセルを、**いまの値を
// 確かめてから**書き換えます。入力: {page_id, edits: [{row（見出しを除いた何行目か・1始まり）, expect: {列: いまの値}, set: {列: 新しい値}}]}。
// 1つでも合わなければ何も書きません（409・どこが違うかを返す）——人が直したあとの行を黙って上書きしないため。
//
// 過去の見積もりを移した行の後始末に使う（利用者 10-04:「数量の記述が無いと1個にしているようですが、ちょっとまずいです…空欄にするのが
// 妥当では？」——1回目の移しで数の無い行に 1 を入れていた）。
func RFQSetCellsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "管理者だけが使えます")
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Edits  []struct {
			Row    int               `json:"row"`
			Expect map[string]string `json:"expect"`
			Set    map[string]string `json:"set"`
		} `json:"edits"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return
	}
	var mismatch string
	changed := 0
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		nodes, err := htmldoc.ParseFragment(cur)
		if err != nil {
			mismatch = "本文を読めません"
			return cur
		}
		tables := tablesOfType(nodes, RFQItemsType)
		if len(tables) == 0 {
			mismatch = "見積依頼明細の表がありません"
			return cur
		}
		rows := rowsOf(tables[0])
		col := headerIndex(rows[0])
		cell := func(row int, label string) (*html.Node, bool) {
			i, ok := col[label]
			if !ok || row < 1 || row >= len(rows) {
				return nil, false
			}
			cells := cellsOf(rows[row])
			if i >= len(cells) {
				return nil, false
			}
			return cells[i], true
		}
		// まず全部を確かめる（1つでも合わなければ何も書かない）。
		for _, e := range req.Edits {
			for label, want := range e.Expect {
				c, ok := cell(e.Row, label)
				if !ok || strings.TrimSpace(textOf(c)) != strings.TrimSpace(want) {
					got := ""
					if ok {
						got = strings.TrimSpace(textOf(c))
					}
					mismatch = strconv.Itoa(e.Row) + "行目の「" + label + "」が「" + want + "」ではありません（いま「" + got + "」）"
					return cur
				}
			}
			for label := range e.Set {
				if _, ok := cell(e.Row, label); !ok {
					mismatch = strconv.Itoa(e.Row) + "行目に「" + label + "」の列がありません"
					return cur
				}
			}
		}
		for _, e := range req.Edits {
			for label, v := range e.Set {
				c, _ := cell(e.Row, label)
				setCellText(c, strings.TrimSpace(v))
				changed++
			}
		}
		return htmldoc.Render(nodes)
	}) {
		return
	}
	if mismatch != "" {
		cms.JSONFail(w, http.StatusConflict, "書き換えていません: "+mismatch)
		return
	}
	auth.Audit(user.Username, "rfq.set-cells", pageID+" "+strconv.Itoa(changed)+"セル")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "changed": changed})
}
