package toho

// ─────────────────────────────────────────────────────────────────────────
// 装置フォルダから選ぶ（2026-10-03）——再見積依頼（rfq_api.go）と見積書を作る（estimate_folder.go）が同じ形で使う。
//
// 利用者:「装置フォルダのページ番号を入力する欄を作り、その下にある加工製品を列挙する一時的な表を作ります」
// （見積依頼）→「見積依頼フォルダと同じように、装置のフォルダのページIDを入れると加工製品を一時的な表に列挙します」（見積）。
// 並びと「加工製品ページかどうか」は「加工製品の一覧」と同じ（productListRows——改定で子ページへ移った旧版は出さない）。
// どのページの番号でもよい（その下を全部見る）。表は画面が組む（本文に残さない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// folderProductsOrFail は ?folder= のページの下にある加工製品ページを返します。断ったときは w に書いて ok=false。
func folderProductsOrFail(w http.ResponseWriter, r *http.Request) (user *auth.User, fid string, rows []productListRow, ok bool) {
	user = auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return nil, "", nil, false
	}
	fid, okID := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("folder")), "/"))
	exists := 0
	if okID {
		// ⚠ 管理者には CanView が無いページでも通るので、在るかを別に見る（無い番号に「0 件」と答えない）。
		database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE id = ?`, pageNum(fid)).Scan(&exists)
	}
	if !okID || exists == 0 || !page.CanView(user, pageNum(fid)) {
		cms.JSONFail(w, http.StatusNotFound, "その番号のページがありません（装置フォルダのページ番号を書いてください）")
		return nil, "", nil, false
	}
	rows, err := productListRows(user, pageNum(fid))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "加工製品を読めませんでした: "+err.Error())
		return nil, "", nil, false
	}
	return user, fid, rows, true
}

// folderProduct は一時的な表の1行の元（加工製品ページ1枚）です——JSON で画面へ返す共通の欄。
type folderProduct struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Machine   string `json:"machine"`
	PartNo    string `json:"part_no"`
	DrawingNo string `json:"drawing_no"`
	Migrating bool   `json:"migrating"`
}

func folderProductOf(p productListRow) folderProduct {
	return folderProduct{ID: page.FormatID(p.PageID), Title: p.Title, Machine: p.Machine,
		PartNo: p.PartNo, DrawingNo: p.DrawingNo, Migrating: p.Migrating}
}
