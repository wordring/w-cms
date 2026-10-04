package toho

// ─────────────────────────────────────────────────────────────────────────
// 過去の販売価格を見積書ページへ移す（2026-10-04）
//
// 利用者:「2026-01-29 ロット20単価9580円はうちの販売価格です」→ 筆者の問い（販売価格をどこに残すか）への答え:
// 「顧客向けの見積書ページとして残してください」。業者の値段を見積依頼書ページへ移した口（/api/rfq/import・rfq_doc.go）と
// 同じ形で、**弊社が出した値段**（ワンノートから加工製品ページに入った「日付 ロット 単価」の書き付け）を見積書ページにする
// ——【要求】見積 §1「あの部品は前いくらで出したか」を見積明細から引けるように。どの行を移すかは道具（実名を含むので
// リポジトリの外）が決め、ここは1枚を作るだけ（管理者だけ）。
//
//   - 見積先は渡されたもの、無ければ1行目の加工製品ページの取引先（productCustomerOf）——どちらも社名を書く口
//     （contacts.OrgNameForPage）を通す。
//   - 置き場はその日の `見積／年／月`。見積日はその日、**送付日もその日**——出した値段なので、未送付の見積書には並べない。
//   - 品番・品名は加工製品ページから（estimateItemNamesOf——見積書を作るときと同じ）。備考の欄に note。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// EstimateImportAPIHandler は POST /api/estimate/import です。
// 入力: {client（空なら加工製品の取引先）, date（YYYY-MM-DD）, note, lines: [{product_id, quantity, unit, price, note}]}。
func EstimateImportAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "管理者だけが使えます（過去の販売価格の移し）")
		return
	}
	var req struct {
		Client string `json:"client"`
		Date   string `json:"date"`
		Note   string `json:"note"`
		Lines  []struct {
			ProductID string `json:"product_id"`
			Quantity  string `json:"quantity"`
			Unit      string `json:"unit"`
			Price     string `json:"price"`
			Note      string `json:"note"`
		} `json:"lines"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	when, err := time.Parse("2006-01-02", strings.TrimSpace(req.Date))
	if err != nil || len(req.Lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "日付（YYYY-MM-DD）と行が要ります")
		return
	}
	clientIn := strings.TrimSpace(req.Client)
	var lines []estimateLine
	for _, l := range req.Lines {
		pid, ok := page.NormalizeID(strings.TrimSpace(l.ProductID))
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "弊社品番（加工製品ページの番号）が読めません: "+l.ProductID)
			return
		}
		id, _ := strconv.Atoi(pid)
		// ⚠ 管理者は無いページでも CanView が通る——在ることはサイドカーで確かめる。
		if _, found := page.ReadSidecar(pid); !found || !page.CanView(user, id) {
			cms.JSONFail(w, http.StatusBadRequest, "加工製品ページ /"+pid+" がありません")
			return
		}
		if clientIn == "" {
			clientIn = productCustomerOf(database.DB, id)
		}
		itemID, itemName := estimateItemNamesOf(id)
		lines = append(lines, estimateLine{ProductID: pid, ItemID: itemID, ItemName: itemName,
			Quantity: strings.TrimSpace(l.Quantity), Unit: strings.TrimSpace(l.Unit), Price: strings.TrimSpace(l.Price),
			Note: strings.TrimSpace(l.Note)})
	}
	client := contacts.OrgNameForPage(user, clientIn)
	date := when.Format("2006-01-02")
	tmpl, err := cms.PageTemplateBody(EstimateTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return
	}
	build := func(pageID string) (string, error) {
		body, err := buildEstimateHTML(tmpl, pageID, client, "", date, "", lines)
		if err != nil {
			return "", err
		}
		body = withTagValues(body, EstimateSentTag, []string{date}, true)
		if note := strings.TrimSpace(req.Note); note != "" {
			return withEstimateNote(body, note)
		}
		return body, nil
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "見積書ページを作れません: "+err.Error())
		return
	}
	newID, ok := importedEstimatePage(w, user, when, build)
	if !ok {
		return
	}
	auth.Audit(user.Username, "estimate.import", newID+" "+client+" "+date)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "client": client, "rows": len(lines)})
}

// importedEstimatePage は `見積／年／月`（その日の年月）にページを作り、build で組んだ本文を書きます。
func importedEstimatePage(w http.ResponseWriter, user *auth.User, when time.Time, build func(pageID string) (string, error)) (string, bool) {
	boxID, err := cms.EnsureTopLevelBox(EstimateBoxTitle, user.Username)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+EstimateBoxTitle+"」ページを用意できません: "+err.Error())
		return "", false
	}
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "年月のフォルダを作れません: "+err.Error())
		return "", false
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積書ページを作れません: "+err.Error())
		return "", false
	}
	body, err := build(newID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積書ページを組めません: "+err.Error())
		return "", false
	}
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return "", false
	}
	return newID, true
}
