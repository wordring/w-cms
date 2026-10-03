package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積書を作る——装置フォルダから選ぶ（2026-10-03）
//
// 利用者:「見積依頼フォルダと同じように、装置のフォルダのページIDを入れると加工製品を一時的な表に列挙します。
// 必要な加工製品にチェックを入れてボタンを押すと、見積もりページが出来て、対応する加工製品ページから確定単価を収集して
// 見積明細表を作ります。ボタンを押すとPDFが作られます」。
//
//	見積フォルダ（トップ直下の「見積」）のビュー「見積書を作る」── 装置フォルダの番号 →「一覧を出す」
//	   → 一時的な表（画面だけ・folder_pick.go）: 見積計算表1枚につき1行（ロット・確定単価・単価の行の備考）
//	   → チェック・見積先・担当者・差出人 →「見積書を作る」→ 見積書ページ（見積／年／月）を開く
//	   → そのページの足元の「📄 PDFを作る」（estimate_pdf.go）
//
//   - 行は**見積計算表ごと**——1つの加工製品に見積計算表は何枚もある（塗装あり・なし、ロットごと・【要求】見積 §4）。
//   - 確定単価が出ない表は選べない行にして理由を出す。見積計算表の無い加工製品も並べて「無い」と出す（黙って抜かない）。
//   - 選んだ表のどれか1つでも確定単価が出なければ見積書を作らない——顧客へ出す紙が黙って欠けないように。
//   - 見積書を作る道は見積計算表の「見積書に入れる」と同じ（newEstimatePage）——単価はその時点の値で写して固定。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// EstimateFolderViewType は見積フォルダに置くビュー「見積書を作る」の形式名です。
const EstimateFolderViewType = "estimate-folder"

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type: EstimateFolderViewType, DisplayName: "見積書を作る", Category: "ビュー", Icon: "📄", Element: "section",
		View: true,
	})
	cms.RegisterView(EstimateFolderViewType, estimateFolderViewHTML)
}

// estimateFolderViewHTML は「見積書を作る」の欄です（表は画面が組む——app.js の wireEstimateFolder）。
// 番号の欄だけブラウザに憶える（data-w-remember）——見積先・担当者は一覧を出すたびにその装置のものを入れ直す。
func estimateFolderViewHTML(user *auth.User, pageIDInt int) string {
	id := page.FormatID(pageIDInt)
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">📄 見積書を作る（装置フォルダから）</h3>`)
	b.WriteString(`<p class="unorder-help">装置フォルダのページ番号を書いて「一覧を出す」を押すと、その下の加工製品の<strong>見積計算表</strong>が` +
		`1枚1行で並びます（ロットと確定単価）。チェックして「見積書を作る」を押すと、見積書ページ（見積／年／月）ができて開きます——` +
		`PDF はそのページの「📄 PDFを作る」で。</p>`)
	b.WriteString(`<div class="matsearch-form estimate-folder-form" data-w-remember="1">`)
	b.WriteString(`<label class="matsearch-field"><span>装置フォルダ</span><input type="text" class="matsearch-input" data-estf="folder" placeholder="001234"/></label>`)
	b.WriteString(`<button type="button" class="matsearch-go" data-estf-list="1">一覧を出す</button>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="estimate-folder-list" data-estf-box="1"></div>`)
	listID := "estf-persons-" + id
	b.WriteString(`<div class="matsearch-form estimate-folder-make" data-estf-make="1" hidden>`)
	b.WriteString(`<label class="matsearch-field"><span>見積先</span><input type="text" class="matsearch-input" data-estf="client"/></label>`)
	b.WriteString(`<label class="matsearch-field"><span>担当者</span><input type="text" class="matsearch-input" data-estf="person" placeholder="空なら御中" list="` + listID + `"/></label>`)
	b.WriteString(`<datalist id="` + listID + `" data-estf="persons"></datalist>`)
	b.WriteString(estimateSignerSelect(user))
	b.WriteString(` <button type="button" class="matsearch-go" data-estf-make-go="1">チェックしたもので見積書を作る</button>`)
	b.WriteString(`</div><div class="unorder-result" data-estf-result="1"></div>`)
	return b.String()
}

// EstimateFolderProductsAPIHandler は GET /api/estimate/folder-products?folder=001234 です（読むだけ）。
// 加工製品ごとに見積計算表を表の順に返し、見積先（装置フォルダの社名）とその会社の担当者の候補を添えます。
func EstimateFolderProductsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, fid, rows, ok := folderProductsOrFail(w, r)
	if !ok {
		return
	}
	type table struct {
		Index    int    `json:"index"`
		Quantity string `json:"quantity"`
		Unit     string `json:"unit"`
		Price    string `json:"price"`
		Note     string `json:"note"`
		Why      string `json:"why"`
	}
	type product struct {
		folderProduct
		Tables []table `json:"tables"`
	}
	out := make([]product, 0, len(rows))
	for _, p := range rows {
		item := product{folderProduct: folderProductOf(p), Tables: []table{}}
		opts, _ := estimateOptionsOf(p.PageID)
		for _, o := range opts {
			item.Tables = append(item.Tables, table{Index: o.Index, Quantity: o.Line.Quantity, Unit: o.Line.Unit,
				Price: o.Line.Price, Note: o.Line.Note, Why: o.Why})
		}
		out = append(out, item)
	}
	client := productCustomerOf(database.DB, pageNum(fid))
	persons := []string{}
	if orgID, ok := contacts.PartnerByTitle(user, client); ok {
		for _, p := range contacts.PersonsOf(user, orgID) {
			persons = append(persons, p.Title)
		}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "folder": fid, "title": cms.PageTitleByID(pageNum(fid)),
		"client": client, "persons": persons, "products": out})
}

// EstimateFromFolderAPIHandler は POST /api/estimate/from-folder です。
// 入力: {items: [{product, index}], client, person, signer}——選んだ見積計算表（加工製品ページと何枚目か・0から）を
// 送られた順（画面は一覧の順）に見積明細へ入れた見積書ページを作ります。
func EstimateFromFolderAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []struct {
			Product string `json:"product"`
			Index   int    `json:"index"`
		} `json:"items"`
		Client string `json:"client"`
		Person string `json:"person"`
		Signer string `json:"signer"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "見積書に入れる見積計算表をチェックしてください")
		return
	}
	var lines []estimateLine
	var from []string
	for _, it := range req.Items {
		pid, okP := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(it.Product), "/"))
		if !okP || !page.CanView(user, pageNum(pid)) {
			cms.JSONFail(w, http.StatusNotFound, "加工製品ページが見つかりません: "+it.Product)
			return
		}
		ln, err := estimateLineOf(pageNum(pid), it.Index)
		if err != nil {
			// 1枚でも欠けるなら作らない（顧客へ出す紙が黙って欠けないように）。
			cms.JSONFail(w, http.StatusConflict, "/"+pid+" の見積計算表（"+strconv.Itoa(it.Index+1)+"枚目）: "+err.Error())
			return
		}
		lines = append(lines, ln)
		from = append(from, pid+"#"+strconv.Itoa(it.Index+1))
	}
	newID, client, ok := newEstimatePage(w, user, req.Client, req.Person, req.Signer, lines)
	if !ok {
		return
	}
	auth.Audit(user.Username, "estimate.new", newID+" ← "+strings.Join(from, "・")+" "+client)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "rows": len(lines)})
}
